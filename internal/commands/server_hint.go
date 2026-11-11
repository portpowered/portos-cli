package commands

import (
	"fmt"
	"os"

	"github.com/portpowered/portos-cli/internal/config"
)

// printServerHintFromConfig prints the configured API URL to stderr as a diagnostic hint.
// This helps users identify when they are pointed at the wrong server.
// Best-effort: silently does nothing if config cannot be loaded.
func printServerHintFromConfig(configProvider *config.ManagerProvider) {
	manager, err := configProvider.GetManager()
	if err != nil {
		return
	}
	cfg, err := manager.LoadConfig()
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stderr, "(server: %s)\n", cfg.APIBaseURL)
}

func (c *EndpointCommand) printServerHint() {
	printServerHintFromConfig(c.configProvider)
}
