package cli

import (
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// StatusCommand wraps the status command implementation.
type StatusCommand struct {
	command *commands.StatusCommand
}

// NewStatusCommand returns a new StatusCommand.
func NewStatusCommand(command *commands.StatusCommand) *StatusCommand {
	return &StatusCommand{command: command}
}

// Generate returns the cobra command for status.
func (c *StatusCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check server connectivity",
		Long: `Check connectivity to the configured PortOS backend server.

Sends a probe to the server's /ping endpoint and reports whether the server
is reachable and is a valid PortOS backend. Use this to verify your API URL
configuration is correct.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.Run(cmd.Context())
		},
	}
}
