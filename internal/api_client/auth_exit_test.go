package api_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
)

type memCredentials struct{ creds auth.Credentials }

func (m *memCredentials) SaveCredentials(c *auth.Credentials) error { m.creds = *c; return nil }
func (m *memCredentials) LoadCredentials() (*auth.Credentials, error) {
	cp := m.creds
	return &cp, nil
}
func (m *memCredentials) DeleteCredentials() error { m.creds = auth.Credentials{}; return nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jwtExpiring(at time.Time) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none","typ":"JWT"}`)) + "." + enc([]byte(fmt.Sprintf(`{"exp":%d}`, at.Unix()))) + ".sig"
}

// sendThroughRealClient sends a message through the real client and auth service, so the
// credential failure travels the same path as in the binary.
func sendThroughRealClient(t *testing.T, creds auth.Credentials, tokenEndpoint http.RoundTripper) error {
	t.Helper()
	for _, name := range []string{config.EnvAPIBaseURL, config.EnvAuthBaseURL, config.EnvBrowserAuthorizationBaseURL} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	provider := config.NewManagerProvider(&flags.GlobalFlags{ConfigDirPath: t.TempDir()})
	manager, err := provider.GetManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.SaveConfig(&config.Config{APIBaseURL: "https://api.example.test", AuthBaseURL: "https://auth.example.test"}); err != nil {
		t.Fatal(err)
	}
	provider = config.NewManagerProvider(&flags.GlobalFlags{ConfigDirPath: manager.ConfigDir()})
	logger := logging.NewLoggerRef()
	storage := &memCredentials{creds: creds}
	authService := auth.NewService(storage, nil, auth.NewBrowserAuthClient(&http.Client{Transport: tokenEndpoint}, provider), provider, logger)
	client := apiclient.NewClient(provider, authService, logger, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("the API must not be called without a usable token")
		return nil, nil
	})})
	_, err = client.Messages().Send(context.Background(), &api.CreateMessageRequest{}, "key")
	return err
}

func TestUnusableCredentialsExitAuth(t *testing.T) {
	noEndpoint := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("unexpected token request") })
	invalidGrant := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":"invalid_grant"}`)), Header: make(http.Header)}, nil
	})
	expired := jwtExpiring(time.Now().Add(-time.Hour))

	tests := []struct {
		name  string
		creds auth.Credentials
		end   http.RoundTripper
		want  error
	}{
		{"no credentials", auth.Credentials{}, noEndpoint, auth.ErrNoCredentials},
		{"invalid token", auth.Credentials{AccessToken: "garbage"}, noEndpoint, auth.ErrTokenInvalid},
		{"expired without refresh token", auth.Credentials{AccessToken: expired}, noEndpoint, auth.ErrTokenExpired},
		{"expired and refresh rejected", auth.Credentials{AccessToken: expired, RefreshToken: "r"}, invalidGrant, auth.ErrTokenExpired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := sendThroughRealClient(t, tt.creds, tt.end)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := exitcode.Code(err); got != exitcode.ExitAuth {
				t.Fatalf("exit code = %d, want %d (err: %v)", got, exitcode.ExitAuth, err)
			}
			if !strings.Contains(err.Error(), "port auth login") {
				t.Fatalf("user message lost its remediation hint: %v", err)
			}
		})
	}
}
