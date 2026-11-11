package auth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// OAuth wire vocabulary for the operator logins (RFC 6749, 7521, 7523, 8693).
const (
	grantTypeClientCredentials = "client_credentials"
	grantTypeTokenExchange     = "urn:ietf:params:oauth:grant-type:token-exchange"
	assertionTypeJWTBearer     = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	tokenTypeIDToken           = "urn:ietf:params:oauth:token-type:id_token"
	operationsAudienceURN      = "urn:portos:operations"

	// AlgRS256 and AlgES256 are the client-assertion signing algorithms.
	AlgRS256 = "RS256"
	AlgES256 = "ES256"

	assertionLifetime = 240 * time.Second // under the 300 s server cap, tolerating clock skew
	tokenPath         = "/auth/token"
	// deviceAuthorizationPath is the RFC 8628 device authorization endpoint.
	deviceAuthorizationPath = "/auth/device-authorization"
	// iamCredentialsURL serves generateIdToken, which needs only roles/iam.serviceAccountOpenIdTokenCreator
	// (gcloud's --impersonate-service-account first mints an access token, which needs TokenCreator).
	iamCredentialsURL = "https://iamcredentials.googleapis.com"
	maxTokenBody      = 1 << 20
)

// serviceAccountPattern bounds the service account placed in the IAM Credentials request path.
var serviceAccountPattern = regexp.MustCompile(`^[A-Za-z0-9._@-]+$`)

const minRSAKeyBits = 2048

// CommandRunner runs an external program and returns its standard output. It exists so tests
// can replace gcloud.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands with os/exec.
type ExecRunner struct{}

// Run executes the command. Standard error is returned in the error text, never standard output.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// ClientCredentialsParams configure the private_key_jwt login.
type ClientCredentialsParams struct {
	AuthURL  string // OAuth issuer origin; the token endpoint and assertion aud are AuthURL + /auth/token
	ClientID string
	KeyFile  string
	Alg      string // RS256 or ES256; empty selects by key type
	KeyID    string // optional kid header
}

// GCPParams configure the Google service-account impersonation login.
type GCPParams struct {
	AuthURL        string
	ServiceAccount string
	Audience       string
}

// OperatorLogin signs operators in without a browser and stores a non-refreshable token.
type OperatorLogin interface {
	ClientCredentials(ctx context.Context, params ClientCredentialsParams) error
	GCPImpersonate(ctx context.Context, params GCPParams) error
}

type operatorLogin struct {
	httpClient *http.Client
	storage    TokenStorage
	runner     CommandRunner
	now        func() time.Time
	iamURL     string
}

// NewOperatorLogin creates the operator login service.
func NewOperatorLogin(httpClient *http.Client, storage TokenStorage, runner CommandRunner) OperatorLogin {
	return &operatorLogin{httpClient: httpClient, storage: storage, runner: runner, now: time.Now, iamURL: iamCredentialsURL}
}

// NewExecRunner provides the production command runner.
func NewExecRunner() CommandRunner { return ExecRunner{} }

func tokenEndpoint(baseURL string) string { return strings.TrimRight(baseURL, "/") + tokenPath }

// ClientCredentials signs a client assertion with the key file and exchanges it for a token.
func (l *operatorLogin) ClientCredentials(ctx context.Context, p ClientCredentialsParams) error {
	endpoint := tokenEndpoint(p.AuthURL)
	assertion, err := l.signAssertion(p, endpoint)
	if err != nil {
		return err
	}
	form := url.Values{
		"grant_type":            {grantTypeClientCredentials},
		"client_id":             {p.ClientID},
		"client_assertion_type": {assertionTypeJWTBearer},
		"client_assertion":      {assertion},
	}
	return l.exchange(ctx, endpoint, form)
}

// signAssertion builds the RFC 7523 client assertion. The caller never logs it.
func (l *operatorLogin) signAssertion(p ClientCredentialsParams, audience string) (string, error) {
	key, err := loadPrivateKey(p.KeyFile)
	if err != nil {
		return "", err
	}
	method, err := signingMethod(key, p.Alg)
	if err != nil {
		return "", err
	}
	jti, err := randomJTI()
	if err != nil {
		return "", err
	}
	now := l.now()
	token := jwt.NewWithClaims(method, jwt.MapClaims{
		"iss": p.ClientID,
		"sub": p.ClientID,
		"aud": audience,
		"iat": now.Unix(),
		"exp": now.Add(assertionLifetime).Unix(),
		"jti": jti,
	})
	if p.KeyID != "" {
		token.Header["kid"] = p.KeyID
	}
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("failed to sign client assertion: %w", err)
	}
	return signed, nil
}

