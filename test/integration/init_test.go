package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/wire"
)

// TestInitCommandNewConfig tests the init command when no config exists
func TestInitCommandNewConfig(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL, "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
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

	if initialized, ok := result["initialized"].(bool); !ok || !initialized {
		t.Errorf("expected initialized to be true, got %v", result["initialized"])
	}
}

// TestInitCommandExistingConfig tests the init command when config already exists
func TestInitCommandExistingConfig(t *testing.T) {
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

	// First, run init to create config
	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL))
	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Run init again - should detect existing config
	// Create a new CLI instance with a new writer
	testWriter2 := NewTestWriter()
	cli2, err := wire.InitializeMockCLI(nil, testWriter2)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd2 := cli2.Generate()
	rootCmd2.SetOut(testWriter2.Stdout())
	rootCmd2.SetErr(testWriter2.Stderr())
	rootCmd2.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL, "--json"))

	if err := rootCmd2.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Validate the output
	actualOutput := testWriter2.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	if initialized, ok := result["initialized"].(bool); !ok || initialized {
		t.Errorf("expected initialized to be false, got %v", result["initialized"])
	}
}

// TestInitCommandDefaultAPIURL tests that init without --api-url uses the default production URL
func TestInitCommandDefaultAPIURL(t *testing.T) {
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
	// Omit --api-url to test default behavior
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Validate initialization succeeded
	actualOutput := testWriter.StdoutString()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	if initialized, ok := result["initialized"].(bool); !ok || !initialized {
		t.Errorf("expected initialized to be true, got %v", result["initialized"])
	}

	// Read the config file directly to verify the default API URL was written
	configPath := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	configContent := string(data)
	if !strings.Contains(configContent, config.DefaultAPIBaseURL) {
		t.Errorf("expected config to contain default API URL %q, got: %s", config.DefaultAPIBaseURL, configContent)
	}
}

// TestInitCommandCustomAPIURL tests that init with --api-url writes the custom URL
func TestInitCommandCustomAPIURL(t *testing.T) {
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

	customURL := "http://localhost:9090"
	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", customURL, "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Validate initialization succeeded
	actualOutput := testWriter.StdoutString()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	if initialized, ok := result["initialized"].(bool); !ok || !initialized {
		t.Errorf("expected initialized to be true, got %v", result["initialized"])
	}

	// Read the config file directly to verify the custom API URL
	configPath := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	configContent := string(data)
	if !strings.Contains(configContent, customURL) {
		t.Errorf("expected config to contain custom API URL %q, got: %s", customURL, configContent)
	}
	if strings.Contains(configContent, config.DefaultAPIBaseURL) {
		t.Errorf("expected config NOT to contain default API URL when custom URL is specified, got: %s", configContent)
	}
}

// TestInitCommandWithAuthURL tests that init with --auth-url writes auth_base_url to config
func TestInitCommandWithAuthURL(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL, "--auth-url", "https://auth.example.com", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Read config file and verify auth_base_url is present
	configPath := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	configContent := string(data)
	if !strings.Contains(configContent, "auth_base_url: https://auth.example.com") {
		t.Errorf("expected config to contain auth_base_url, got: %s", configContent)
	}
}

// TestInitCommandWithoutAuthURL tests that init without --auth-url omits auth_base_url from config
func TestInitCommandWithoutAuthURL(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL, "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	// Read config file and verify auth_base_url is NOT present
	configPath := filepath.Join(configDir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}

	configContent := string(data)
	if strings.Contains(configContent, "auth_base_url") {
		t.Errorf("expected config NOT to contain auth_base_url when --auth-url is not specified, got: %s", configContent)
	}
}

// TestInitCommandNextStepsNewConfig tests that init prints auth guidance on first-time init
func TestInitCommandNextStepsNewConfig(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute init command: %v", err)
	}

	output := testWriter.StdoutString()
	if !strings.Contains(output, "port auth set-token") && !strings.Contains(output, "port auth login") {
		t.Errorf("expected next-steps auth guidance in output, got: %s", output)
	}
}

// TestInitCommandNextStepsExistingConfig tests that init prints auth guidance when config already exists
func TestInitCommandNextStepsExistingConfig(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)

	// First init to create config
	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}
	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL))
	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute first init: %v", err)
	}

	// Second init - already exists path
	testWriter2 := NewTestWriter()
	cli2, err := wire.InitializeMockCLI(nil, testWriter2)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}
	rootCmd2 := cli2.Generate()
	rootCmd2.SetOut(testWriter2.Stdout())
	rootCmd2.SetErr(testWriter2.Stderr())
	rootCmd2.SetArgs(ArgsWithConfigDir(configDir, "init", "--api-url", testAPIBaseURL))
	if err := rootCmd2.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute second init: %v", err)
	}

	output := testWriter2.StdoutString()
	if !strings.Contains(output, "port auth set-token") && !strings.Contains(output, "port auth login") {
		t.Errorf("expected next-steps auth guidance in output, got: %s", output)
	}
}
