package integration

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
)

// TestListCommandsJSON tests the list-commands subcommand with JSON output
func TestListCommandsJSON(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient, err := setupMockClient("/endpoints", "endpoint_single.json")
	if err != nil {
		t.Fatalf("failed to set up mock client: %v", err)
	}

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "list-commands", "port1/principals/user123/endpoints/endpoint1", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute list-commands: %v", err)
	}

	actualOutput := testWriter.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	// Verify endpoint_id is present
	if result["endpoint_id"] != "port1/principals/user123/endpoints/endpoint1" {
		t.Errorf("expected endpoint_id to be the full ID, got %v", result["endpoint_id"])
	}

	// Verify interfaces array is present with commands
	interfaces, ok := result["interfaces"].([]interface{})
	if !ok {
		t.Fatalf("expected interfaces to be an array, got %T", result["interfaces"])
	}
	if len(interfaces) == 0 {
		t.Fatal("expected at least one interface")
	}

	// Verify the power interface has the turn-off command (from fixture)
	iface := interfaces[0].(map[string]interface{})
	if iface["name"] != "power" {
		t.Errorf("expected first interface to be 'power', got %v", iface["name"])
	}

	commands, ok := iface["commands"].([]interface{})
	if !ok || len(commands) == 0 {
		t.Fatal("expected commands array with at least one entry")
	}
	if commands[0] != "turn-off" {
		t.Errorf("expected first command to be 'turn-off', got %v", commands[0])
	}
}

// TestListCommandsHumanReadable tests the list-commands subcommand with human-readable output
func TestListCommandsHumanReadable(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient, err := setupMockClient("/endpoints", "endpoint_single.json")
	if err != nil {
		t.Fatalf("failed to set up mock client: %v", err)
	}

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "list-commands", "port1/principals/user123/endpoints/endpoint1"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute list-commands: %v", err)
	}

	actualOutput := testWriter.StdoutString()
	if !strings.Contains(actualOutput, "power:") {
		t.Errorf("expected output to contain 'power:', got: %s", actualOutput)
	}
	if !strings.Contains(actualOutput, "turn-off") {
		t.Errorf("expected output to contain 'turn-off', got: %s", actualOutput)
	}
}

// TestListCommandsMissingEndpointID tests that list-commands requires an endpoint ID
func TestListCommandsMissingEndpointID(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient, err := setupMockClient("/endpoints", "endpoint_single.json")
	if err != nil {
		t.Fatalf("failed to set up mock client: %v", err)
	}

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "list-commands"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error when no endpoint ID provided")
	}
}
