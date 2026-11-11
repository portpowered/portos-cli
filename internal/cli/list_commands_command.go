package cli

import (
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// ListCommandsCliCommand wraps the list-commands command implementation.
type ListCommandsCliCommand struct {
	command *commands.MessageCommand
}

// NewListCommandsCliCommand returns a new ListCommandsCliCommand.
func NewListCommandsCliCommand(command *commands.MessageCommand) *ListCommandsCliCommand {
	return &ListCommandsCliCommand{command: command}
}

// Generate returns the cobra command for list-commands.
func (c *ListCommandsCliCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short: "List available message commands for an endpoint",
		Long: `List the message commands supported by a specific endpoint.

Queries the endpoint's capability interfaces and shows which commands
(e.g., turn-on, set-brightness, lock) can be sent to the device.

The output is grouped by capability interface (e.g., power, brightness).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.ListCommands(cmd.Context(), commands.ListCommandsParams{
				EndpointID: args[0],
			})
		},
	}
}
