package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/portpowered/portos-cli/internal/config"
)

// deviceAuthClient implements DeviceAuthClient for OAuth 2.0 Device Code Grant flow
type deviceAuthClient struct {
	httpClient     *http.Client
	configProvider *config.ManagerProvider
}

// NewDeviceAuthClient creates a new device authorization client
func NewDeviceAuthClient(httpClient *http.Client, configProvider *config.ManagerProvider) DeviceAuthClient {
	return &deviceAuthClient{
		httpClient:     httpClient,
		configProvider: configProvider,
	}
}

func (c *deviceAuthClient) getAuthBaseURL() (string, error) {
	mgr, err := c.configProvider.GetManager()
	if err != nil {
		return "", err
	}
	cfg, err := mgr.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	return strings.TrimSuffix(cfg.AuthBaseURL, "/"), nil
}

// RequestDeviceAuthorization initiates device authorization by sending device public key
func (c *deviceAuthClient) RequestDeviceAuthorization(ctx context.Context, req DeviceAuthorizationRequest) (*DeviceAuthorizationResponse, error) {
	baseURL, err := c.getAuthBaseURL()
	if err != nil {
		return nil, err
	}
	// Build form data
	data := url.Values{}
	data.Set("client_id", req.ClientID)
	data.Set("device_pubkey", req.DevicePubkey)
	if len(req.Scope) > 0 {
		data.Set("scope", strings.Join(req.Scope, " "))
	}

	// Create request
	reqURL := baseURL + deviceAuthorizationPath
	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Add DPoP header if provided
	if req.DPoPToken != "" {
		httpReq.Header.Set("DPoP", req.DPoPToken)
	}

	// Execute request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		var oauthErr OAuthError
		if err := json.Unmarshal(body, &oauthErr); err == nil {
			return nil, &oauthErr
		}
		return nil, fmt.Errorf("device authorization failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse successful response
	var deviceResp DeviceAuthorizationResponse
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &deviceResp, nil
}

// PollForToken polls the token endpoint with device code until authorization completes
// Implements RFC 8628 polling mechanism with proper error handling
// Generates a fresh DPoP token for each polling request to ensure unique jti
func (c *deviceAuthClient) PollForToken(ctx context.Context, req TokenPollRequest) (*TokenResponse, error) {
	reqURL := req.TokenEndpoint
	if reqURL == "" {
		baseURL, err := c.getAuthBaseURL()
		if err != nil {
			return nil, err
		}
		reqURL = baseURL + tokenPath
	}

	// Convert private key bytes to ed25519.PrivateKey
	if len(req.DPoPPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key size: expected %d, got %d", ed25519.PrivateKeySize, len(req.DPoPPrivateKey))
	}
	privateKey := ed25519.PrivateKey(req.DPoPPrivateKey)

	// Poll with exponential backoff (starting with interval from device authorization response)
	// For now, use a fixed 5 second interval - this should come from DeviceAuthorizationResponse
	pollInterval := 5 * time.Second
	maxAttempts := 60 // Maximum number of polling attempts (5 minutes total with 5s interval)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		tokenResp, oauthErr, err := c.requestToken(ctx, reqURL, privateKey, req)
		if err != nil {
			return nil, err
		}
		if tokenResp != nil {
			return tokenResp, nil
		}

		// Handle OAuth error codes per RFC 8628
		switch oauthErr.ErrorCode {
		case "authorization_pending":
			// User hasn't completed authorization yet - continue polling
			if attempt < maxAttempts-1 {
				if err := waitPollInterval(ctx, pollInterval); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("authorization pending: user has not completed authorization")

		case "slow_down":
			// Increase polling interval by 5 seconds as per RFC 8628
			pollInterval += 5 * time.Second
			if attempt < maxAttempts-1 {
				if err := waitPollInterval(ctx, pollInterval); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("authorization pending: slow_down received")

		case "expired_token":
			return nil, fmt.Errorf("device code expired: %s", oauthErr.ErrorDescription)

		case "access_denied":
			return nil, fmt.Errorf("user denied authorization: %s", oauthErr.ErrorDescription)

		default:
			return nil, fmt.Errorf("token request failed: %s - %s", oauthErr.ErrorCode, oauthErr.ErrorDescription)
		}
	}

	return nil, fmt.Errorf("polling timeout: maximum attempts exceeded")
}

// waitPollInterval sleeps for d or until the context is cancelled.
func waitPollInterval(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// requestToken performs one polling request with a fresh DPoP proof. It returns
// either a token response (success) or the parsed OAuth error to be interpreted
// by the caller; transport and non-OAuth failures are returned as errors.
func (c *deviceAuthClient) requestToken(ctx context.Context, reqURL string, privateKey ed25519.PrivateKey, req TokenPollRequest) (*TokenResponse, *OAuthError, error) {
	// Generate fresh DPoP token for each request (each needs unique jti for replay protection)
	dpopToken, err := generateDPoPToken(privateKey, "POST", reqURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate DPoP token: %w", err)
	}

	// Build form data
	data := url.Values{}
	data.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	data.Set("device_code", req.DeviceCode)
	data.Set("client_id", req.ClientID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("DPoP", dpopToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to execute request: %w", err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		var tokenResp TokenResponse
		if err := json.Unmarshal(body, &tokenResp); err != nil {
			return nil, nil, fmt.Errorf("failed to parse token response: %w", err)
		}
		return &tokenResp, nil, nil
	}

	var oauthErr OAuthError
	if err := json.Unmarshal(body, &oauthErr); err != nil {
		// Non-OAuth error format
		return nil, nil, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(body))
	}
	return nil, &oauthErr, nil
}

// generateKeyPair generates an Ed25519 keypair for device authentication
func generateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// encodePublicKeyBase64 encodes a public key as base64 for transmission
func encodePublicKeyBase64(pubKey ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pubKey)
}

// generateDPoPToken generates a DPoP JWT token for proof-of-possession
// Reference: RFC 9449 Section 6.1 (DPoP JWT Claims)
func generateDPoPToken(privateKey ed25519.PrivateKey, httpMethod, httpURI string) (string, error) {
	// Generate unique JTI for replay protection
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", fmt.Errorf("failed to generate JTI: %w", err)
	}
	jti := base64.RawURLEncoding.EncodeToString(jtiBytes)

	// Get JWK thumbprint for jkt claim
	pubKey := privateKey.Public().(ed25519.PublicKey)
	jkt, err := calculateJWKThumbprint(pubKey)
	if err != nil {
		return "", fmt.Errorf("failed to calculate JWK thumbprint: %w", err)
	}

	// Create DPoP claims
	now := time.Now()
	claims := jwt.MapClaims{
		"htu": httpURI,    // HTTP URI
		"htm": httpMethod, // HTTP Method
		"iat": now.Unix(), // Issued At
		"jti": jti,        // JWT ID (for replay protection)
		"jkt": jkt,        // JWK Thumbprint
	}

	// RFC 9449 section 4.2: the proof carries its public key in the jwk header
	// and is typed dpop+jwt; the server reads the key from the header only.
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["typ"] = "dpop+jwt"
	token.Header["jwk"] = generateJWK(pubKey)

	// Sign the token with Ed25519 private key
	// Note: jwt-go expects crypto.PrivateKey, ed25519.PrivateKey implements this
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign DPoP token: %w", err)
	}

	return tokenString, nil
}

// calculateJWKThumbprint calculates the JWK thumbprint according to RFC 7638
// For Ed25519 (OKP), the canonical JSON is: {"crv":"Ed25519","kty":"OKP","x":"<base64url-encoded-public-key>"}
func calculateJWKThumbprint(pubKey ed25519.PublicKey) (string, error) {
	// Encode public key as base64url
	x := base64.RawURLEncoding.EncodeToString(pubKey)

	// Create canonical JSON (sorted alphabetically by key name)
	// {"crv":"Ed25519","kty":"OKP","x":"..."}
	canonicalJSON := fmt.Sprintf(`{"crv":"Ed25519","kty":"OKP","x":"%s"}`, x)

	// Calculate SHA-256 hash
	hash := sha256.Sum256([]byte(canonicalJSON))

	// Encode as base64url
	return base64.RawURLEncoding.EncodeToString(hash[:]), nil
}

// generateJWK creates a JWK representation of the Ed25519 public key
// Reference: RFC 8037 Section 2 (OKP keys)
func generateJWK(pubKey ed25519.PublicKey) map[string]interface{} {
	return map[string]interface{}{
		"kty": "OKP",
		"crv": "Ed25519",
		"x":   base64.RawURLEncoding.EncodeToString(pubKey),
	}
}
