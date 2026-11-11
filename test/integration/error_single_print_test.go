package integration

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
)

// countOccurrences returns the number of non-overlapping occurrences of substr in s.
func countOccurrences(s, substr string) int {
	return strings.Count(s, substr)
}

// runCLICommand initializes a mock CLI, runs the given args, and returns the
// CLIApp, testWriter, and the error from ExecuteContext.
func runCLICommand(t *testing.T, args ...string) (*testWriter, error) {
	t.Helper()
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}
	defer os.Chdir(originalDir)
	configDir := setDirectory(t)

	tw := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, tw)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.SetOut(tw.Stdout())
	rootCmd.SetErr(tw.Stderr())
	rootCmd.SetArgs(ArgsWithConfigDir(configDir, args...))

	ctx := context.Background()
	execErr := rootCmd.ExecuteContext(ctx)

	// Simulate main()'s behavior: route errors through HandleError
	if execErr != nil {
		cli.HandleError(execErr)
	}

	return tw, execErr
}

// TestUnknownCommandErrorPrintsOnce verifies that running an unknown command
// (e.g., 'port nonexistent') prints the error message exactly once to stderr.
func TestUnknownCommandErrorPrintsOnce(t *testing.T) {
	tw, err := runCLICommand(t, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown command, but got nil")
	}

	stderr := tw.StderrString()
	errText := "unknown command"

	count := countOccurrences(stderr, errText)
	if count != 1 {
		t.Errorf("expected %q to appear exactly once in stderr, but found %d occurrences.\nStderr:\n%s", errText, count, stderr)
	}
}

// TestTypoSuggestionErrorPrintsOnce verifies that a typo command
// (e.g., 'port endpont') prints the suggestion exactly once to stderr.
func TestTypoSuggestionErrorPrintsOnce(t *testing.T) {
	tw, err := runCLICommand(t, "endpont")
	if err == nil {
		t.Fatal("expected error for typo command, but got nil")
	}

	stderr := tw.StderrString()

	// The unknown command error should appear exactly once
	unknownCount := countOccurrences(stderr, "unknown command")
	if unknownCount != 1 {
		t.Errorf("expected 'unknown command' to appear exactly once in stderr, but found %d occurrences.\nStderr:\n%s", unknownCount, stderr)
	}

	// The "Did you mean" suggestion should appear exactly once
	suggestionCount := countOccurrences(stderr, "Did you mean")
	if suggestionCount != 1 {
		t.Errorf("expected 'Did you mean' to appear exactly once in stderr, but found %d occurrences.\nStderr:\n%s", suggestionCount, stderr)
	}
}

// TestAuthErrorPrintsOnce verifies that an auth-related error (missing token)
// prints exactly once to stderr.
func TestAuthErrorPrintsOnce(t *testing.T) {
	// Don't set any token env vars — this should cause an auth error
	tw, err := runCLICommand(t, "endpoint", "list")
	if err == nil {
		t.Fatal("expected error for missing auth token, but got nil")
	}

	stderr := tw.StderrString()

	// The error should appear exactly once — count "Error:" prefix occurrences
	errorPrefixCount := countOccurrences(stderr, "Error:")
	if errorPrefixCount != 1 {
		t.Errorf("expected 'Error:' to appear exactly once in stderr, but found %d occurrences.\nStderr:\n%s", errorPrefixCount, stderr)
	}
}

// TestArgValidationErrorPrintsOnce verifies that an argument validation error
// (e.g., missing required arg) prints exactly once to stderr.
func TestArgValidationErrorPrintsOnce(t *testing.T) {
	tw, err := runCLICommand(t, "auth", "set-token")
	if err == nil {
		t.Fatal("expected error for missing arg, but got nil")
	}

	stderr := tw.StderrString()

	// The error about args should appear exactly once
	errorPrefixCount := countOccurrences(stderr, "Error:")
	if errorPrefixCount != 1 {
		t.Errorf("expected 'Error:' to appear exactly once in stderr, but found %d occurrences.\nStderr:\n%s", errorPrefixCount, stderr)
	}
}
