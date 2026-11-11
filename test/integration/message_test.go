package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/wire"
	"github.com/portpowered/portos-cli/test/mocks"
)

// TestMessageCommand tests the message command with simple command
func TestMessageCommand(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	messageJSON, err := loadFixture("message_response.json")
	if err != nil {
		t.Fatalf("failed to load message fixture: %v", err)
	}

	routes := map[string]http.RoundTripper{
		"/message-requests": mocks.NewJSONMockRoundTripper(200, messageJSON),
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "port1/principals/user123/endpoints/endpoint1", "turn-on", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute message command: %v", err)
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

	if status, ok := result["status"].(string); !ok || status != "QUEUED" {
		t.Errorf("expected durable request status QUEUED, got %v", result["status"])
	}
}

// TestMessageCommandWithArgs tests the message command with arguments
func TestMessageCommandWithArgs(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	messageJSON, err := loadFixture("message_response.json")
	if err != nil {
		t.Fatalf("failed to load message fixture: %v", err)
	}

	routes := map[string]http.RoundTripper{
		"/message-requests": mocks.NewJSONMockRoundTripper(200, messageJSON),
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "port1/principals/user123/endpoints/endpoint1", "power/set_power", "power=true", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute message command: %v", err)
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

	if status, ok := result["status"].(string); !ok || status != "QUEUED" {
		t.Errorf("expected durable request status QUEUED, got %v", result["status"])
	}
}

// TestMessageCommandWithJSONArgs tests the message command with JSON arguments
func TestMessageCommandWithJSONArgs(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	messageJSON, err := loadFixture("message_response.json")
	if err != nil {
		t.Fatalf("failed to load message fixture: %v", err)
	}

	routes := map[string]http.RoundTripper{
		"/message-requests": mocks.NewJSONMockRoundTripper(200, messageJSON),
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "port1/principals/user123/endpoints/endpoint1", "power/set_power", `{"power": true}`, "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute message command: %v", err)
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

	if status, ok := result["status"].(string); !ok || status != "QUEUED" {
		t.Errorf("expected durable request status QUEUED, got %v", result["status"])
	}
}

// TestMessageCommandMissingArgs tests error handling for missing arguments
func TestMessageCommandMissingArgs(t *testing.T) {
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message"))

	ctx := context.Background()
	err = rootCmd.ExecuteContext(ctx)
	if err == nil {
		t.Fatal("expected error for missing arguments, but got none")
	}
}

// TestMessageCommandResponseStructure validates the CLI response JSON structure and values
func TestMessageCommandResponseStructure(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	messageJSON, err := loadFixture("message_response.json")
	if err != nil {
		t.Fatalf("failed to load message fixture: %v", err)
	}

	routes := map[string]http.RoundTripper{
		"/message-requests": mocks.NewJSONMockRoundTripper(200, messageJSON),
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "port1/principals/user123/endpoints/endpoint1", "turn-on", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute message command: %v", err)
	}

	actualOutput := testWriter.StdoutString()
	if actualOutput == "" {
		t.Fatal("expected output, but got empty string")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse output JSON: %v\nOutput was: %s", err, actualOutput)
	}

	// Required: success is true
	if status, ok := result["status"].(string); !ok || status != "QUEUED" {
		t.Errorf("expected durable request status QUEUED, got %v", result["status"])
	}

	if id, ok := result["id"].(string); !ok || id != "message-request-1234567890" {
		t.Errorf("expected durable request ID from fixture, got %v", result["id"])
	}
	if _, present := result["results"]; !present {
		t.Error("canonical results list missing")
	}
	if _, present := result["errors"]; !present {
		t.Error("canonical errors list missing")
	}

}

// TestMessageCommandSpeakRawText validates that raw text args are wrapped in SSML for the speak command
func TestMessageCommandSpeakRawText(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)
	setTestAPIEnv(t)

	messageJSON, err := loadFixture("message_response.json")
	if err != nil {
		t.Fatalf("failed to load message fixture: %v", err)
	}

	var capturedRequestBody []byte
	messageMock := &messageCapturingRoundTripper{
		messageJSON: messageJSON,
		captureFunc: func(body []byte) {
			capturedRequestBody = body
		},
	}

	routes := map[string]http.RoundTripper{
		"/message-requests": messageMock,
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
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "speak", "port1/principals/user123/endpoints/ep1", "Hello", "world", "--json"))

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("failed to execute message command: %v", err)
	}

	if len(capturedRequestBody) == 0 {
		t.Fatal("expected to capture message request body")
	}

	assertSpeakRequestBody(t, capturedRequestBody)

	// Validate CLI response
	actualOutput := testWriter.StdoutString()
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(actualOutput), &result); err != nil {
		t.Fatalf("failed to parse CLI output JSON: %v", err)
	}
	if status, ok := result["status"].(string); !ok || status != "QUEUED" {
		t.Errorf("expected durable request status QUEUED, got %v", result["status"])
	}
}

func assertSpeakRequestBody(t *testing.T, capturedRequestBody []byte) {
	var requestBody map[string]interface{}
	if err := json.Unmarshal(capturedRequestBody, &requestBody); err != nil {
		t.Fatalf("failed to parse captured request: %v", err)
	}

	message, ok := requestBody["message"].(map[string]interface{})
	if !ok {
		t.Fatal("expected request to have a message object")
	}

	body, ok := message["body"].(map[string]interface{})
	if !ok {
		t.Fatal("expected message to have a body object")
	}

	input, ok := body["input"].(map[string]interface{})
	if !ok {
		t.Fatal("expected speak message body to have input object")
	}

	if typ, ok := input["type"].(string); !ok || typ != "SSML" {
		t.Errorf("expected input.type to be SSML, got %v", input["type"])
	}

	expectedSSML := "<speak>Hello world</speak>"
	if ssml, ok := input["ssml"].(string); !ok || ssml != expectedSSML {
		t.Errorf("expected input.ssml to be %q, got %q", expectedSSML, input["ssml"])
	}
}

// TestMessageCommandNoToken asserts that running without a token produces a concise,
// actionable error that does not repeat "failed to send message" more than once.
func TestMessageCommandNoToken(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)

	// Set base URL but deliberately omit PORTOS_ACCESS_TOKEN.
	t.Setenv(config.EnvAPIBaseURL, testAPIBaseURL)
	t.Setenv(config.EnvAccessToken, "")

	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(setupMockClientWithRoutes(nil), testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, "message", "turn-on", "port1/principals/user123/endpoints/endpoint1"))

	ctx := context.Background()
	cmdErr := rootCmd.ExecuteContext(ctx)
	if cmdErr == nil {
		t.Fatal("expected error when no token is set, but got none")
	}

	msg := cmdErr.Error()

	// AC-1: "failed to send message" must not appear more than once.
	if strings.Count(msg, "failed to send message") > 1 {
		t.Errorf("error message contains 'failed to send message' more than once: %q", msg)
	}

	// AC-2: actionable guidance must be present.
	if !strings.Contains(msg, "PORTOS_ACCESS_TOKEN") && !strings.Contains(msg, "port auth login") {
		t.Errorf("error message must mention PORTOS_ACCESS_TOKEN or 'port auth login', got: %q", msg)
	}

	// AC-3: single line under 120 characters.
	if strings.Contains(msg, "\n") {
		t.Errorf("error message must be a single line, got: %q", msg)
	}
	if len(msg) >= 120 {
		t.Errorf("error message must be under 120 characters (got %d): %q", len(msg), msg)
	}
}
