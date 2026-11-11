package cli

import (
	"fmt"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// RulesCommand is the parent command group for access control rule operations.
type RulesCommand struct{}

// NewRulesCommand creates a new RulesCommand.
func NewRulesCommand() *RulesCommand {
	return &RulesCommand{}
}

// Generate returns the cobra command for the rules group.
func (c *RulesCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short: "Manage access control rules",
		Long: `Manage access control rules in Port OS.

Access control rules define who can perform what operations on which resources.
Available subcommands:
  list    - List all access control rules
  create  - Create a new access control rule
  delete  - Delete an access control rule by ID
  establish - Grant, revoke, check or list plugin establish access (operators)`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
}

// ListRulesCommand wraps the rules list subcommand.
type ListRulesCommand struct {
	command *commands.RulesCommand
}

// NewListRulesCommand returns a new ListRulesCommand.
func NewListRulesCommand(command *commands.RulesCommand) *ListRulesCommand {
	return &ListRulesCommand{command: command}
}

// Generate returns the cobra command for list rules.
func (c *ListRulesCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Short: "List access control rules",
		Long:  "List all access control rules for the current user",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			owner, _ := cmd.Flags().GetString("owner")
			maxResults, _ := cmd.Flags().GetInt("max-results")
			nextToken, _ := cmd.Flags().GetString("next-token")

			params := commands.ListRulesParams{
				Owner:     owner,
				NextToken: nextToken,
			}
			if cmd.Flags().Changed("max-results") {
				params.MaxResults = &maxResults
			}

			return c.command.List(ctx, params)
		},
	}
	cmd.Flags().String("owner", "", "Filter rules by owner")
	cmd.Flags().Int("max-results", 0, "Maximum number of results to return")
	cmd.Flags().String("next-token", "", "Pagination token for next page of results")
	return cmd
}

// CreateRuleCommand wraps the rules create subcommand.
type CreateRuleCommand struct {
	command *commands.RulesCommand
}

// NewCreateRuleCommand returns a new CreateRuleCommand.
func NewCreateRuleCommand(command *commands.RulesCommand) *CreateRuleCommand {
	return &CreateRuleCommand{command: command}
}

// Generate returns the cobra command for create rule.
func (c *CreateRuleCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Short: "Create an access control rule",
		Long:  "Create a new access control rule granting or denying an identity permission to perform an operation on a resource",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			resource, _ := cmd.Flags().GetString("resource")
			operation, _ := cmd.Flags().GetString("operation")
			identity, _ := cmd.Flags().GetString("identity")
			effect, _ := cmd.Flags().GetString("effect")
			grant, _ := cmd.Flags().GetString("grant")

			// --grant is an alias for --effect; --grant takes precedence if both set
			if grant != "" {
				effect = grant
			}

			if resource == "" {
				return fmt.Errorf("--resource is required")
			}
			if operation == "" {
				return fmt.Errorf("--operation is required")
			}
			if identity == "" {
				return fmt.Errorf("--identity is required")
			}

			params := commands.CreateRuleParams{
				Resource:  resource,
				Operation: operation,
				Identity:  identity,
				Effect:    effect,
			}
			return c.command.Create(ctx, params)
		},
	}
	cmd.Flags().String("resource", "", "Resource to grant/deny access to (required)")
	cmd.Flags().String("operation", "", "Operation to grant/deny (required)")
	cmd.Flags().String("identity", "", "Identity to grant/deny access for (required)")
	cmd.Flags().String("effect", "ALLOW", "Effect of the rule: ALLOW or DENY")
	cmd.Flags().String("grant", "", "Alias for --effect (ALLOW or DENY)")
	return cmd
}

// DeleteRuleCommand wraps the rules delete subcommand.
type DeleteRuleCommand struct {
	command *commands.RulesCommand
}

// NewDeleteRuleCommand returns a new DeleteRuleCommand.
func NewDeleteRuleCommand(command *commands.RulesCommand) *DeleteRuleCommand {
	return &DeleteRuleCommand{command: command}
}

// Generate returns the cobra command for delete rule.
func (c *DeleteRuleCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete <rule-id>",
		Short: "Delete an access control rule",
		Long:  "Delete an access control rule by its ID",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			params := commands.DeleteRuleParams{
				RuleID: args[0],
			}
			return c.command.Delete(ctx, params)
		},
	}
	return cmd
}
