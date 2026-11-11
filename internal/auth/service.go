package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
)

// defaultCLIClientID is the well-known OAuth client ID for the Port OS CLI.
const defaultCLIClientID = "port1/systems/zero/oauth-clients/command-line-interface"

// AuthRemediationHint is the standard suffix appended to all auth error messages
// so that every failure mode tells the user how to fix authentication.
// AuthRemediationHint is the standard suffix appended to all auth error messages.
// Exported so other CLI packages can reference the canonical remediation text.
const AuthRemediationHint = `authenticate with "port auth login", "port auth set-token <token>", or PORTOS_ACCESS_TOKEN`

// ServiceImpl implements the authentication service
type ServiceImpl struct {
	tokenStorage   TokenStorage
	deviceClient   DeviceAuthClient
	browserClient  BrowserAuthClient
	configProvider *config.ManagerProvider
	logger         *logging.LoggerRef
}

// NewService creates a new authentication service
func NewService(
	tokenStorage TokenStorage,
	deviceClient DeviceAuthClient,
	browserClient BrowserAuthClient,
	configProvider *config.ManagerProvider,
	logger *logging.LoggerRef,
) Service {
	return &ServiceImpl{
		tokenStorage:   tokenStorage,
		deviceClient:   deviceClient,
		browserClient:  browserClient,
		configProvider: configProvider,
		logger:         logger,
	}
}

// getAuthBaseURL returns the auth base URL from config (with fallback to APIBaseURL handled at config level).
func (s *ServiceImpl) getAuthBaseURL() (string, error) {
	mgr, err := s.configProvider.GetManager()
	if err != nil {
		return "", err
	}
	cfg, err := mgr.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	return cfg.AuthBaseURL, nil
}

func (s *ServiceImpl) getBrowserAuthorizationBaseURL() (string, error) {
	mgr, err := s.configProvider.GetManager()
	if err != nil {
		return "", err
	}
	cfg, err := mgr.LoadConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load config: %w", err)
	}
	return cfg.BrowserAuthorizationBaseURL, nil
}

// SetToken sets an access token directly without going through device auth
func (s *ServiceImpl) SetToken(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	// Create credentials - expiry will be extracted from JWT when needed
	creds := &Credentials{
		AccessToken:  token,
		RefreshToken: "", // No refresh token for manually set tokens
		TokenType:    "Bearer",
	}

	return s.tokenStorage.SaveCredentials(creds)
}

// formatLoginInstructions formats the device authorization instructions as a string
func formatLoginInstructions(deviceAuthResp *DeviceAuthorizationResponse) string {
	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString("To complete authentication, please visit:\n")
	sb.WriteString(fmt.Sprintf("  %s\n", deviceAuthResp.VerificationURIComplete))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Or visit %s and enter code: %s\n", deviceAuthResp.VerificationURI, deviceAuthResp.UserCode))
	sb.WriteString("\n")
	sb.WriteString("Waiting for authorization...\n")
	return sb.String()
}

// formatLoginSuccess formats the login success message as a string
func formatLoginSuccess() string {
	return "Authentication successful!\n"
}

// requestDeviceAuthorization signs a DPoP token and requests device authorization from the auth server.
func (s *ServiceImpl) requestDeviceAuthorization(ctx context.Context, clientID, authBaseURL string, privKey ed25519.PrivateKey, pubKeyBase64 string) (*DeviceAuthorizationResponse, error) {
	deviceAuthEndpointURL := strings.TrimSuffix(authBaseURL, "/") + deviceAuthorizationPath
	dpopToken, err := generateDPoPToken(privKey, "POST", deviceAuthEndpointURL)
	if err != nil {
		return nil, fmt.Errorf("failed to generate DPoP token for device authorization: %w", err)
	}

	deviceAuthReq := DeviceAuthorizationRequest{
		ClientID:     clientID,
		DevicePubkey: pubKeyBase64,
		Scope:        []string{}, // Default scope - can be made configurable
		DPoPToken:    dpopToken,
	}

	deviceAuthResp, err := s.deviceClient.RequestDeviceAuthorization(ctx, deviceAuthReq)
	if err != nil {
		return nil, fmt.Errorf("failed to request device authorization: %w", err)
	}
	return deviceAuthResp, nil
}

