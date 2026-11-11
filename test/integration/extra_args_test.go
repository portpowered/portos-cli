package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/wire"
)

// TestEndpointListRejectsExtraArgs asserts that 'port endpoint list <extra>' exits non-zero
// with a meaningful error and does NOT return the normal list JSON output.
func TestEndpointListRejectsExtraArgs(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	t.Setenv(config.EnvAccessToken, generateTestJWT(t))

	mockClient, err := setupMockClient("/endpoints", "endpoints_list.json")
	if err != nil {
		t.Fatalf("failed to setup mock client: %v", err)
	}

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "unexpected-arg"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected non-zero exit for extra positional arg, but command succeeded")
	}

	// Error message should reference the unexpected argument
	errMsg := err.Error()
	if !strings.Contains(errMsg, "unknown") && !strings.Contains(errMsg, "accepts") && !strings.Contains(errMsg, "arg") {
		t.Errorf("expected error message referencing unexpected argument, got: %v", errMsg)
	}

	// The normal endpoint list JSON output should NOT be present (cobra may print usage to stdout)
	stdout := testWriter.StdoutString()
	if strings.Contains(stdout, `"endpoints"`) {
		t.Errorf("expected no endpoint list JSON in output on arg error, got: %s", stdout)
	}
}

// TestEndpointListRejectsMultipleExtraArgs asserts that 'port endpoint list -- extra args here' exits non-zero.
func TestEndpointListRejectsMultipleExtraArgs(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	t.Setenv(config.EnvAccessToken, generateTestJWT(t))

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--", "extra", "args", "here"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected non-zero exit for extra positional args after --, but command succeeded")
	}
}

// TestListFilesystemsRejectsExtraArgs asserts that 'port files list-filesystems <extra>' exits non-zero.
