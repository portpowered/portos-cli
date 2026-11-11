package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// ServeCommand wraps the serve subcommand.
type ServeCommand struct {
	command *commands.ServeCommand
}

// NewServeCommand creates a new ServeCommand.
func NewServeCommand(command *commands.ServeCommand) *ServeCommand {
	return &ServeCommand{command: command}
}

// Generate returns the cobra command for serve.
func (c *ServeCommand) Generate() *cobra.Command {
	var endpointID string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a persistent WebSocket connection to Port OS",
		Long: `Start a persistent WebSocket connection to Port OS to receive dispatched events.

This command establishes and maintains a WebSocket connection to the backend,
printing received events to stdout. It is designed for use with systemd or
similar process managers for headless device integration.

The connection automatically handles ping/pong keepalive and will attempt
to reconnect on connection drops with exponential backoff.

Use Ctrl+C (SIGINT) or SIGTERM to gracefully shut down.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			params := commands.ServeParams{
				EndpointID: endpointID,
			}
			return c.command.Serve(ctx, params)
		},
	}

	cmd.Flags().StringVar(&endpointID, "endpoint-id", "", "Endpoint ID to scope events (optional)")

	return cmd
}
