package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/portpowered/portos-cli/internal/clidocs"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// GroupCommand is the parent of the group subcommands.
type GroupCommand struct{}

// NewGroupCommand returns a new GroupCommand.
func NewGroupCommand() *GroupCommand {
	return &GroupCommand{}
}

// Generate returns the cobra command for group.
func (c *GroupCommand) Generate() *cobra.Command {
	doc, _ := clidocs.Command(clidocs.GroupID)
	cmd := &cobra.Command{
		Short: doc.Summary,
		Long:  doc.Description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.Example = formatGeneratedExamples(doc.Examples)
	return cmd
}

// GroupListCommand wraps the group list subcommand.
type GroupListCommand struct {
	command *commands.GroupCommand
}

// NewGroupListCommand returns a new GroupListCommand.
func NewGroupListCommand(command *commands.GroupCommand) *GroupListCommand {
	return &GroupListCommand{command: command}
}

// Generate returns the cobra command for group list.
func (c *GroupListCommand) Generate() *cobra.Command {
	doc, _ := clidocs.Command(clidocs.GroupListID)
	cmd := &cobra.Command{
		Use:   "list",
		Short: doc.Summary,
		Long:  doc.Description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			maxResults, _ := cmd.Flags().GetString("max-results")
			nextToken, _ := cmd.Flags().GetString("next-token")
			expand, _ := cmd.Flags().GetString("expand")
			params := commands.GroupListParams{}
			if maxResults != "" {
				parsed, err := strconv.Atoi(maxResults)
				if err != nil || parsed <= 0 {
					return fmt.Errorf("--max-results must be a positive integer (got %q)", maxResults)
				}
				params.MaxResults = &parsed
			}
			if nextToken != "" {
				params.NextToken = &nextToken
			}
			for _, token := range strings.Split(expand, ",") {
				if token = strings.TrimSpace(token); token != "" {
					params.Expand = append(params.Expand, token)
				}
			}
			return c.command.List(cmd.Context(), params)
		},
	}
	cmd.Example = formatGeneratedExamples(doc.Examples)
	addGeneratedStringFlag(cmd, doc, "max-results", "", "Maximum number of results to return")
	addGeneratedStringFlag(cmd, doc, "next-token", "", "Token for pagination")
	addGeneratedStringFlag(cmd, doc, "expand", "", "Expansion tokens, comma separated, such as interfaces.attributes")
	return cmd
}
