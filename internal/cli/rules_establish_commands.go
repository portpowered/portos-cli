package cli

import (
	"context"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// EstablishGroupCommand is the "rules establish" group.
type EstablishGroupCommand struct{}

// NewEstablishGroupCommand returns the establish group command.
func NewEstablishGroupCommand() *EstablishGroupCommand { return &EstablishGroupCommand{} }

// Generate returns the cobra command for the establish group.
func (c *EstablishGroupCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short: "Manage plugin establish access rules",
		Long: `Grant, revoke, check or list the rules that let a principal establish a plugin auth link.

Every subcommand requires --environment, the name of an entry under environments in the CLI config.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
}

// establishOperation runs one establish operation with the parsed flags.
type establishOperation func(*commands.EstablishCommand) func(context.Context, commands.EstablishParams) error

// establishSubcommand wraps one establish operation as a cobra command.
type establishSubcommand struct {
	short   string
	long    string
	write   bool
	run     establishOperation
	command *commands.EstablishCommand
}

// The four subcommands are distinct types so Wire can provide each one.
type (
	EstablishGrantCommand  struct{ *establishSubcommand }
	EstablishRevokeCommand struct{ *establishSubcommand }
	EstablishCheckCommand  struct{ *establishSubcommand }
	EstablishListCommand   struct{ *establishSubcommand }
)

// NewEstablishGrantCommand returns "rules establish grant".
func NewEstablishGrantCommand(command *commands.EstablishCommand) *EstablishGrantCommand {
	return &EstablishGrantCommand{&establishSubcommand{
		short: "Grant establish access",
		long:  "Create an ALLOW rule letting the principal establish the plugin link, then print the evaluate readback.",
		write: true, command: command,
		run: func(c *commands.EstablishCommand) func(context.Context, commands.EstablishParams) error {
			return c.Grant
		},
	}}
}

// NewEstablishRevokeCommand returns "rules establish revoke".
func NewEstablishRevokeCommand(command *commands.EstablishCommand) *EstablishRevokeCommand {
	return &EstablishRevokeCommand{&establishSubcommand{
		short: "Revoke establish access",
		long:  "Delete every matching ALLOW rule the operator can see, then read back. Fails and lists the rule IDs if permission is still ALLOW.",
		write: true, command: command,
		run: func(c *commands.EstablishCommand) func(context.Context, commands.EstablishParams) error {
			return c.Revoke
		},
	}}
}

// NewEstablishCheckCommand returns "rules establish check".
func NewEstablishCheckCommand(command *commands.EstablishCommand) *EstablishCheckCommand {
	return &EstablishCheckCommand{&establishSubcommand{
		short:   "Check establish access",
		long:    "Evaluate whether the principal may establish the plugin link and print the result.",
		command: command,
		run: func(c *commands.EstablishCommand) func(context.Context, commands.EstablishParams) error {
			return c.Check
		},
	}}
}

// NewEstablishListCommand returns "rules establish list".
func NewEstablishListCommand(command *commands.EstablishCommand) *EstablishListCommand {
	return &EstablishListCommand{&establishSubcommand{
		short:   "List establish rules",
		long:    "List the rules that govern establishing the plugin link.",
		command: command,
		run: func(c *commands.EstablishCommand) func(context.Context, commands.EstablishParams) error {
			return c.List
		},
	}}
}

// Generate returns the cobra command for the establish operation.
func (c *establishSubcommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Short: c.short,
		Long:  c.long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			flags := cmd.Flags()
			get := func(name string) string { v, _ := flags.GetString(name); return v }
			params := commands.EstablishParams{
				Environment:     get("environment"),
				Plugin:          get("plugin"),
				AuthVariant:     get("auth-variant"),
				Principal:       get("principal"),
				Regionalization: get("regionalization"),
			}
			if c.write {
				params.DryRun, _ = flags.GetBool("dry-run")
				params.Reason = get("reason")
			}
			return c.run(c.command)(cmd.Context(), params)
		},
	}
	flags := cmd.Flags()
	flags.String("environment", "", "Name of an entry in the CLI config environments map; its API base URL is used (required)")
	flags.String("plugin", "", "Plugin ID whose access is being established (required)")
	flags.String("auth-variant", "", "Auth variant the rule applies to (required)")
	flags.String("principal", "", "Principal the rule applies to (required)")
	flags.String("regionalization", "", "Regionalization the rule applies to; * means unconstrained (no REGIONALIZATION condition), the same as omitting it")
	if c.write {
		flags.Bool("dry-run", false, "Print the exact rule JSON and the evaluate request without writing")
		flags.String("reason", "", "Reason recorded in the audit log for this change")
	}
	return cmd
}
