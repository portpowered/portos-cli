package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/portpowered/portos-cli/internal/logging"
)

// mockTokenStorage implements TokenStorage for testing
type mockTokenStorage struct {
	creds   *Credentials
	loadErr error
	saveErr error
	saved   *Credentials
}

func (m *mockTokenStorage) SaveCredentials(creds *Credentials) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saved = creds
	return nil
}

func (m *mockTokenStorage) LoadCredentials() (*Credentials, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.creds, nil
}

func (m *mockTokenStorage) DeleteCredentials() error {
	m.creds = &Credentials{}
	return nil
}

// generateTestJWTWithExpiry creates a JWT with a specific expiry time for testing
func generateTestJWTWithExpiry(t *testing.T, expiry time.Time) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "test-user",
		"iat": time.Now().Unix(),
		"exp": expiry.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(testRSAKey(t))
	if err != nil {
		t.Fatalf("failed to generate test JWT: %v", err)
	}
	return tokenString
}

var (
	testRSAKeyOnce sync.Once
	testRSAKeyVal  *rsa.PrivateKey
)

// testRSAKey is a throwaway RSA key shared by the tests in this package; Port OS tokens are RS256.
func testRSAKey(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	testRSAKeyOnce.Do(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("failed to generate test RSA key: %v", err)
		}
		testRSAKeyVal = key
	})
	return testRSAKeyVal
}

func newTestService(storage TokenStorage) *ServiceImpl {
	return &ServiceImpl{
		tokenStorage: storage,
		logger:       logging.NewLoggerRef(),
	}
}

func TestGetAccessToken_NoCredentialsConfigured(t *testing.T) {
	storage := &mockTokenStorage{
		creds: &Credentials{}, // empty credentials — no token configured
	}
	svc := newTestService(storage)

	_, err := svc.GetAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error when no credentials configured, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "no access token") {
		t.Errorf("error should mention 'no access token', got: %s", msg)
	}
	if !strings.Contains(msg, "PORTOS_ACCESS_TOKEN") {
		t.Errorf("error should mention PORTOS_ACCESS_TOKEN env var, got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("error should mention 'port auth set-token', got: %s", msg)
	}
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("error should wrap ErrNoCredentials, errors.Is returned false")
	}
}

func TestGetAccessToken_TokenExpiredNoRefreshToken(t *testing.T) {
	expiredToken := generateTestJWTWithExpiry(t, time.Now().Add(-1*time.Hour))
	storage := &mockTokenStorage{
		creds: &Credentials{
			AccessToken:  expiredToken,
			RefreshToken: "", // no refresh token
		},
	}
	svc := newTestService(storage)

	_, err := svc.GetAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for expired token with no refresh token, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "access token expired") {
		t.Errorf("error should mention 'access token expired', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "PORTOS_ACCESS_TOKEN") {
		t.Errorf("error should mention PORTOS_ACCESS_TOKEN, got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("error should mention 'port auth set-token', got: %s", msg)
	}
	// Should NOT say "refresh failed" — there was no refresh attempt
	if strings.Contains(msg, "refresh failed") {
		t.Errorf("error should not mention refresh failure when no refresh token exists, got: %s", msg)
	}
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("error should wrap ErrTokenExpired, errors.Is returned false")
	}
}

func TestGetAccessToken_TokenExpiredRefreshFailed(t *testing.T) {
	expiredToken := generateTestJWTWithExpiry(t, time.Now().Add(-1*time.Hour))
	storage := &mockTokenStorage{
		creds: &Credentials{
			AccessToken:  expiredToken,
			RefreshToken: "some-refresh-token",
		},
	}
	// The token endpoint rejects the refresh token, so the session is over
	server := &fakeTokenEndpoint{status: 400, body: `{"error":"invalid_grant"}`}
	svc, _ := newRefreshService(t, server, storage.creds)

	_, err := svc.GetAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for expired token with failed refresh, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "access token expired") {
		t.Errorf("error should mention 'access token expired', got: %s", msg)
	}
	if !strings.Contains(msg, "refresh failed") {
		t.Errorf("error should mention 'refresh failed', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("error should mention 'port auth set-token', got: %s", msg)
	}
	if !errors.Is(err, ErrTokenExpired) {
		t.Errorf("error should wrap ErrTokenExpired, errors.Is returned false")
	}
}

