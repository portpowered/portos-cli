package auth

import (
	"context"
	"crypto/ed25519"
)

// LoginMessageHandler is a function type for handling login messages
// Used to display messages during the login flow without using fmt.Printf directly
type LoginMessageHandler func(message string)

// Service defines the authentication service interface
// It supports both direct token setting and device authorization flow
type Service interface {
	// SetToken sets an access token directly without going through device auth
	SetToken(ctx context.Context, token string) error

	// Login initiates the device authorization flow (OAuth 2.0 Device Code Grant)
	// It handles the complete flow: registration, polling, and credential storage
	// messageHandler is an optional callback for displaying messages during the flow
	Login(ctx context.Context, clientID string, messageHandler LoginMessageHandler) error

	// BrowserLogin initiates the browser-based OAuth 2.0 Authorization Code + PKCE flow.
	// It starts a local callback server, opens the system browser, and waits for the
	// authorization code before exchanging it for an access token.
	BrowserLogin(ctx context.Context, messageHandler LoginMessageHandler) error

	// GetAccessToken retrieves the current access token
	// Returns empty string if not authenticated
	GetAccessToken(ctx context.Context) (string, error)

	// RefreshToken refreshes the access token using the stored refresh token
	RefreshToken(ctx context.Context) error

	// IsAuthenticated checks if valid credentials are stored
	IsAuthenticated(ctx context.Context) (bool, error)

	// GetPrincipalPathFromToken extracts namespace, principalType, principalId from the JWT subject.
	// Uses unverified JWT parsing (no JWKS validation). The subject is expected to be in
	// Port OS format: "namespace/principalType/principalId" (e.g. "port1/users/xyz").
	// Returns error if token is missing or subject cannot be parsed.
	GetPrincipalPathFromToken(ctx context.Context) (namespace, principalType, principalId string, err error)
}

// TokenStorage defines the interface for token persistence
type TokenStorage interface {
	// SaveCredentials saves authentication credentials
	SaveCredentials(creds *Credentials) error

	// LoadCredentials loads authentication credentials
	LoadCredentials() (*Credentials, error)

	// DeleteCredentials removes stored credentials
	DeleteCredentials() error
}

// Credentials represents stored authentication credentials
type Credentials struct {
	AccessToken    string `yaml:"access_token"`
	RefreshToken   string `yaml:"refresh_token"`
	TokenType      string `yaml:"token_type,omitempty"`
	ExpiresAt      int64  `yaml:"expires_at,omitempty"`       // milliseconds since epoch; 0 when unknown
	DPoPPrivateKey string `yaml:"dpop_private_key,omitempty"` // Base64-encoded Ed25519 private key for DPoP
}

// BrowserAuthClient defines the interface for browser-based OAuth token exchange
type BrowserAuthClient interface {
	// ExchangeCodeForToken exchanges an authorization code for an access token
	ExchangeCodeForToken(ctx context.Context, req CodeExchangeRequest) (*TokenResponse, error)
	// RefreshAccessToken redeems a refresh token (RFC 6749 section 6). A returned *OAuthError carries the server's error code.
	RefreshAccessToken(ctx context.Context, req RefreshGrantRequest) (*TokenResponse, error)
}

// RefreshGrantRequest contains the parameters for the refresh_token grant.
type RefreshGrantRequest struct {
	RefreshToken string
	ClientID     string
	// DPoPPrivateKey is set when the credentials are bound to a DPoP key; a proof is sent with the request.
	DPoPPrivateKey ed25519.PrivateKey
}

// CodeExchangeRequest contains the parameters for the authorization code exchange
type CodeExchangeRequest struct {
	Code         string
	RedirectURI  string
	CodeVerifier string
	ClientID     string
}

// DeviceAuthClient defines the interface for device authorization API calls
type DeviceAuthClient interface {
	// RequestDeviceAuthorization initiates device authorization by sending device public key
	RequestDeviceAuthorization(ctx context.Context, req DeviceAuthorizationRequest) (*DeviceAuthorizationResponse, error)

	// PollForToken polls the token endpoint with device code until authorization completes
	PollForToken(ctx context.Context, req TokenPollRequest) (*TokenResponse, error)
}

// DeviceAuthorizationRequest represents a request to start device authorization
type DeviceAuthorizationRequest struct {
	ClientID     string
	DevicePubkey string // Base64-encoded public key
	Scope        []string
	DPoPToken    string // DPoP JWT token for proof-of-possession
}

// DeviceAuthorizationResponse represents the response from device authorization request
type DeviceAuthorizationResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"` // seconds
	Interval                int    `json:"interval"`   // polling interval in seconds
}

// TokenPollRequest represents a request to poll for tokens using device code
type TokenPollRequest struct {
	DeviceCode     string
	ClientID       string
	DPoPPrivateKey []byte // Ed25519 private key for generating DPoP tokens (each request needs unique jti)
	TokenEndpoint  string // Full URL of the token endpoint for DPoP generation
}

// TokenResponse represents the response from token endpoint
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"` // seconds
	Scope        string `json:"scope,omitempty"`
}

// OAuthError represents an OAuth error response
type OAuthError struct {
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// Error implements the error interface
func (e *OAuthError) Error() string {
	if e.ErrorDescription != "" {
		return e.ErrorDescription
	}
	return e.ErrorCode
}
