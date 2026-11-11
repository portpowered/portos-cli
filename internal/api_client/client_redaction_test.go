package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// Known test secrets — these must NEVER appear in log output.
const (
	testAccessToken  = "eyJhbGciOiJFZERTQSIsInR5cCI6ImF0K2p3dCJ9.eyJzdWIiOiJwb3J0MS91c2Vycy90ZXN0dXNlciJ9.test-signature-value"
	testRefreshToken = "dGhpcy1pcy1hLXJlZnJlc2gtdG9rZW4tdGhhdC1pcy12ZXJ5LWxvbmc"
	testDPoPKey      = "MC4CAQAwBQYDK2VwBCIEIHpYk2x3bAEz0PJ0nDGoXm3MqJlqXbuZx3E_examplekey"
	testSetCookie    = "session=abc123def456ghi789jkl012mno345pqr678; Path=/; HttpOnly; Secure"
)

// stubAuthService implements auth.Service returning a fixed token.
type stubAuthService struct {
	token string
}

func (s *stubAuthService) SetToken(context.Context, string) error { return nil }
func (s *stubAuthService) Login(context.Context, string, auth.LoginMessageHandler) error {
	return nil
}
func (s *stubAuthService) BrowserLogin(context.Context, auth.LoginMessageHandler) error { return nil }
func (s *stubAuthService) GetAccessToken(context.Context) (string, error)               { return s.token, nil }
func (s *stubAuthService) RefreshToken(context.Context) error                           { return nil }
func (s *stubAuthService) IsAuthenticated(context.Context) (bool, error)                { return true, nil }
func (s *stubAuthService) GetPrincipalPathFromToken(context.Context) (string, string, string, error) {
	return "port1", "users", "test", nil
}

// newTestClient builds a ClientImpl wired to an observer logger and the given
// baseURL, returning the client and the log observer so callers can inspect
// captured log entries.
func newTestClient(t *testing.T, baseURL string) (*ClientImpl, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zapcore.DebugLevel)
	loggerRef := logging.NewLoggerRef()
	loggerRef.Set(zap.New(core))

	// We bypass ensureInited() via initOnce.Do below, so configProvider is unused.
	client := NewClient(nil, &stubAuthService{token: testAccessToken}, loggerRef, http.DefaultClient).(*ClientImpl)
	client.baseURL = baseURL
	// Mark initOnce as done so ensureInited() is a no-op.
	client.initOnce.Do(func() {})

	return client, logs
}

// serializeLogs flattens all observed log entries into a single string for
// substring searches.
func serializeLogs(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, entry := range logs.All() {
		b.WriteString(entry.Message)
		b.WriteString(" ")
		for _, field := range entry.Context {
			fmt.Fprintf(&b, "%s=%v ", field.Key, field)
		}
	}
	return b.String()
}

func TestDoRequest_RedactsRequestHeaders(t *testing.T) {
	// Server that returns a simple 200.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, logs := newTestClient(t, srv.URL)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}

	output := serializeLogs(logs)

	// The full access token must not appear.
	if strings.Contains(output, testAccessToken) {
		t.Error("full access token leaked in request log output")
	}

	// The Authorization header value set by doRequest is "Bearer <token>".
	// The redacted form should be present (first 15 chars + "...").
	bearerPrefix := ("Bearer " + testAccessToken)[:15] + "..."
	if !strings.Contains(output, bearerPrefix) {
		t.Errorf("expected redacted Authorization prefix %q in output, but not found", bearerPrefix)
	}
}

func TestDoRequest_RedactsResponseHeaders(t *testing.T) {
	// Server that returns sensitive response headers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", testSetCookie)
		w.Header().Set("X-Request-Id", "req-abc123")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, logs := newTestClient(t, srv.URL)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}

	output := serializeLogs(logs)

	// Full Set-Cookie value must not appear.
	if strings.Contains(output, testSetCookie) {
		t.Error("full Set-Cookie value leaked in response log output")
	}

	// The redacted form should be present.
	cookiePrefix := testSetCookie[:15] + "..."
	if !strings.Contains(output, cookiePrefix) {
		t.Errorf("expected redacted Set-Cookie prefix %q in output, but not found", cookiePrefix)
	}

	// Non-sensitive response headers should pass through unchanged.
	if !strings.Contains(output, "req-abc123") {
		t.Error("expected non-sensitive header X-Request-Id to pass through unchanged")
	}
}

func TestDoRequest_RequestBodyDoesNotLeakToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, logs := newTestClient(t, srv.URL)

	// Send a request with a JSON body — the body itself is logged in debug mode.
	body := strings.NewReader(`{"label":"test-device"}`)
	_, err := client.doRequest(context.Background(), "POST", "/endpoints", body, nil)
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}

	output := serializeLogs(logs)

	// Access token must not appear anywhere.
	if strings.Contains(output, testAccessToken) {
		t.Error("full access token leaked when request body is present")
	}
}

func TestDoRequest_MultipleSecrets_NoneLeaked(t *testing.T) {
	// Verify that no known secret values appear in any log entry.
	secrets := []string{testAccessToken, testRefreshToken, testDPoPKey, testSetCookie}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", testSetCookie)
		w.Header().Set("Authorization", "Bearer "+testRefreshToken) // unusual but tests redaction
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, logs := newTestClient(t, srv.URL)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}

	output := serializeLogs(logs)

	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Errorf("secret leaked in log output: %s...", secret[:20])
		}
	}
}
