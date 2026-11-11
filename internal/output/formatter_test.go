package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
	"go.uber.org/zap/zaptest"
)

// MockWriter implements the Writer interface for testing
type MockWriter struct {
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (w *MockWriter) Stdout() io.Writer {
	return w.stdout
}

func (w *MockWriter) Stderr() io.Writer {
	return w.stderr
}

func NewMockWriter() *MockWriter {
	return &MockWriter{
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
	}
}

func loggerRefForTest(t *testing.T) *logging.LoggerRef {
	ref := logging.NewLoggerRef()
	ref.Set(zaptest.NewLogger(t))
	return ref
}

func TestJSONFormatter_Print(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewJSONFormatter(writer, loggerRefForTest(t))

	testData := map[string]string{"key": "value"}
	err := formatter.Print(testData)
	if err != nil {
		t.Fatalf("Print failed: %v", err)
	}

	var result map[string]string
	stdout := writer.Stdout().(*bytes.Buffer)
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal JSON output: %v", err)
	}

	if result["key"] != "value" {
		t.Errorf("Expected key=value, got key=%s", result["key"])
	}
}

func TestJSONFormatter_PrintError(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewJSONFormatter(writer, loggerRefForTest(t))

	testErr := errors.New("test error")
	err := formatter.PrintError(testErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	var result map[string]interface{}
	stderr := writer.Stderr().(*bytes.Buffer)
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal JSON error output: %v", err)
	}

	errorObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error to be an object, got %T: %v", result["error"], result["error"])
	}

	msg, ok := errorObj["message"].(string)
	if !ok {
		t.Fatalf("Expected error.message to be a string, got %T", errorObj["message"])
	}
	if msg != "test error" {
		t.Errorf("Expected error.message='test error', got '%s'", msg)
	}

	errType, ok := errorObj["type"].(string)
	if !ok {
		t.Fatalf("Expected error.type to be a string, got %T", errorObj["type"])
	}
	if errType != "cli_error" {
		t.Errorf("Expected error.type='cli_error', got '%s'", errType)
	}
}

func TestJSONFormatter_IsJSON(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewJSONFormatter(writer, loggerRefForTest(t))

	if !formatter.IsJSON() {
		t.Error("Expected IsJSON() to return true for JSONFormatter")
	}
}

func TestHumanFormatter_Print(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewHumanFormatter(writer, loggerRefForTest(t))

	testData := "test string"
	err := formatter.Print(testData)
	if err != nil {
		t.Fatalf("Print failed: %v", err)
	}

	stdout := writer.Stdout().(*bytes.Buffer)
	output := strings.TrimSpace(stdout.String())
	if output != "test string" {
		t.Errorf("Expected 'test string', got '%s'", output)
	}
}

func TestHumanFormatter_PrintError(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewHumanFormatter(writer, loggerRefForTest(t))

	testErr := errors.New("test error")
	err := formatter.PrintError(testErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	stderr := writer.Stderr().(*bytes.Buffer)
	output := strings.TrimSpace(stderr.String())
	expected := "Error: test error"
	if output != expected {
		t.Errorf("Expected '%s', got '%s'", expected, output)
	}
}

func TestHumanFormatter_IsJSON(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewHumanFormatter(writer, loggerRefForTest(t))

	if formatter.IsJSON() {
		t.Error("Expected IsJSON() to return false for HumanFormatter")
	}
}

func TestJSONFormatter_PrintError_APIError(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewJSONFormatter(writer, loggerRefForTest(t))

	errCode, errFamily := "INVALID_VALUE", "REQUEST_INVALID"
	errMsg := "invalid endpoint id"
	apiErr := &apiclient.APIError{
		StatusCode: 400,
		Code:       &errCode,
		Family:     &errFamily,
		Message:    &errMsg,
		Err:        fmt.Errorf("failed to send message: invalid endpoint id (status: 400)"),
	}

	err := formatter.PrintError(apiErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	var result map[string]interface{}
	stderr := writer.Stderr().(*bytes.Buffer)
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal JSON error output: %v", err)
	}

	errorObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error to be an object, got %T", result["error"])
	}

	if errorObj["message"] != "invalid endpoint id" {
		t.Errorf("Expected error.message='invalid endpoint id', got '%v'", errorObj["message"])
	}
	if errorObj["type"] != "api_error" || errorObj["code"] != "INVALID_VALUE" || errorObj["family"] != "REQUEST_INVALID" {
		t.Errorf("Expected type api_error with code INVALID_VALUE and family REQUEST_INVALID, got %v", errorObj)
	}
	status, ok := errorObj["status"].(float64)
	if !ok {
		t.Fatalf("Expected error.status to be a number, got %T", errorObj["status"])
	}
	if int(status) != 400 {
		t.Errorf("Expected error.status=400, got %d", int(status))
	}
}

