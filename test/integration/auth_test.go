package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/wire"
)

// mockAuthService is a minimal auth.Service implementation for testing the login command path.
type mockAuthService struct {
	browserLoginErr error
}

func (m *mockAuthService) SetToken(_ context.Context, _ string) error { return nil }
func (m *mockAuthService) Login(_ context.Context, _ string, _ auth.LoginMessageHandler) error {
	return nil
}
func (m *mockAuthService) BrowserLogin(_ context.Context, handler auth.LoginMessageHandler) error {
	if handler != nil {
		handler("Authentication successful!\n")
	}
	return m.browserLoginErr
}
func (m *mockAuthService) GetAccessToken(_ context.Context) (string, error) { return "", nil }
func (m *mockAuthService) RefreshToken(_ context.Context) error             { return nil }
func (m *mockAuthService) IsAuthenticated(_ context.Context) (bool, error)  { return false, nil }
func (m *mockAuthService) GetPrincipalPathFromToken(_ context.Context) (string, string, string, error) {
	return "", "", "", nil
}

// TestAuthSetToken tests the auth set-token command
func TestAuthSetToken(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	testToken := generateTestJWT(t)

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "auth", "set-token", testToken, "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute auth set-token command: %v", err)
	}

	// Validate the output
	actualOutput := testWriter.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	if success, ok := result["success"].(bool); !ok || !success {
		t.Errorf("expected success to be true, got %v", result["success"])
	}
}

// TestAuthLoginSuccess tests the auth login command succeeds when BrowserLogin returns a token.
// It uses a mock auth.Service to avoid requiring a real browser or OAuth server.
func TestAuthLoginSuccess(t *testing.T) {
	testWriter := NewTestWriter()
	logRef := logging.NewLoggerRef()
	jsonFmt := output.NewJSONFormatter(testWriter, logRef)
	globalFlags := flags.NewGlobalFlags()
	configProvider := config.NewManagerProvider(globalFlags)
	mockSvc := &mockAuthService{}

	authCmd := commands.NewAuthCommand(mockSvc, configProvider, jsonFmt, logRef)
	err := authCmd.Login(context.Background(), commands.LoginParams{})
	if err != nil {
		t.Fatalf("expected auth login to succeed, got: %v", err)
	}

	actual := testWriter.StdoutString()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actual), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actual)
	}
	if success, ok := result["success"].(bool); !ok || !success {
		t.Errorf("expected success to be true, got %v", result["success"])
	}
}

// TestAuthSetTokenEmpty tests error handling for empty token
func TestAuthSetTokenEmpty(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "auth", "set-token", ""))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error for empty token, but got none")
	}
}

// TestAuthLoginUnknownFlag verifies that the auth login command rejects unknown flags.
func TestAuthLoginUnknownFlag(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	// A flag login does not define must fail before any login starts. (--client-id is a real
	// flag again, for --client-credentials; with it this test opened a browser sign-in.)
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "auth", "login", "--no-such-flag", "some-id"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error for an unknown flag, but got none")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("error %q, want an unknown-flag error", err)
	}
}
