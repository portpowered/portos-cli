package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
)

const refreshTestAuthBase = "https://auth.example.test"

// memStorage keeps what was saved so a refresh followed by a reload sees the new credentials.
type memStorage struct{ creds *Credentials }

func (m *memStorage) SaveCredentials(c *Credentials) error { cp := *c; m.creds = &cp; return nil }
func (m *memStorage) LoadCredentials() (*Credentials, error) {
	cp := *m.creds
	return &cp, nil
}
func (m *memStorage) DeleteCredentials() error { m.creds = &Credentials{}; return nil }

// fakeTokenEndpoint is a fake token endpoint: it records each request and answers with status/body.
type fakeTokenEndpoint struct {
	status   int
	body     string
	requests []*http.Request
	forms    []url.Values
}

func (s *fakeTokenEndpoint) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	s.requests = append(s.requests, r)
	s.forms = append(s.forms, r.PostForm)
	return &http.Response{StatusCode: s.status, Body: io.NopCloser(strings.NewReader(s.body)), Header: make(http.Header)}, nil
}

// newRefreshService wires the real browser auth client to the fake token endpoint.
func newRefreshService(t *testing.T, server *fakeTokenEndpoint, creds *Credentials) (*ServiceImpl, *memStorage) {
	t.Helper()
	return newRefreshServiceAt(t, &http.Client{Transport: server}, refreshTestAuthBase, creds)
}

// newRefreshServiceAt wires the real browser auth client to any HTTP client and auth origin.
func newRefreshServiceAt(t *testing.T, httpClient *http.Client, authBase string, creds *Credentials) (*ServiceImpl, *memStorage) {
	t.Helper()
	for _, name := range []string{config.EnvAPIBaseURL, config.EnvAuthBaseURL, config.EnvBrowserAuthorizationBaseURL} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	provider := config.NewManagerProvider(&flags.GlobalFlags{ConfigDirPath: t.TempDir()})
	manager, err := provider.GetManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveConfig(&config.Config{APIBaseURL: "https://api.example.test", AuthBaseURL: authBase}); err != nil {
		t.Fatal(err)
	}
	provider = config.NewManagerProvider(&flags.GlobalFlags{ConfigDirPath: manager.ConfigDir()})
	storage := &memStorage{creds: creds}
	return &ServiceImpl{
		tokenStorage:   storage,
		browserClient:  NewBrowserAuthClient(httpClient, provider),
		configProvider: provider,
		logger:         logging.NewLoggerRef(),
	}, storage
}

func TestRefreshTokenRotates(t *testing.T) {
	server := &fakeTokenEndpoint{status: 200, body: `{"access_token":"new-a","refresh_token":"new-r","token_type":"Bearer","expires_in":3600}`}
	svc, storage := newRefreshService(t, server, &Credentials{AccessToken: "old-a", RefreshToken: "old-r", TokenType: "Bearer"})

	before := time.Now()
	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(server.requests) != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", len(server.requests))
	}
	req, form := server.requests[0], server.forms[0]
	if req.Method != http.MethodPost || req.URL.String() != refreshTestAuthBase+"/auth/token" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	if got := req.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
		t.Fatalf("Content-Type = %q", got)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "old-r" || form.Get("client_id") != defaultCLIClientID {
		t.Fatalf("form = %v", form)
	}
	if form.Get("client_secret") != "" || req.Header.Get("DPoP") != "" {
		t.Fatal("an unbound public-client refresh must send neither a secret nor a DPoP proof")
	}
	if storage.creds.AccessToken != "new-a" || storage.creds.RefreshToken != "new-r" {
		t.Fatalf("stored access=%q refresh=%q, want new-a / new-r", storage.creds.AccessToken, storage.creds.RefreshToken)
	}
	if storage.creds.ExpiresAt < before.Add(time.Hour).UnixMilli() {
		t.Fatalf("ExpiresAt = %d, want about an hour from now", storage.creds.ExpiresAt)
	}
}

func TestRefreshTokenKeepsOldRefreshWhenNoneReturned(t *testing.T) {
	server := &fakeTokenEndpoint{status: 200, body: `{"access_token":"new-a","token_type":"Bearer","expires_in":60}`}
	svc, storage := newRefreshService(t, server, &Credentials{AccessToken: "old-a", RefreshToken: "old-r", TokenType: "Bearer"})

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if storage.creds.AccessToken != "new-a" || storage.creds.RefreshToken != "old-r" {
		t.Fatalf("stored access=%q refresh=%q, want new-a / old-r", storage.creds.AccessToken, storage.creds.RefreshToken)
	}
}