func TestGetAccessToken_MalformedToken(t *testing.T) {
	storage := &mockTokenStorage{
		creds: &Credentials{
			AccessToken: "invalid-token",
		},
	}
	svc := newTestService(storage)

	_, err := svc.GetAccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}

	msg := err.Error()
	// Must NOT expose JWT internals
	if strings.Contains(msg, "failed to parse JWT") {
		t.Errorf("error should not expose JWT parse details, got: %s", msg)
	}
	if strings.Contains(msg, "number of segments") {
		t.Errorf("error should not expose JWT segment details, got: %s", msg)
	}
	// Must contain actionable guidance
	if !strings.Contains(msg, "PORTOS_ACCESS_TOKEN") {
		t.Errorf("error should mention PORTOS_ACCESS_TOKEN, got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("error should mention 'port auth set-token', got: %s", msg)
	}
	if !errors.Is(err, ErrTokenInvalid) {
		t.Errorf("error should wrap ErrTokenInvalid, errors.Is returned false")
	}
}

func TestGetAccessToken_ValidToken(t *testing.T) {
	validToken := generateTestJWTWithExpiry(t, time.Now().Add(1*time.Hour))
	storage := &mockTokenStorage{
		creds: &Credentials{
			AccessToken: validToken,
			TokenType:   "Bearer",
		},
	}
	svc := newTestService(storage)

	token, err := svc.GetAccessToken(context.Background())
	if err != nil {
		t.Fatalf("expected no error for valid token, got: %v", err)
	}
	if token != validToken {
		t.Error("returned token should match the stored token")
	}
}

// fakeBrowserClient records every call so a test can assert the browser login makes no other request.
type fakeBrowserClient struct {
	calls    []string
	response *TokenResponse
}

func (f *fakeBrowserClient) ExchangeCodeForToken(_ context.Context, _ CodeExchangeRequest) (*TokenResponse, error) {
	f.calls = append(f.calls, "ExchangeCodeForToken")
	return f.response, nil
}

func (f *fakeBrowserClient) RefreshAccessToken(_ context.Context, _ RefreshGrantRequest) (*TokenResponse, error) {
	f.calls = append(f.calls, "RefreshAccessToken")
	return f.response, nil
}

func TestBrowserLoginStoresRefreshToken(t *testing.T) {
	storage := &mockTokenStorage{}
	fake := &fakeBrowserClient{response: &TokenResponse{AccessToken: "a", RefreshToken: "r", TokenType: "Bearer", ExpiresIn: 3600}}
	svc := newTestService(storage)
	svc.browserClient = fake

	before := time.Now()
	if err := svc.exchangeAndStoreBrowserCredentials(context.Background(), "code", "http://127.0.0.1/cb", "verifier"); err != nil {
		t.Fatal(err)
	}

	if storage.saved == nil {
		t.Fatal("no credentials were stored")
	}
	if storage.saved.AccessToken != "a" || storage.saved.RefreshToken != "r" {
		t.Fatalf("stored access=%q refresh=%q, want a / r", storage.saved.AccessToken, storage.saved.RefreshToken)
	}
	if storage.saved.TokenType != "Bearer" {
		t.Fatalf("stored token type = %q, want Bearer", storage.saved.TokenType)
	}
	if storage.saved.ExpiresAt < before.Add(time.Hour).UnixMilli() {
		t.Fatalf("ExpiresAt = %d, want at least one hour from login", storage.saved.ExpiresAt)
	}
	if len(fake.calls) != 1 || fake.calls[0] != "ExchangeCodeForToken" {
		t.Fatalf("calls = %v, want only ExchangeCodeForToken", fake.calls)
	}
}
