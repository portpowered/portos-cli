package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
)

// CLIOAuthCallbackPort is the fixed port used by the CLI OAuth callback server.
// This port must match the redirect URI registered on the backend for the CLI OAuth client.
const CLIOAuthCallbackPort = 11778

// PKCEParams holds the PKCE code verifier and challenge for the OAuth authorization flow.
type PKCEParams struct {
	CodeVerifier  string
	CodeChallenge string
}

// GeneratePKCE generates a PKCE code verifier and S256 code challenge.
// The verifier is 32 random bytes encoded as base64url.
func GeneratePKCE() (PKCEParams, error) {
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return PKCEParams{}, fmt.Errorf("failed to generate PKCE verifier: %w", err)
	}

	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// S256: BASE64URL(SHA256(ASCII(code_verifier)))
	h := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(h[:])

	return PKCEParams{
		CodeVerifier:  codeVerifier,
		CodeChallenge: codeChallenge,
	}, nil
}

// CallbackServer is a local HTTP server that listens for the OAuth callback.
type CallbackServer struct {
	Port   int
	server *http.Server
	codeCh chan string
	errCh  chan error
}

// StartCallbackServer starts a local HTTP server on the fixed CLI OAuth callback port
// to receive the OAuth authorization code callback.
func StartCallbackServer() (*CallbackServer, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", CLIOAuthCallbackPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		var opErr *net.OpError
		if errors.As(err, &opErr) {
			return nil, fmt.Errorf("port %d is already in use — please close any other login sessions and try again", CLIOAuthCallbackPort)
		}
		return nil, fmt.Errorf("failed to start callback server: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port

	cs := &CallbackServer{
		Port:   port,
		codeCh: make(chan string, 1),
		errCh:  make(chan error, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handleCallback)

	cs.server = &http.Server{Handler: mux}

	go func() {
		// Serve returns when Shutdown is called — ignore the expected ErrServerClosed
		if serveErr := cs.server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case cs.errCh <- serveErr:
			default:
			}
		}
	}()

	return cs, nil
}

func (cs *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	if errParam := q.Get("error"); errParam != "" {
		errDesc := q.Get("error_description")
		if errDesc == "" {
			errDesc = errParam
		}
		http.Error(w, fmt.Sprintf(`<html><body><h2>Authentication Error</h2><p>%s</p><p>You can close this window.</p></body></html>`, errDesc), http.StatusBadRequest)
		w.Header().Set("Content-Type", "text/html")
		select {
		case cs.errCh <- fmt.Errorf("authorization error: %s", errDesc):
		default:
		}
		return
	}

	code := q.Get("code")
	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		select {
		case cs.errCh <- fmt.Errorf("missing authorization code in callback"):
		default:
		}
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `<html><body><h2>Authentication successful!</h2><p>You can close this window.</p></body></html>`)

	select {
	case cs.codeCh <- code:
	default:
	}
}

// WaitForCode waits for the authorization code from the callback server.
// It blocks until a code is received, an error occurs, or the context is cancelled.
func (cs *CallbackServer) WaitForCode(ctx context.Context) (string, error) {
	select {
	case code := <-cs.codeCh:
		return code, nil
	case err := <-cs.errCh:
		return "", err
	case <-ctx.Done():
		return "", fmt.Errorf("timed out waiting for authorization callback: %w", ctx.Err())
	}
}

// Shutdown gracefully shuts down the callback server.
func (cs *CallbackServer) Shutdown(ctx context.Context) error {
	return cs.server.Shutdown(ctx)
}

// RedirectURI returns the full redirect URI for this callback server.
func (cs *CallbackServer) RedirectURI() string {
	return fmt.Sprintf("http://localhost:%d/callback", cs.Port)
}

// BuildAuthURL builds the OAuth 2.0 authorization URL with PKCE parameters.
func BuildAuthURL(baseURL, clientID, redirectURI, codeChallenge string) string {
	baseURL = strings.TrimSuffix(baseURL, "/")
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	return baseURL + "/oauth/authorize?" + params.Encode()
}

// OpenBrowser opens the given URL in the system's default browser.
func OpenBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	case "darwin":
		cmd = exec.Command("open", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}
