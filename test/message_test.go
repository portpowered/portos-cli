package test

import (
	"context"
	"testing"

	"github.com/portpowered/portos-cli/internal/wire"
)

func TestSendSuccess(t *testing.T) {
	cli, err := wire.InitializeCLI()
	if err != nil {
		t.Fatalf("failed to initialize CLI: %v", err)
	}
	rootCmd := cli.Generate()
	rootCmd.ExecuteContext(context.Background())
}