func TestJSONFormatter_PrintError_404HintField(t *testing.T) {
	writer := NewMockWriter()
	formatter := NewJSONFormatter(writer, loggerRefForTest(t))

	errMsg := "requested resource not found"
	apiErr := &apiclient.APIError{
		StatusCode: 404,
		Message:    &errMsg,
		Err:        fmt.Errorf("%s (status: 404)", errMsg),
	}

	err := formatter.PrintError(apiErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	var result map[string]interface{}
	stderr := writer.Stderr().(*bytes.Buffer)
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("Failed to unmarshal JSON error output: %v", err)
	}

	errorObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error to be an object, got %T", result["error"])
	}

	// Original message preserved
	if errorObj["message"] != "requested resource not found" {
		t.Errorf("Expected original message preserved, got '%v'", errorObj["message"])
	}

	// Hint field present
	hint, ok := errorObj["hint"].(string)
	if !ok {
		t.Fatal("Expected error.hint to be a string")
	}
	if !strings.Contains(hint, "list-filesystems") {
		t.Errorf("Expected hint to mention list-filesystems, got: %s", hint)
	}

	// Status preserved
	status, ok := errorObj["status"].(float64)
	if !ok {
		t.Fatalf("Expected error.status to be a number, got %T", errorObj["status"])
	}
	if int(status) != 404 {
		t.Errorf("Expected error.status=404, got %d", int(status))
	}
}

func TestRootFormatter_PrintError_JSONMode(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{OutputInJson: true}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	testErr := errors.New("something went wrong")
	err := formatter.PrintError(testErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	var result map[string]interface{}
	stderr := writer.Stderr().(*bytes.Buffer)
	if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
		t.Fatalf("Output is not valid JSON: %v\nGot: %s", err, stderr.String())
	}

	errorObj, ok := result["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("Expected error to be an object, got %T", result["error"])
	}
	if _, ok := errorObj["message"].(string); !ok {
		t.Error("Expected error.message to be a string")
	}
	if _, ok := errorObj["type"].(string); !ok {
		t.Error("Expected error.type to be a string")
	}
}

func TestRootFormatter_PrintError_HumanMode(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{OutputInJson: false}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	testErr := errors.New("something went wrong")
	err := formatter.PrintError(testErr)
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	stderr := writer.Stderr().(*bytes.Buffer)
	output := strings.TrimSpace(stderr.String())
	if output != "Error: something went wrong" {
		t.Errorf("Expected 'Error: something went wrong', got '%s'", output)
	}

	// Verify it is NOT valid JSON (it's human-readable)
	var jsonCheck map[string]interface{}
	if json.Unmarshal(stderr.Bytes(), &jsonCheck) == nil {
		t.Error("Expected human-mode error to NOT be valid JSON")
	}
}

func TestRootFormatter_Print_QuietModeSuppressesOutput(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{QuietMode: true}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	err := formatter.Print("should not appear")
	if err != nil {
		t.Fatalf("Print failed: %v", err)
	}

	if writer.stdout.Len() != 0 {
		t.Errorf("Expected no output in quiet mode, got: %q", writer.stdout.String())
	}
}

func TestRootFormatter_PrintError_NotSuppressedByQuietMode(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{QuietMode: true}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	err := formatter.PrintError(errors.New("visible error"))
	if err != nil {
		t.Fatalf("PrintError failed: %v", err)
	}

	if writer.stderr.Len() == 0 {
		t.Error("Expected error output even in quiet mode, got nothing")
	}
	if !strings.Contains(writer.stderr.String(), "visible error") {
		t.Errorf("Expected stderr to contain 'visible error', got: %q", writer.stderr.String())
	}
}

func TestRootFormatter_IsQuiet(t *testing.T) {
	writer := NewMockWriter()

	t.Run("true when QuietMode set", func(t *testing.T) {
		f := &flags.GlobalFlags{QuietMode: true}
		formatter := NewRootFormatter(loggerRefForTest(t), writer, f)
		if !formatter.IsQuiet() {
			t.Error("Expected IsQuiet() to return true")
		}
	})

	t.Run("false when QuietMode not set", func(t *testing.T) {
		f := &flags.GlobalFlags{QuietMode: false}
		formatter := NewRootFormatter(loggerRefForTest(t), writer, f)
		if formatter.IsQuiet() {
			t.Error("Expected IsQuiet() to return false")
		}
	})
}

func TestRootFormatter_Print_QuietAndJSONCombination(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{QuietMode: true, OutputInJson: true}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	err := formatter.Print(map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("Print failed: %v", err)
	}

	if writer.stdout.Len() != 0 {
		t.Errorf("Expected no output with --quiet --json, got: %q", writer.stdout.String())
	}
}

func TestRootFormatter_Print_NormalModeProducesOutput(t *testing.T) {
	writer := NewMockWriter()
	f := &flags.GlobalFlags{QuietMode: false}
	formatter := NewRootFormatter(loggerRefForTest(t), writer, f)

	err := formatter.Print("hello world")
	if err != nil {
		t.Fatalf("Print failed: %v", err)
	}

	if writer.stdout.Len() == 0 {
		t.Error("Expected output in normal mode, got nothing")
	}
	if !strings.Contains(writer.stdout.String(), "hello world") {
		t.Errorf("Expected stdout to contain 'hello world', got: %q", writer.stdout.String())
	}
}
