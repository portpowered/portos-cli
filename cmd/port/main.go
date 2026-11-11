package main

import (
	"fmt"
	"os"

	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/wire"
)

// version is set at build time via ldflags: -X main.version=<value>
var version = "dev"

func main() {
	// Initialize CLI with wire dependency injection
	var err error
	cli, err := wire.InitializeCLI()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to initialize CLI: %v\n", err)
		os.Exit(1)
	}

	// Generate the root command from injected dependencies
	rootCmd := cli.Generate()
	rootCmd.Version = version

	classify := exitcode.TrackUsageErrors(rootCmd)
	if err := classify(rootCmd.Execute()); err != nil {
		// Route all errors through the centralized handler (SilenceErrors suppresses Cobra's own print)
		cli.HandleError(err)
		os.Exit(exitcode.Code(err))
	}
}
