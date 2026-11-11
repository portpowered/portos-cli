package output

import (
	"io"
	"os"
)

// CLIWriter implements the Writer interface using stdout and stderr

type CLIWriter struct{}

func (w *CLIWriter) Stdout() io.Writer {
	return os.Stdout
}

func (w *CLIWriter) Stderr() io.Writer {
	return os.Stderr
}

// NewCLIWriter creates a new CLI writer
func NewCLIWriter() Writer {
	return &CLIWriter{}
}
