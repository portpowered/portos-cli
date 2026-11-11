package auth

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/flags"
)

func TestStartCallbackServer_UsesFixedPortAndRedirectURI(t *testing.T) {
	cs, err := StartCallbackServer()
	if err != nil {
		t.Fatalf("StartCallbackServer() error = %v", err)
	}
	t.Cleanup(func() { cs.Shutdown(context.Background()) })

	if cs.Port != CLIOAuthCallbackPort {
		t.Errorf("expected port %d, got %d", CLIOAuthCallbackPort, cs.Port)
	}

	expected := fmt.Sprintf("http://localhost:%d/callback", CLIOAuthCallbackPort)
	if got := cs.RedirectURI(); got != expected {
		t.Errorf("RedirectURI() = %q, want %q", got, expected)
	}

	// Also test that a second StartCallbackServer returns a user-friendly error
	// while the first one holds the port.
	_, csErr := StartCallbackServer()
	if csErr == nil {
		t.Fatal("expected error when port is in use, got nil")
	}
	if !strings.Contains(csErr.Error(), "already in use") {
		t.Errorf("expected user-friendly error about port in use, got: %v", csErr)
	}
}

func TestStartCallbackServer_PortInUse_ExternalBlocker(t *testing.T) {
	// Occupy the fixed port with a raw listener, then verify StartCallbackServer
	// produces a user-friendly error message.
	addr := fmt.Sprintf("127.0.0.1:%d", CLIOAuthCallbackPort)
	blocker, err := net.Listen("tcp", addr)
	if err != nil {
		t.Skipf("could not occupy port %d (may already be in use): %v", CLIOAuthCallbackPort, err)
	}
	t.Cleanup(func() { blocker.Close() })

	_, csErr := StartCallbackServer()
	if csErr == nil {
		t.Fatal("expected error when port is in use, got nil")
	}
	if !strings.Contains(csErr.Error(), "already in use") {
		t.Errorf("expected user-friendly error about port in use, got: %v", csErr)
	}
}

func TestGeneratePKCE_ProducesValidParams(t *testing.T) {
	params, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error = %v", err)
	}
	if params.CodeVerifier == "" {
		t.Error("CodeVerifier should not be empty")
	}
	if params.CodeChallenge == "" {
		t.Error("CodeChallenge should not be empty")
	}
	if params.CodeVerifier == params.CodeChallenge {
		t.Error("CodeVerifier and CodeChallenge should be different")
	}
}

func TestCallbackServer_WaitForCode_ReceivesCode(t *testing.T) {
	cs := &CallbackServer{
		codeCh: make(chan string, 1),
		errCh:  make(chan error, 1),
	}

	cs.codeCh <- "test-auth-code"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	code, err := cs.WaitForCode(ctx)
	if err != nil {
		t.Fatalf("WaitForCode() error = %v", err)
	}
	if code != "test-auth-code" {
		t.Errorf("WaitForCode() = %q, want %q", code, "test-auth-code")
	}
}

func TestBuildAuthURL_ContainsRedirectURI(t *testing.T) {
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", CLIOAuthCallbackPort)
	authURL := BuildAuthURL("https://example.com", "my-client", redirectURI, "challenge123")

	if !strings.Contains(authURL, "redirect_uri=http%3A%2F%2Flocalhost%3A11778%2Fcallback") {
		t.Errorf("auth URL does not contain properly encoded redirect URI: %s", authURL)
	}
}

type browserOriginProbeTransport func(*http.Request) (*http.Response, error)

var _ http.RoundTripper = browserOriginProbeTransport(nil)

func (transport browserOriginProbeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestBrowserNavigationOverrideKeepsTokenExchangeOnAuthOrigin(t *testing.T) {
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
	if err := manager.SaveConfig(&config.Config{APIBaseURL: "https://api.portoperatingsystem.com", AuthBaseURL: "https://auth.portoperatingsystem.com", BrowserAuthorizationBaseURL: "https://portoperatingsystem.com"}); err != nil {
		t.Fatal(err)
	}
	provider = config.NewManagerProvider(&flags.GlobalFlags{ConfigDirPath: manager.ConfigDir()})
	service := &ServiceImpl{configProvider: provider}
	baseURL, err := service.getBrowserAuthorizationBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	pkce, err := GeneratePKCE()
	if err != nil {
		t.Fatal(err)
	}
	callback := "http://localhost:11778/callback"
	openingURL, err := url.Parse(BuildAuthURL(baseURL, defaultCLIClientID, callback, pkce.CodeChallenge))
	if err != nil {
		t.Fatal(err)
	}
	if openingURL.Host != "portoperatingsystem.com" || openingURL.Path != "/oauth/authorize" || openingURL.Query().Get("portos_request") != "" || openingURL.Query().Get("resource") != "" {
		t.Fatal("browser must enter the existing website first-party flow")
	}
	if openingURL.Query().Get("client_id") != defaultCLIClientID || openingURL.Query().Get("redirect_uri") != callback || openingURL.Query().Get("code_challenge") != pkce.CodeChallenge || openingURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("browser entry point must preserve exact callback and PKCE binding")
	}
	var paths []string
	client := NewBrowserAuthClient(&http.Client{Transport: browserOriginProbeTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "auth.portoperatingsystem.com" || request.Method != http.MethodPost {
			t.Error("credential exchange must remain on the configured auth origin")
		}
		paths = append(paths, request.URL.Path)
		responseBody := ""
		if request.URL.Path == "/auth/token" {
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("client_id") != defaultCLIClientID || request.Form.Get("redirect_uri") != callback || request.Form.Get("code_verifier") != pkce.CodeVerifier {
				t.Error("code exchange binding changed")
			}
			responseBody = `{"access_token":"test-session-token","token_type":"Bearer"}`
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(responseBody)), Header: make(http.Header)}, nil
	})}, provider)
	response, err := client.ExchangeCodeForToken(context.Background(), CodeExchangeRequest{Code: "test-code", ClientID: defaultCLIClientID, RedirectURI: callback, CodeVerifier: pkce.CodeVerifier})
	if err != nil {
		t.Fatal(err)
	}
	if response.AccessToken != "test-session-token" {
		t.Fatal("code exchange must return the access token")
	}
	if len(paths) != 1 || paths[0] != "/auth/token" {
		t.Fatal("the browser login must only call the token endpoint")
	}
}
