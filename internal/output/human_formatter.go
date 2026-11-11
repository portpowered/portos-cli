package output

import (
	"encoding/json"
	"fmt"

	"github.com/portpowered/portos-cli/internal/logging"
)

// HumanFormatter handles human-readable output formatting
type HumanFormatter struct {
	writer Writer
	logger *logging.LoggerRef
}

// NewHumanFormatter creates a new human-readable formatter
func NewHumanFormatter(writer Writer, logger *logging.LoggerRef) *HumanFormatter {
	return &HumanFormatter{
		writer: writer,
		logger: logger,
	}
}

// Print outputs data in human-readable format to stdout
func (f *HumanFormatter) Print(data interface{}) error {
	// For human-readable output, we need to handle different data types
	// This is a basic implementation that can be extended for specific command types

	switch v := data.(type) {
	case string:
		_, err := fmt.Fprintln(f.writer.Stdout(), v)
		return err
	case fmt.Stringer:
		_, err := fmt.Fprintln(f.writer.Stdout(), v.String())
		return err
	default:
		// For complex types, fall back to JSON but without indentation for more compact output
		encoder := json.NewEncoder(f.writer.Stdout())
		return encoder.Encode(data)
	}
}

// PrintError outputs error in human-readable format to stderr
func (f *HumanFormatter) PrintError(err error) error {
	_, printErr := fmt.Fprintf(f.writer.Stderr(), "Error: %s\n", err.Error())
	return printErr
}

// IsJSON returns false for human formatter
func (f *HumanFormatter) IsJSON() bool {
	return false
}

// IsQuiet returns false for the human formatter standalone — quiet mode is managed by RootFormatter.
func (f *HumanFormatter) IsQuiet() bool {
	return false
}
