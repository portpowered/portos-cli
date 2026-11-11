package integration

import (
	"context"
	"os"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
)

// TestEndpointListQuietModeSuppressesOutput verifies that --quiet suppresses all stdout output.
func TestEndpointListQuietModeSuppressesOutput(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--quiet"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("command failed: %v", err)
	}

	stdout := testWriter.StdoutString()
	if stdout != "" {
		t.Errorf("expected no stdout output with --quiet, but got:\n%s", stdout)
	}
}

// TestEndpointListShortQuietFlag verifies that -q (short flag) also suppresses output.
func TestEndpointListShortQuietFlag(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "-q"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("command failed: %v", err)
	}

	stdout := testWriter.StdoutString()
	if stdout != "" {
		t.Errorf("expected no stdout output with -q, but got:\n%s", stdout)
	}
}

// TestEndpointListQuietWithJSON verifies that --quiet --json produces no stdout output.
func TestEndpointListQuietWithJSON(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--quiet", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("command failed: %v", err)
	}

	stdout := testWriter.StdoutString()
	if stdout != "" {
		t.Errorf("expected no stdout output with --quiet --json, but got:\n%s", stdout)
	}
}

// TestEndpointShowQuietModeSuppressesOutput verifies --quiet works on endpoint show.
func TestEndpointShowQuietModeSuppressesOutput(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient, err := setupMockClient("/endpoints", "endpoint_single.json")
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "show", "port1/principals/user123/endpoints/endpoint1", "--quiet"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("command failed: %v", err)
	}

	stdout := testWriter.StdoutString()
	if stdout != "" {
		t.Errorf("expected no stdout output with --quiet, but got:\n%s", stdout)
	}
}

// TestFilesystemListQuietModeSuppressesOutput verifies --quiet works on filesystem list.