// pollAndStoreDeviceCredentials polls for the device token (DPoP tokens are generated fresh per attempt) and
// saves the resulting credentials. The token expiry is extracted from the JWT when needed.
func (s *ServiceImpl) pollAndStoreDeviceCredentials(ctx context.Context, clientID, authBaseURL string, privKey ed25519.PrivateKey, deviceCode string) (*TokenResponse, error) {
	pollReq := TokenPollRequest{
		DeviceCode:     deviceCode,
		ClientID:       clientID,
		DPoPPrivateKey: []byte(privKey), // Convert to []byte for transmission
		TokenEndpoint:  strings.TrimSuffix(authBaseURL, "/") + tokenPath,
	}

	tokenResp, err := s.deviceClient.PollForToken(ctx, pollReq)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain token: %w", err)
	}

	creds := &Credentials{
		AccessToken:    tokenResp.AccessToken,
		RefreshToken:   tokenResp.RefreshToken,
		TokenType:      tokenResp.TokenType,
		DPoPPrivateKey: base64.StdEncoding.EncodeToString(privKey),
		ExpiresAt:      expiresAtFromNow(tokenResp.ExpiresIn),
	}
	if err := s.tokenStorage.SaveCredentials(creds); err != nil {
		return nil, fmt.Errorf("failed to save credentials: %w", err)
	}
	return tokenResp, nil
}

// Login initiates the device authorization flow (OAuth 2.0 Device Code Grant)
// It handles the complete flow: registration, polling, and credential storage
// messageHandler is an optional callback for displaying messages during the flow
func (s *ServiceImpl) Login(ctx context.Context, clientID string, messageHandler LoginMessageHandler) error {
	if clientID == "" {
		clientID = defaultCLIClientID
	}

	s.logger.Logger().Info("Starting device authorization flow", zap.String("clientID", clientID))

	// Step 1: Generate device keypair (Ed25519)
	pubKey, privKey, err := generateKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate device keypair: %w", err)
	}

	// Step 2: Encode public key as base64
	pubKeyBase64 := encodePublicKeyBase64(pubKey)

	// Steps 3-4: Request device authorization
	authBaseURL, err := s.getAuthBaseURL()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	deviceAuthResp, err := s.requestDeviceAuthorization(ctx, clientID, authBaseURL, privKey, pubKeyBase64)
	if err != nil {
		return err
	}

	// Step 5: Display user code and verification URI to the user via message handler
	s.logger.Logger().Info("Device authorization requested",
		zap.String("userCode", deviceAuthResp.UserCode),
		zap.String("verificationURI", deviceAuthResp.VerificationURIComplete))

	if messageHandler != nil {
		instructions := formatLoginInstructions(deviceAuthResp)
		messageHandler(instructions)
	}

	// Steps 6-7: Poll for the token and store credentials
	tokenResp, err := s.pollAndStoreDeviceCredentials(ctx, clientID, authBaseURL, privKey, deviceAuthResp.DeviceCode)
	if err != nil {
		return err
	}

	s.logger.Logger().Info("Authentication successful", zap.String("tokenType", tokenResp.TokenType))

	if messageHandler != nil {
		successMsg := formatLoginSuccess()
		messageHandler(successMsg)
	}

	return nil
}

// BrowserLogin initiates the browser-based OAuth 2.0 Authorization Code + PKCE flow.
func (s *ServiceImpl) BrowserLogin(ctx context.Context, messageHandler LoginMessageHandler) error {
	// Step 1: Generate PKCE parameters
	pkce, err := GeneratePKCE()
	if err != nil {
		return fmt.Errorf("failed to generate PKCE parameters: %w", err)
	}

	// Step 2: Start local callback server
	callbackServer, err := StartCallbackServer()
	if err != nil {
		return fmt.Errorf("failed to start callback server: %w", err)
	}
	defer callbackServer.Shutdown(context.Background()) //nolint:errcheck

	// Step 3: Resolve browser navigation independently from token endpoints.
	authBaseURL, err := s.getBrowserAuthorizationBaseURL()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Step 4: Build authorization URL
	authURL := BuildAuthURL(
		strings.TrimSuffix(authBaseURL, "/"),
		defaultCLIClientID,
		callbackServer.RedirectURI(),
		pkce.CodeChallenge,
	)

	// Step 5: Print instructions via message handler
	if messageHandler != nil {
		messageHandler(fmt.Sprintf("\nOpening browser for authentication...\nIf browser does not open, visit:\n  %s\n\nWaiting for authorization...\n", authURL))
	}

	// Step 6: Open browser (best effort — don't fail if browser can't open)
	if err := OpenBrowser(authURL); err != nil {
		s.logger.Logger().Warn("Failed to open browser", zap.Error(err))
	}

	// Step 7: Wait for callback code (with 5 minute timeout)
	codeCtx, codeCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer codeCancel()

	code, err := callbackServer.WaitForCode(codeCtx)
	if err != nil {
		return fmt.Errorf("failed to receive authorization code: %w", err)
	}

	if err := s.exchangeAndStoreBrowserCredentials(ctx, code, callbackServer.RedirectURI(), pkce.CodeVerifier); err != nil {
		return err
	}

	s.logger.Logger().Info("Browser authentication successful")
	if messageHandler != nil {
		messageHandler(formatLoginSuccess())
	}

	return nil
}

