package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/portpowered/portos-cli/internal/config"
)

// browserAuthClient implements BrowserAuthClient for the OAuth 2.0 token endpoint: the authorization
// code + PKCE exchange and the refresh_token grant.
type browserAuthClient struct {
	httpClient     *http.Client
	configProvider *config.ManagerProvider
}

// NewBrowserAuthClient creates a new browser-based OAuth auth client.
func NewBrowserAuthClient(httpClient *http.Client, configProvider *config.ManagerProvider) BrowserAuthClient {
	return &browserAuthClient{
		httpClient:     httpClient,
		configProvider: configProvider,
	}
}

func (c *browserAuthClient) getAuthBaseURL() (string, error) {
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

// ExchangeCodeForToken exchanges an authorization code for an access token.
func (c *browserAuthClient) ExchangeCodeForToken(ctx context.Context, req CodeExchangeRequest) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "authorization_code")
	data.Set("code", req.Code)
	data.Set("redirect_uri", req.RedirectURI)
	data.Set("client_id", req.ClientID)
	data.Set("code_verifier", req.CodeVerifier)
	return c.postTokenForm(ctx, data, nil, "token exchange")
}

// RefreshAccessToken redeems a refresh token at the token endpoint (RFC 6749 section 6). The CLI is a
// public client, so it identifies itself with client_id only. When the credentials are bound to a DPoP
// key (device-code login), a fresh proof for this request is sent as well.
func (c *browserAuthClient) RefreshAccessToken(ctx context.Context, req RefreshGrantRequest) (*TokenResponse, error) {
	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("refresh_token", req.RefreshToken)
	data.Set("client_id", req.ClientID)
	return c.postTokenForm(ctx, data, req.DPoPPrivateKey, "token refresh")
}

// postTokenForm posts a token-endpoint form and decodes the token response or the OAuth error.
func (c *browserAuthClient) postTokenForm(ctx context.Context, data url.Values, dpopKey ed25519.PrivateKey, what string) (*TokenResponse, error) {
	baseURL, err := c.getAuthBaseURL()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	tokenURL := baseURL + "/auth/token"

	httpReq, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if dpopKey != nil {
		proof, err := generateDPoPToken(dpopKey, "POST", tokenURL)
		if err != nil {
			return nil, fmt.Errorf("failed to generate DPoP token: %w", err)
		}
		httpReq.Header.Set("DPoP", proof)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var oauthErr OAuthError
		if jsonErr := json.Unmarshal(body, &oauthErr); jsonErr == nil && oauthErr.ErrorCode != "" {
			return nil, &oauthErr
		}
		return nil, fmt.Errorf("%s failed with status %d: %s", what, resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	return &tokenResp, nil
}
