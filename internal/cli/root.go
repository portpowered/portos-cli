package cli

import (
	"github.com/portpowered/portos-cli/internal/flags"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/spf13/cobra"
)

// Command is implemented by CLI command groups that produce a cobra command.
type Command interface {
	Generate() *cobra.Command
}

// RootCommand holds the root "port" command and its subcommand builders.
// Subcommands are wired in routes.go.
type RootCommand struct {
	LoggerRef *logging.LoggerRef
	Flags     *flags.GlobalFlags
}

// NewRootCommand constructs a RootCommand with the given dependencies.
func NewRootCommand(
	loggerRef *logging.LoggerRef,
	flags *flags.GlobalFlags,
) *RootCommand {
	return &RootCommand{
		LoggerRef: loggerRef,
		Flags:     flags,
	}
}

// Generate builds the full CLI tree (see routes.BuildRoot) and returns the root cobra command.
func (c *RootCommand) Generate() *cobra.Command {
	return c.makeRootCommand()
}

func (c *RootCommand) makeRootCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "port",
		Short: "Port OS CLI - Command-line interface for Port OS API",
		Long: `Port OS CLI is a command-line tool for interacting with the Port OS API.
Use port to control devices and manage device automation flows.

Getting started:
  1. port init              Initialize configuration
  2. port auth set-token    Authenticate (or use port auth login)
  3. port endpoint list     Start using the API

Alternatively, set the PORTOS_ACCESS_TOKEN environment variable to skip
auth setup entirely.`,
		SilenceErrors: true,
		SilenceUsage:  true,
		Example: `  # Getting started
  port init --api-url http://localhost:8080   # Initialize configuration
  port auth set-token <your-token>            # Set your access token
  port endpoint list                          # List your devices
  port message turn-on ep/0abc1234            # Send a command to a device`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			c.LoggerRef.Set(logging.ProvideLogger(c.Flags))
		},
		PersistentPostRun: func(cmd *cobra.Command, args []string) {
			_ = c.LoggerRef.Logger().Sync()
		},
	}
}
