package output

import (
	"encoding/json"
	"errors"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

// JSONFormatter handles JSON output formatting
type JSONFormatter struct {
	writer Writer
	logger *logging.LoggerRef
}

// NewJSONFormatter creates a new JSON formatter
func NewJSONFormatter(writer Writer, logger *logging.LoggerRef) *JSONFormatter {
	return &JSONFormatter{
		writer: writer,
		logger: logger,
	}
}

// Print outputs data as JSON to stdout
func (f *JSONFormatter) Print(data interface{}) error {
	encoder := json.NewEncoder(f.writer.Stdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(data)
}

// PrintError outputs error as structured JSON to stderr in the form:
//
//	{"error": {"message": "...", "type": "...", "status": N}}
//
// The "status" field is included only when the error originates from an HTTP response.
func (f *JSONFormatter) PrintError(err error) error {
	errorObj := map[string]interface{}{
		"message": err.Error(),
		"type":    "cli_error",
	}

	var apiErr *apiclient.APIError
	if errors.As(err, &apiErr) {
		errorObj["status"] = apiErr.StatusCode
		errorObj["type"] = "api_error"
		if apiErr.Code != nil {
			errorObj["code"] = *apiErr.Code
		}
		if apiErr.Family != nil {
			errorObj["family"] = *apiErr.Family
		}
		if apiErr.Message != nil {
			errorObj["message"] = *apiErr.Message
		}
		if apiErr.StatusCode == 404 {
			errorObj["hint"] = "The filesystem may not exist. Run 'port files list-filesystems' to check, or 'port files create-filesystem' to create one."
		}
	}

	errorData := map[string]interface{}{
		"error": errorObj,
	}

	encoder := json.NewEncoder(f.writer.Stderr())
	encoder.SetIndent("", "  ")
	return encoder.Encode(errorData)
}

// IsJSON returns true for JSON formatter
func (f *JSONFormatter) IsJSON() bool {
	return true
}

// IsQuiet returns false for the JSON formatter standalone — quiet mode is managed by RootFormatter.
func (f *JSONFormatter) IsQuiet() bool {
	return false
}
