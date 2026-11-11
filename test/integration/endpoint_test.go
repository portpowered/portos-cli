package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
	"github.com/portpowered/portos-cli/test/mocks"
)

// TestEndpointShowWithSingleEndpoint tests the CLI endpoint show command
// against a mock customer who has a single endpoint
func TestEndpointShowWithSingleEndpoint(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	responseBody, err := loadFixture("endpoint_single_expanded.json")
	if err != nil {
		t.Fatalf("failed to load expanded endpoint fixture: %v", err)
	}
	mockClient := setupMockClientWithRoutes(map[string]http.RoundTripper{
		testAPIBaseURL + "/endpoints?expand=interfaces.attributes&id=port1%2Fprincipals%2Fuser123%2Fendpoints%2Fendpoint1": mocks.NewJSONMockRoundTripper(200, responseBody),
	})

	testWriter := NewTestWriter()
	// Initialize CLI with the mock HTTP client
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	// Generate the root command
	rootCmd := cli.Generate()

	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	// Set up test arguments for endpoint show
	// The endpoint ID from the fixture is "port1/principals/user123/endpoints/endpoint1"
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "show", "port1/principals/user123/endpoints/endpoint1", "--json"))

	// Execute the command
	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute endpoint show command: %v", err)
	}

	// Validate the output
	actualOutput := testWriter.stdout.String()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	assertEndpointShowJSON(t, actualOutput)
}

func assertEndpointShowJSON(t *testing.T, actualOutput string) {
	// Parse the expected JSON
	expectedJSON := `{
  "id": "port1/principals/user123/endpoints/endpoint1",
  "name": "Living Room Light",
  "type": "smart_light",
  "interfaces": [
    {
      "name": "power",
      "commands": [
        "turn-off"
      ],
      "attributes": [
        "power"
      ]
    }
  ],
  "manufacturer": "Philips",
  "model": "Hue White",
  "serial_number": "12345678",
  "description": "A smart light bulb in the living room",
  "state": {
    "power": "on"
  }
}`

	// Parse both JSON structures for comparison
	var expected, actual map[string]interface{}
	if err := json.Unmarshal([]byte(expectedJSON), &expected); err != nil {
		t.Fatalf("failed to parse expected JSON: %v", err)
	}

	if err := json.Unmarshal([]byte(actualOutput), &actual); err != nil {
		t.Fatalf("failed to parse actual output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	// Compare the JSON structures
	expectedBytes, _ := json.MarshalIndent(expected, "", "  ")
	actualBytes, _ := json.MarshalIndent(actual, "", "  ")

	// Compare by re-marshaling and comparing (handles field order differences)
	if string(expectedBytes) != string(actualBytes) {
		t.Errorf("output mismatch!\nExpected:\n%s\n\nActual:\n%s", string(expectedBytes), string(actualBytes))
	}
}

// TestEndpointList tests the endpoint list command
func TestEndpointList(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute endpoint list command: %v", err)
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

	endpoints, ok := result["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %T", result["endpoints"])
	}

	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d", len(endpoints))
	}
}

// TestEndpointListJSONContainsFullID asserts that 'port endpoint list --json' output
// includes the full 'id' field for every endpoint.
func TestEndpointListJSONContainsFullID(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute endpoint list command: %v", err)
	}

	actualOutput := testWriter.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	endpoints, ok := result["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %T", result["endpoints"])
	}
	if len(endpoints) == 0 {
		t.Fatal("expected at least one endpoint in output")
	}

	// Assert that every endpoint has a non-empty 'id' field (full endpoint ID).
	for i, ep := range endpoints {
		epMap, ok := ep.(map[string]interface{})
		if !ok {
			t.Fatalf("endpoint %d is not an object", i)
		}

		id, ok := epMap["id"].(string)
		if !ok || id == "" {
			t.Errorf("endpoint %d: expected non-empty 'id' field in JSON output, got %v", i, epMap["id"])
		}
	}

	// Verify the specific IDs from the fixture match
	ep0 := endpoints[0].(map[string]interface{})
	if ep0["id"] != "port1/principals/user123/endpoints/endpoint1" {
		t.Errorf("endpoint 0: expected full ID 'port1/principals/user123/endpoints/endpoint1', got %q", ep0["id"])
	}

	ep1 := endpoints[1].(map[string]interface{})
	if ep1["id"] != "port1/principals/user123/endpoints/endpoint2" {
		t.Errorf("endpoint 1: expected full ID 'port1/principals/user123/endpoints/endpoint2', got %q", ep1["id"])
	}
}

// TestEndpointListWithFilters tests the endpoint list command with filters
func TestEndpointListWithFilters(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--owner", "port1/principals/user123", "--max-results", "10", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute endpoint list command: %v", err)
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

	endpoints, ok := result["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %T", result["endpoints"])
	}

	if len(endpoints) == 0 {
		t.Error("expected at least one endpoint")
	}
}

// TestEndpointListWithTypeAndSerialNumberFilter tests that --type and --serial-number are accepted and sent to the API
func TestEndpointListWithTypeAndSerialNumberFilter(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--type", "light", "--serial-number", "SN-12345", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute endpoint list command with type and serial-number: %v", err)
	}

	actualOutput := testWriter.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	// Verify we got endpoints (mock returns list fixture)
	endpoints, ok := result["endpoints"].([]interface{})
	if !ok {
		t.Fatalf("expected endpoints array, got %T", result["endpoints"])
	}
	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints from fixture, got %d", len(endpoints))
	}
}

// TestEndpointShowNotFound tests the endpoint show command when endpoint is not found
func TestEndpointShowNotFound(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	mockClient, err := setupMockClient("/endpoints", "endpoint_empty.json")
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "show", "nonexistent-endpoint", "--json"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error when endpoint not found, but got none")
	}

	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

// TestUnauthorizedError tests error handling for unauthorized requests
func TestUnauthorizedError(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	errorJSON, err := loadFixture("error_unauthorized.json")
	if err != nil {
		t.Fatalf("failed to load error fixture: %v", err)
	}

	routes := map[string]http.RoundTripper{
		"/endpoints": mocks.NewJSONMockRoundTripper(401, errorJSON),
	}
	mockClient := setupMockClientWithRoutes(routes)

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(mockClient, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--json"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error for unauthorized request, but got none")
	}
}

// TestEndpointListWithInvalidMaxResults tests error handling for invalid max-results
func TestEndpointListWithInvalidMaxResults(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "endpoint", "list", "--max-results", "invalid"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error for invalid max-results, but got none")
	}

	if !strings.Contains(err.Error(), "max-results") {
		t.Errorf("expected 'max-results' error, got: %v", err)
	}
}
