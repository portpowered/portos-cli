package output

import (
	"io"
)

// Formatter handles output formatting for CLI commands
type Formatter interface {
	// Print formats and outputs data to stdout
	Print(data interface{}) error

	// PrintError formats and outputs error data to stderr
	PrintError(err error) error

	// IsJSON returns true if formatter is configured for JSON output
	IsJSON() bool

	// IsQuiet returns true if quiet mode is enabled.
	// Note: Print() centrally suppresses all output in quiet mode, so commands do NOT
	// need to check IsQuiet() before calling Print(). This method is exposed for cases
	// where commands need to vary behavior beyond output suppression (e.g., skipping
	// expensive formatting or side effects that only matter when output is visible).
	IsQuiet() bool
}

// Writer provides the output destinations
type Writer interface {
	Stdout() io.Writer
	Stderr() io.Writer
}
