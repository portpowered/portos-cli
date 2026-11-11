package integration

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/test/mocks"
)

// fixtureDir is resolved when the package initializes, while the working directory is still
// this package's directory (go test starts there; some tests chdir later). Builds use
// -trimpath, so runtime.Caller cannot locate source files.
var fixtureDir, fixtureDirErr = filepath.Abs(filepath.Join("..", "fixtures"))

// getFixtureDir returns the absolute path to the test fixtures directory
func getFixtureDir() string {
	if fixtureDirErr != nil {
		panic(fixtureDirErr)
	}
	return fixtureDir
}

// generateTestJWT generates a JWT token for testing purposes
// The token will be valid for 1 hour from now
func generateTestJWT(t *testing.T) string {
	// Create claims with expiry 1 hour from now
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": "test-user",                   // Subject
		"iat": now.Unix(),                    // Issued at
		"exp": now.Add(1 * time.Hour).Unix(), // Expiry time
		"iss": "test-issuer",                 // Issuer
	}

	// Create token with RS256 signing (using a throwaway test key)
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

type testWriter struct {
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func (w *testWriter) Stdout() io.Writer {
	return &w.stdout
}

func (w *testWriter) Stderr() io.Writer {
	return &w.stderr
}

func (w *testWriter) StdoutString() string {
	return w.stdout.String()
}

func (w *testWriter) StderrString() string {
	return w.stderr.String()
}

// Reset clears stdout and stderr buffers (e.g. between multiple commands in one test).
func (w *testWriter) Reset() {
	w.stdout.Reset()
	w.stderr.Reset()
}

func NewTestWriter() *testWriter {
	return &testWriter{
		stdout: bytes.Buffer{},
		stderr: bytes.Buffer{},
	}
}

func setupMockClient(path string, fixturaPath string) (*http.Client, error) {
	fixtureDir := getFixtureDir()
	fixturePath := filepath.Join(fixtureDir, fixturaPath)

	// Load the single endpoint fixture
	endpointData, err := os.ReadFile(fixturePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load endpoint fixture: %v", err)
	}

	// Parse the endpoint to ensure it's valid JSON
	var endpoint map[string]interface{}
	if err := json.Unmarshal(endpointData, &endpoint); err != nil {
		return nil, fmt.Errorf("failed to parse endpoint fixture: %v", err)
	}

	// Convert to JSON
	responseJSON, err := json.Marshal(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %v", err)
	}

	routes := map[string]http.RoundTripper{
		path: mocks.NewJSONMockRoundTripper(200, string(responseJSON)),
	}
	mockClient := mocks.NewRoutingMockClient(routes)
	return mockClient, nil
}

func setupMockClientWithRoutes(routes map[string]http.RoundTripper) *http.Client {
	return mocks.NewRoutingMockClient(routes)
}

func loadFixture(fixturePath string) (string, error) {
	fixtureDir := getFixtureDir()
	fullPath := filepath.Join(fixtureDir, fixturePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to load fixture: %v", err)
	}
	return string(data), nil
}

// testAPIBaseURL is the base URL used in all integration tests.
// Since tests use mock HTTP clients, any valid URL works.
const testAPIBaseURL = "http://localhost:8080"

// setTestAPIEnv sets PORTOS_ACCESS_TOKEN and PORTOS_API_BASE_URL for tests that call API commands.
// It returns the generated JWT token string.
func setTestAPIEnv(t *testing.T) string {
	t.Helper()
	token := generateTestJWT(t)
	t.Setenv(config.EnvAccessToken, token)
	t.Setenv(config.EnvAPIBaseURL, testAPIBaseURL)
	return token
}

// setDirectory creates a temporary directory for the test, changes the process working
// directory to it, and returns its path. Callers should pass this path as --config-dir
// so the CLI uses it for config/credentials for the duration of the test.
// Restore the original working directory when the test ends (e.g. defer os.Chdir(originalDir)).
func setDirectory(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to change to temp directory: %v", err)
	}
	return tmpDir
}

// ArgsWithConfigDir prepends --config-dir and configDir to the given command args.
// Use this so integration tests use a temp config directory instead of ~/.portos.
func ArgsWithConfigDir(configDir string, args ...string) []string {
	return append([]string{"--config-dir", configDir}, args...)
}

// messageCapturingRoundTripper captures request bodies for inspection
type messageCapturingRoundTripper struct {
	messageJSON string
	captureFunc func([]byte)
}

func (c *messageCapturingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Read and capture the request body without consuming it
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			c.captureFunc(bodyBytes)
			// Restore the body for the actual response handling
			req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		}
	}

	// Return the mock response
	return mocks.NewJSONMockRoundTripper(200, c.messageJSON).RoundTrip(req)
}
