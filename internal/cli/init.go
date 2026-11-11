package cli

import (
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// InitCommand wraps the init command implementation.
type InitCommand struct {
	command *commands.InitCommand
}

// NewInitCommand returns a new InitCommand.
func NewInitCommand(command *commands.InitCommand) *InitCommand {
	return &InitCommand{command: command}
}

// Generate returns the cobra command for init.
func (c *InitCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize Port OS CLI configuration",
		Long: `Initialize the Port OS CLI by creating the .portos directory and configuration file.
This command sets up the default configuration for the CLI.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			apiURL, _ := cmd.Flags().GetString("api-url")
			authURL, _ := cmd.Flags().GetString("auth-url")
			return c.command.Run(apiURL, authURL)
		},
	}
	cmd.Flags().String("api-url", "", "API base URL to configure.")
	cmd.Flags().String("auth-url", "", "Auth base URL to configure.")
	return cmd
}