func randomJTI() (string, error) {
	buf := make([]byte, 16) // 128 bits
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("failed to generate jti: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func loadPrivateKey(path string) (crypto.Signer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("key file %s is not PEM encoded", path)
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := k.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	return nil, fmt.Errorf("key file %s must hold an RSA or EC P-256 private key", path)
}

// signingMethod picks RS256 for RSA keys and ES256 for P-256 keys, and rejects any mismatch.
func signingMethod(key crypto.Signer, alg string) (jwt.SigningMethod, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		if k.N.BitLen() < minRSAKeyBits {
			return nil, fmt.Errorf("RSA key must be at least %d bits", minRSAKeyBits)
		}
		if alg == "" || alg == AlgRS256 {
			return jwt.SigningMethodRS256, nil
		}
	case *ecdsa.PrivateKey:
		if k.Curve != elliptic.P256() {
			return nil, fmt.Errorf("EC key must use curve P-256")
		}
		if alg == "" || alg == AlgES256 {
			return jwt.SigningMethodES256, nil
		}
	default:
		return nil, fmt.Errorf("unsupported key type: use an RSA or EC P-256 private key")
	}
	return nil, fmt.Errorf("--alg %s does not match the key type (use %s or %s)", alg, AlgRS256, AlgES256)
}

// GCPImpersonate gets a Google ID token for the service account from the IAM Credentials API and
// exchanges it.
// The ID token stays in memory: it is never written to disk or logged.
func (l *operatorLogin) GCPImpersonate(ctx context.Context, p GCPParams) error {
	if !serviceAccountPattern.MatchString(p.ServiceAccount) {
		return fmt.Errorf("service account %q is not a valid identifier", p.ServiceAccount)
	}
	if p.Audience == "" {
		return fmt.Errorf("a Google audience is required: pass --audience or set operationsAudience on the environment")
	}
	idToken, err := l.generateIDToken(ctx, p.ServiceAccount, p.Audience)
	if err != nil {
		return err
	}
	form := url.Values{
		"grant_type":         {grantTypeTokenExchange},
		"subject_token":      {idToken},
		"subject_token_type": {tokenTypeIDToken},
		"audience":           {operationsAudienceURN},
	}
	return l.exchange(ctx, tokenEndpoint(p.AuthURL), form)
}

// generateIDToken asks the IAM Credentials API for the service account's ID token, authenticated as
// the gcloud user. The caller needs only roles/iam.serviceAccountOpenIdTokenCreator on the account.
func (l *operatorLogin) generateIDToken(ctx context.Context, serviceAccount, audience string) (string, error) {
	out, err := l.runner.Run(ctx, "gcloud", "auth", "print-access-token")
	if err != nil {
		return "", fmt.Errorf("gcloud could not provide your access token: %w", err)
	}
	userToken := strings.TrimSpace(string(out))
	if userToken == "" {
		return "", fmt.Errorf("gcloud returned an empty access token")
	}
	body, err := json.Marshal(map[string]any{"audience": audience, "includeEmail": true})
	if err != nil {
		return "", fmt.Errorf("failed to encode the ID token request: %w", err)
	}
	endpoint := l.iamURL + "/v1/projects/-/serviceAccounts/" + url.PathEscape(serviceAccount) + ":generateIdToken"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("failed to create the ID token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+userToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to request an ID token for %s: %w", serviceAccount, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		return "", fmt.Errorf("failed to read the ID token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("could not mint an ID token for %s (HTTP %d): you need roles/iam.serviceAccountOpenIdTokenCreator on it", serviceAccount, resp.StatusCode)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &minted); err != nil || minted.Token == "" {
		return "", fmt.Errorf("the IAM Credentials API returned no ID token")
	}
	return minted.Token, nil
}

// exchange posts the form to the token endpoint and stores the returned token without a refresh token.
func (l *operatorLogin) exchange(ctx context.Context, endpoint string, form url.Values) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := l.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenBody))
	if err != nil {
		return fmt.Errorf("failed to read token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var oauthErr OAuthError
		if json.Unmarshal(body, &oauthErr) == nil && oauthErr.ErrorCode != "" {
			return &oauthErr
		}
		return fmt.Errorf("token request failed with status %d", resp.StatusCode)
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return fmt.Errorf("token response did not contain an access token")
	}
	// Operator login replaces any existing session (browser tokens, refresh token and DPoP key).
	return l.storage.SaveCredentials(&Credentials{AccessToken: tok.AccessToken, TokenType: "Bearer"})
}