// exchangeAndStoreBrowserCredentials exchanges the authorization code for tokens and stores the
// access and refresh tokens as returned, the same way the device-code login stores them.
func (s *ServiceImpl) exchangeAndStoreBrowserCredentials(ctx context.Context, code, redirectURI, codeVerifier string) error {
	tokenResp, err := s.browserClient.ExchangeCodeForToken(ctx, CodeExchangeRequest{
		Code:         code,
		RedirectURI:  redirectURI,
		CodeVerifier: codeVerifier,
		ClientID:     defaultCLIClientID,
	})
	if err != nil {
		return fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	creds := &Credentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresAt:    expiresAtFromNow(tokenResp.ExpiresIn),
	}
	if err := s.tokenStorage.SaveCredentials(creds); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}
	return nil
}

// expiresAtFromNow converts a token response's expires_in (seconds) to Credentials.ExpiresAt (ms since
// epoch); 0 when the server gave no lifetime.
func expiresAtFromNow(expiresIn int) int64 {
	if expiresIn <= 0 {
		return 0
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second).UnixMilli()
}

// extractSubjectFromJWT extracts the subject claim from a JWT token without validating it (unverified parse).
func extractSubjectFromJWT(tokenString string) (string, error) {
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return "", fmt.Errorf("failed to parse JWT: %w", err)
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("invalid JWT claims format")
	}
	subValue, exists := claims["sub"]
	if !exists {
		return "", fmt.Errorf("JWT missing sub claim")
	}
	sub, ok := subValue.(string)
	if !ok {
		return "", fmt.Errorf("invalid sub claim type: %T", subValue)
	}
	return sub, nil
}

// extractExpiryFromJWT extracts the expiry time from a JWT token without validating it
// Returns the expiry time and any error encountered
func extractExpiryFromJWT(tokenString string) (time.Time, error) {
	// Parse the token without validation (we only need to extract claims)
	parser := jwt.NewParser()
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse JWT: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return time.Time{}, fmt.Errorf("invalid JWT claims format")
	}

	// Extract exp claim (expiry time as Unix timestamp)
	expValue, exists := claims[JWT_CLAIM_EXPIRES_AT]
	if !exists {
		return time.Time{}, fmt.Errorf("JWT missing exp claim")
	}

	// Handle different numeric types (float64 from JSON, int64, etc.)
	var expUnix int64
	switch v := expValue.(type) {
	case float64:
		expUnix = int64(v)
	case int64:
		expUnix = v
	case int:
		expUnix = int64(v)
	default:
		return time.Time{}, fmt.Errorf("invalid exp claim type: %T", expValue)
	}

	return time.Unix(expUnix, 0), nil
}

const (
	refreshableExpiryBuffer  = 5 * time.Minute
	nonRefreshableExpirySkew = 10 * time.Second
)

// expiryBuffer is how long before exp a token counts as expired. A refreshable token is renewed early;
// a non-refreshable operational token (300 s lifetime) is usable until just before its exp.
func expiryBuffer(refreshable bool) time.Duration {
	if refreshable {
		return refreshableExpiryBuffer
	}
	return nonRefreshableExpirySkew
}

// GetAccessToken retrieves the current access token
func (s *ServiceImpl) GetAccessToken(ctx context.Context) (string, error) {
	creds, err := s.tokenStorage.LoadCredentials()
	// Log only whether a token is present — never log the raw token value to avoid credential leaks
	s.logger.Logger().Debug("Loading credentials", zap.Bool("has_token", creds != nil && creds.AccessToken != ""))
	if err != nil {
		s.logger.Logger().Warn("Failed to load credentials", zap.Error(err))
		return "", fmt.Errorf("failed to load credentials: %w", err)
	}

	if creds.AccessToken == "" {
		s.logger.Logger().Warn("No access token configured")
		return "", newAuthError(fmt.Sprintf("no access token — %s", AuthRemediationHint), ErrNoCredentials)
	}

	// Extract expiry from JWT token
	expiresAt, err := extractExpiryFromJWT(creds.AccessToken)
	if err != nil {
		s.logger.Logger().Debug("Failed to extract expiry from JWT, token may be invalid", zap.Error(err))
		return "", newAuthError(fmt.Sprintf("invalid access token — %s", AuthRemediationHint), ErrTokenInvalid)
	}

	// Check if token is expired (with 5 minute buffer)
	if expiresAt.Before(time.Now().Add(expiryBuffer(creds.RefreshToken != ""))) {
		// Token is expired or about to expire - try to refresh if refresh token is available
		if creds.RefreshToken != "" {
			if err := s.RefreshToken(ctx); err != nil {
				s.logger.Logger().Warn("Failed to refresh token", zap.Error(err))
				// Only a rejected refresh token (invalid_grant) means the session is over. Anything else
				// (network, 5xx, local failure) is surfaced as its own error so callers can retry.
				if errors.Is(err, ErrTokenExpired) {
					return "", newAuthError(fmt.Sprintf("access token expired and refresh failed — %s", AuthRemediationHint), ErrTokenExpired)
				}
				return "", fmt.Errorf("access token expired and could not be refreshed: %w", err)
			}
			// Reload credentials after refresh
			creds, err = s.tokenStorage.LoadCredentials()
			if err != nil {
				return "", fmt.Errorf("failed to reload credentials after refresh: %w", err)
			}
		} else {
			return "", newAuthError(fmt.Sprintf("access token expired — %s", AuthRemediationHint), ErrTokenExpired)
		}
	}

	return creds.AccessToken, nil
}

