package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/portpowered/portos-cli/internal/clidocs"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

type EndpointCommand struct {
}

func NewEndpointCommand() *EndpointCommand {
	return &EndpointCommand{}
}

func (c *EndpointCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Aliases: []string{"ep"},
		Short:   "Endpoint management commands",
		Long:    "List and show endpoint information",
		Example: `  # List all your endpoints
  port endpoint list

  # Show details for a specific endpoint
  port endpoint show port1/systems/zero/endpoints/abc-123

  # List endpoints as JSON for scripting
  port endpoint list --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
}

// EndpointListCommand wraps the endpoint list subcommand.
type EndpointListCommand struct {
	command *commands.EndpointCommand
}

// NewEndpointListCommand returns a new EndpointListCommand.
func NewEndpointListCommand(command *commands.EndpointCommand) *EndpointListCommand {
	return &EndpointListCommand{command: command}
}

// Generate returns the cobra command for endpoint list.
func (c *EndpointListCommand) Generate() *cobra.Command {
	doc, _ := clidocs.Command(clidocs.EndpointListID)
	cmd := &cobra.Command{
		Use:   "list",
		Short: doc.Summary,
		Long:  doc.Description,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			owner, _ := cmd.Flags().GetString("owner")
			maxResults, _ := cmd.Flags().GetString("max-results")
			nextToken, _ := cmd.Flags().GetString("next-token")
			typeFilter, _ := cmd.Flags().GetString("type")
			serialNumber, _ := cmd.Flags().GetString("serial-number")
			var maxResultsInt *int
			if maxResults != "" {
				parsed, err := strconv.Atoi(maxResults)
				if err != nil {
					return fmt.Errorf("invalid max-results value: %w", err)
				}
				if parsed <= 0 {
					return fmt.Errorf("--max-results must be a positive integer (got %d)", parsed)
				}
				maxResultsInt = &parsed
			}
			var nextTokenPtr *string
			if nextToken != "" {
				nextTokenPtr = &nextToken
			}
			var typePtr *string
			if typeFilter != "" {
				typePtr = &typeFilter
			}
			var serialNumberPtr *string
			if serialNumber != "" {
				serialNumberPtr = &serialNumber
			}
			params := commands.ListParams{
				Owner:        owner,
				MaxResults:   maxResultsInt,
				NextToken:    nextTokenPtr,
				Type:         typePtr,
				SerialNumber: serialNumberPtr,
			}
			return c.command.List(ctx, params)
		},
	}
	cmd.Example = formatGeneratedExamples(doc.Examples)
	addGeneratedStringFlag(cmd, doc, "owner", "~caller", "Filter endpoints by owner")
	addGeneratedStringFlag(cmd, doc, "max-results", "", "Maximum number of results to return")
	addGeneratedStringFlag(cmd, doc, "next-token", "", "Token for pagination")
	addGeneratedStringFlag(cmd, doc, "type", "", "Filter endpoints by primary type")
	addGeneratedStringFlag(cmd, doc, "serial-number", "", "Filter endpoints by serial number")
	return cmd
}

func addGeneratedStringFlag(cmd *cobra.Command, doc clidocs.CommandDoc, name string, fallbackDefault string, fallbackDescription string) {
	flag, ok := doc.Flag(name)
	if !ok {
		cmd.Flags().String(name, fallbackDefault, fallbackDescription)
		return
	}
	cmd.Flags().String(flag.Name, flag.Default, flag.Description)
}

func formatGeneratedExamples(examples []clidocs.ExampleDoc) string {
	if len(examples) == 0 {
		return ""
	}

	lines := make([]string, 0, len(examples)*3)
	for _, example := range examples {
		if example.Description != "" {
			lines = append(lines, "  # "+example.Description)
		}
		if example.Invocation != "" {
			lines = append(lines, "  "+example.Invocation)
		}
		if example.Output != "" {
			lines = append(lines, example.Output)
		}
	}
	return strings.Join(lines, "\n")
}

// EndpointShowCommand wraps the endpoint show subcommand.
type EndpointShowCommand struct {
	command *commands.EndpointCommand
}

// NewEndpointShowCommand returns a new EndpointShowCommand.
func NewEndpointShowCommand(command *commands.EndpointCommand) *EndpointShowCommand {
	return &EndpointShowCommand{command: command}
}

// Generate returns the cobra command for endpoint show.
func (c *EndpointShowCommand) Generate() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [endpoint-id]",
		Short: "Show detailed endpoint information",
		Long:  "Display detailed information about a specific endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			owner, _ := cmd.Flags().GetString("owner")
			params := commands.ShowParams{
				EndpointID: args[0],
				Owner:      owner,
			}
			return c.command.Show(ctx, params)
		},
	}
	cmd.Flags().String("owner", "~caller", "Filter endpoints by owner")
	return cmd
}

// EndpointAttributeCommand wraps the endpoint attribute subcommand.
type EndpointAttributeCommand struct {
	command *commands.EndpointCommand
}

// NewEndpointAttributeCommand returns a new EndpointAttributeCommand.
func NewEndpointAttributeCommand(command *commands.EndpointCommand) *EndpointAttributeCommand {
	return &EndpointAttributeCommand{command: command}
}

// Generate returns the cobra command for endpoint attribute.
func (c *EndpointAttributeCommand) Generate() *cobra.Command {
	doc, _ := clidocs.Command(clidocs.EndpointAttributeID)
	cmd := &cobra.Command{
		Use:   "attribute <endpoint-id> <interface>/<attribute>",
		Short: doc.Summary,
		Long:  doc.Description,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return c.command.Attribute(cmd.Context(), commands.AttributeParams{
				EndpointID: args[0],
				Attribute:  args[1],
				Force:      force,
			})
		},
	}
	cmd.Example = formatGeneratedExamples(doc.Examples)
	cmd.Flags().Bool("force", false, "Read the value from the device instead of stored state")
	return cmd
}
