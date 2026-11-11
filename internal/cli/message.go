package cli

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// MessageCommand wraps the message command implementation (send messages and snapshot).
type MessageCommand struct {
	command *commands.MessageCommand
}

// NewMessageCommand returns a new MessageCommand.
func NewMessageCommand(command *commands.MessageCommand) *MessageCommand {
	return &MessageCommand{command: command}
}

// Generate returns the cobra command for message (and snapshot usage).
func (c *MessageCommand) Generate() *cobra.Command {
	command := &cobra.Command{
		Aliases: []string{"msg"},
		Short:   "Send messages to endpoints or capture snapshots",
		Long: `Send device messages to endpoints.

DEVICE COMMANDS (primary usage):
  port message <command> <endpoint-id> [args...]

  The message command accepts any device command name — not just the ones
  listed under "Available Commands" below. Commands like turn-on, set-brightness,
  lock, and hundreds more are all valid.

  Command can be a simple name (e.g., "turn-on") or a namespaced path
  (e.g., "power/turn-on"). Arguments can be key-value pairs or JSON.

UTILITY SUBCOMMANDS:
  The "Available Commands" section (snapshot, record, move) lists utility
  subcommands that provide specialized functionality beyond simple message
  sending — such as capturing a single video frame or recording a stream.
  These are separate from the device commands listed below.

` + buildSupportedCommandsSection() + `

  Note: Endpoints may support additional commands beyond those listed above.
  The supported commands shown here are known types from the API specification.`,
		Example: `  # Send a simple command to a device
  port message turn-on ep/0abc1234

  # Use a namespace path for the command
  port message power/turn-on ep/0abc1234

  # Pass arguments as key-value pairs
  port message set-brightness ep/0abc1234 brightness=50

  # Pass arguments as JSON
  port message set-target-temperature ep/0abc1234 '{"temperature": 22, "unit": "celsius"}'

  # Wait for the result and send the whole body as JSON
  port message move ep/0abc1234 --wait --body '{"velocity":0.2,"omega":0,"duration":5000}'

  # Use the 'msg' alias for brevity
  port msg lock ep/0abc1234`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			key, _ := cmd.Flags().GetString("idempotency-key")
			wait, _ := cmd.Flags().GetBool("wait")
			body, _ := cmd.Flags().GetString("body")
			params := commands.RunParams{
				IdempotencyKey: key,
				Wait:           wait,
				Body:           body,
				BodySet:        cmd.Flags().Changed("body"),
				Command:        args[0],
				EndpointID:     args[1],
				Args:           args[2:],
			}
			return c.command.Run(ctx, params)
		},
	}
	command.Flags().String("idempotency-key", "", "Stable key for this intended command; reuse it only for an identical retry")
	command.Flags().Bool("wait", false, "Wait for the final result (responseType SYNC); a failed request exits non-zero")
	command.Flags().String("body", "", "Whole message body as a JSON object; cannot be combined with key=value arguments")
	return command
}

// buildSupportedCommandsSection generates a help section listing available message
// types grouped by capability interface, sourced from the MessagesTypes registry.
func buildSupportedCommandsSection() string {
	// Group message types by namespace, filtering to only "in" or unset directionality
	// (i.e., commands that can be sent TO a device, not responses/events coming OUT).
	type entry struct {
		name        string
		description string
	}
	groups := make(map[string][]entry)

	for _, info := range api.MessagesTypes {
		if info.Directionality == "out" {
			continue
		}
		groups[info.Namespace] = append(groups[info.Namespace], entry{
			name:        info.Name,
			description: info.Description,
		})
	}

	// Sort namespaces for stable output
	namespaces := make([]string, 0, len(groups))
	for ns := range groups {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)

	var b strings.Builder
	b.WriteString("Supported commands:")

	for _, ns := range namespaces {
		entries := groups[ns]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].name < entries[j].name
		})

		// Extract friendly group name from the namespace path (last segment, title-cased)
		groupName := friendlyNamespaceName(path.Base(ns))
		fmt.Fprintf(&b, "\n\n  %s:", groupName)
		for _, e := range entries {
			fmt.Fprintf(&b, "\n    %-28s %s", e.name, e.description)
		}
	}

	return b.String()
}

// friendlyNamespaceName converts a kebab-case namespace segment to a title-case label.
// e.g. "media-playback" → "Media Playback"
func friendlyNamespaceName(segment string) string {
	words := strings.Split(segment, "-")
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
