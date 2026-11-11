package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
)

func TestVersionFlag(t *testing.T) {
	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.Version = "test-version"
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs([]string{"--version"})

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("--version flag returned error: %v", err)
	}

	output := testWriter.StdoutString()
	if !strings.Contains(output, "test-version") {
		t.Errorf("expected output to contain 'test-version', got: %s", output)
	}
}

func TestVersionFlagShorthand(t *testing.T) {
	testWriter := NewTestWriter()
	cli, err := wire.InitializeMockCLI(nil, testWriter)
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}

	rootCmd := cli.Generate()
	rootCmd.Version = "test-version"
	rootCmd.SetOut(testWriter.Stdout())
	rootCmd.SetErr(testWriter.Stderr())
	rootCmd.SetArgs([]string{"-V"})

	ctx := context.Background()
	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("-V flag returned error: %v", err)
	}

	output := testWriter.StdoutString()
	if !strings.Contains(output, "test-version") {
		t.Errorf("expected output to contain 'test-version', got: %s", output)
	}
}