// GetPrincipalPathFromToken extracts namespace, principalType, principalId from the JWT subject.
// Uses unverified JWT parsing (no JWKS validation). The subject is expected to be in
// Port OS format: "namespace/principalType/principalId" (e.g. "port1/users/xyz").
func (s *ServiceImpl) GetPrincipalPathFromToken(ctx context.Context) (namespace, principalType, principalId string, err error) {
	token, err := s.GetAccessToken(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("no access token available: %w", err)
	}
	if token == "" {
		return "", "", "", fmt.Errorf("no access token available")
	}
	sub, err := extractSubjectFromJWT(token)
	if err != nil {
		s.logger.Logger().Debug("Failed to extract subject from JWT", zap.Error(err))
		return "", "", "", fmt.Errorf("invalid access token — %s", AuthRemediationHint)
	}
	parts := strings.Split(sub, "/")
	if len(parts) == 3 {
		return parts[0], parts[1], parts[2], nil
	}
	if len(parts) == 1 && parts[0] != "" {
		return "port1", "users", parts[0], nil
	}
	return "", "", "", fmt.Errorf("JWT subject has unexpected format: %q (expected namespace/principalType/principalId)", sub)
}

// RefreshToken refreshes the access token using the stored refresh token
func (s *ServiceImpl) RefreshToken(ctx context.Context) error {
	creds, err := s.tokenStorage.LoadCredentials()
	if err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	if creds.RefreshToken == "" {
		return fmt.Errorf("no refresh token available")
	}

	if s.browserClient == nil {
		return newAuthError(fmt.Sprintf("token refresh is not configured — %s", AuthRemediationHint), ErrTokenInvalid)
	}

	req := RefreshGrantRequest{RefreshToken: creds.RefreshToken, ClientID: defaultCLIClientID}
	if creds.DPoPPrivateKey != "" {
		keyBytes, err := base64.StdEncoding.DecodeString(creds.DPoPPrivateKey)
		if err != nil || len(keyBytes) != ed25519.PrivateKeySize {
			return newAuthError(fmt.Sprintf("stored DPoP key is invalid — %s", AuthRemediationHint), ErrTokenInvalid)
		}
		req.DPoPPrivateKey = ed25519.PrivateKey(keyBytes)
	}

	tokenResp, err := s.browserClient.RefreshAccessToken(ctx, req)
	if err != nil {
		var oauthErr *OAuthError
		if errors.As(err, &oauthErr) && oauthErr.ErrorCode == "invalid_grant" {
			return newAuthError(fmt.Sprintf("refresh token is no longer valid — %s", AuthRemediationHint), ErrTokenExpired)
		}
		return fmt.Errorf("failed to refresh token: %w", err)
	}

	updated := *creds
	updated.AccessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		updated.RefreshToken = tokenResp.RefreshToken
	}
	if tokenResp.TokenType != "" {
		updated.TokenType = tokenResp.TokenType
	}
	updated.ExpiresAt = expiresAtFromNow(tokenResp.ExpiresIn)
	if err := s.tokenStorage.SaveCredentials(&updated); err != nil {
		return fmt.Errorf("failed to save credentials: %w", err)
	}
	return nil
}

// IsAuthenticated checks if valid credentials are stored
func (s *ServiceImpl) IsAuthenticated(ctx context.Context) (bool, error) {
	creds, err := s.tokenStorage.LoadCredentials()
	if err != nil {
		return false, err
	}

	if creds.AccessToken == "" {
		return false, nil
	}

	// Extract expiry from JWT token
	expiresAt, err := extractExpiryFromJWT(creds.AccessToken)
	if err != nil {
		// If we can't parse the JWT, consider it invalid
		return false, nil
	}

	// Check if token is expired (with the buffer for this credential kind)
	if expiresAt.Before(time.Now().Add(expiryBuffer(creds.RefreshToken != ""))) {
		return false, nil
	}

	return true, nil
}