func TestExpiredAccessTokenRefreshesAutomatically(t *testing.T) {
	fresh := generateTestJWTWithExpiry(t, time.Now().Add(time.Hour))
	server := &fakeTokenEndpoint{status: 200, body: `{"access_token":"` + fresh + `","refresh_token":"new-r","token_type":"Bearer","expires_in":3600}`}
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))
	svc, storage := newRefreshService(t, server, &Credentials{AccessToken: expired, RefreshToken: "old-r", TokenType: "Bearer"})

	got, err := svc.GetAccessToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != fresh {
		t.Fatal("GetAccessToken must return the refreshed access token")
	}
	if len(server.requests) != 1 || storage.creds.RefreshToken != "new-r" {
		t.Fatalf("calls=%d stored refresh=%q", len(server.requests), storage.creds.RefreshToken)
	}

	// A second call finds the fresh token and does not refresh again.
	if _, err := svc.GetAccessToken(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(server.requests) != 1 {
		t.Fatalf("calls after second GetAccessToken = %d, want 1", len(server.requests))
	}
}

func TestRefreshInvalidGrantReturnsExpiredError(t *testing.T) {
	server := &fakeTokenEndpoint{status: 400, body: `{"error":"invalid_grant","error_description":"refresh token revoked"}`}
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))
	svc, storage := newRefreshService(t, server, &Credentials{AccessToken: expired, RefreshToken: "old-r"})

	err := svc.RefreshToken(context.Background())
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("RefreshToken error = %v, want ErrTokenExpired", err)
	}
	if !strings.Contains(err.Error(), "port auth login") {
		t.Fatalf("error should tell the user to log in again: %v", err)
	}

	_, err = svc.GetAccessToken(context.Background())
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("GetAccessToken error = %v, want ErrTokenExpired", err)
	}
	if storage.creds.RefreshToken != "old-r" || storage.creds.AccessToken != expired {
		t.Fatal("a failed refresh must leave the stored credentials untouched")
	}
}

func TestRefreshNonOAuthFailureIsNotReportedAsInvalidGrant(t *testing.T) {
	server := &fakeTokenEndpoint{status: 503, body: `upstream down`}
	svc, _ := newRefreshService(t, server, &Credentials{AccessToken: "a", RefreshToken: "r"})

	err := svc.RefreshToken(context.Background())
	if err == nil || errors.Is(err, ErrTokenExpired) {
		t.Fatalf("a transient failure must not be reported as an expired session, got %v", err)
	}
}

func TestRefreshSendsDPoPProofWhenCredentialsAreBound(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server := &fakeTokenEndpoint{status: 200, body: `{"access_token":"new-a","refresh_token":"new-r","token_type":"DPoP","expires_in":3600}`}
	svc, storage := newRefreshService(t, server, &Credentials{
		AccessToken: "old-a", RefreshToken: "old-r", TokenType: "DPoP",
		DPoPPrivateKey: base64.StdEncoding.EncodeToString(priv),
	})

	if err := svc.RefreshToken(context.Background()); err != nil {
		t.Fatal(err)
	}

	proof := server.requests[0].Header.Get("DPoP")
	if proof == "" {
		t.Fatal("a DPoP-bound refresh must send a DPoP proof")
	}
	claims := jwt.MapClaims{}
	token, err := jwt.NewParser(jwt.WithValidMethods([]string{"EdDSA"})).ParseWithClaims(proof, claims, func(*jwt.Token) (interface{}, error) { return pub, nil })
	if err != nil {
		t.Fatalf("DPoP proof does not verify against the stored key: %v", err)
	}
	// RFC 9449 section 4.2: the server reads the proof's key from the jwk header.
	if jwk, _ := token.Header["jwk"].(map[string]interface{}); token.Header["typ"] != "dpop+jwt" || jwk["x"] != base64.RawURLEncoding.EncodeToString(pub) {
		t.Fatalf("DPoP header = %v, want typ dpop+jwt and the public key as jwk", token.Header)
	}
	if claims["htm"] != "POST" || claims["htu"] != refreshTestAuthBase+"/auth/token" {
		t.Fatalf("DPoP claims htm=%v htu=%v", claims["htm"], claims["htu"])
	}
	if claims["jkt"] == nil || claims["jti"] == nil {
		t.Fatal("DPoP proof needs jkt and jti")
	}
	if storage.creds.DPoPPrivateKey == "" {
		t.Fatal("the DPoP key must be kept across a refresh")
	}
}
