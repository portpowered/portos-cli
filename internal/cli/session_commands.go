package cli

import (
	"time"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/spf13/cobra"
)

// SessionGroupCommand is the `port session` group.
type SessionGroupCommand struct{}

// NewSessionGroupCommand creates the session group.
func NewSessionGroupCommand() *SessionGroupCommand { return &SessionGroupCommand{} }

// Generate returns the cobra command for the session group.
func (c *SessionGroupCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short: "Hold an endpoint through the local session host",
		Long: `Hold an endpoint through the local session host.

A hold keeps a robot's remote-control session open between commands, so an
agent can send move after move without a start-session and end-session each
time. The hold prints a lease; pass it as --lease or PORTOS_LEASE.`,
	}
}

// SessionHoldCommand wraps `port session hold`.
type SessionHoldCommand struct {
	command *commands.SessionCommand
}

// NewSessionHoldCommand creates the hold command wrapper.
func NewSessionHoldCommand(command *commands.SessionCommand) *SessionHoldCommand {
	return &SessionHoldCommand{command: command}
}

// Generate returns the cobra command for hold.
func (c *SessionHoldCommand) Generate() *cobra.Command {
	var motion, camera bool
	var deadMan time.Duration
	cmd := &cobra.Command{
		Short: "Open a hold on an endpoint and print its lease",
		Long: `Start the session host if needed, open a hold on the endpoint and print
{holdId, lease, endpointId, kinds, pose}.

The host ends the hold, sending the robot home, when no command arrives for
--dead-man, when the machine sleeps, or when the robot leaves remote control.`,
		Example: `  port session hold ep/0abc --motion --dead-man 10m`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.Hold(cmd.Context(), commands.HoldParams{EndpointID: args[0], Motion: motion, Camera: camera, DeadMan: deadMan})
		},
	}
	cmd.Flags().BoolVar(&motion, "motion", false, "Hold the endpoint's remote-control session")
	cmd.Flags().BoolVar(&camera, "camera", false, "Hold the endpoint's camera session, so snapshots and play-audio under the lease skip opening a new one")
	cmd.Flags().DurationVar(&deadMan, "dead-man", 0, "End the hold after this long without a command (default 10m)")
	return cmd
}

// SessionStatusCommand wraps `port session status`.
type SessionStatusCommand struct {
	command *commands.SessionCommand
}

// NewSessionStatusCommand creates the status command wrapper.
func NewSessionStatusCommand(command *commands.SessionCommand) *SessionStatusCommand {
	return &SessionStatusCommand{command: command}
}

// Generate returns the cobra command for status.
func (c *SessionStatusCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short:   "Show the holds, with motion and camera state and how ended ones ended",
		Example: `  port session status`,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			return c.command.Status(cmd.Context(), id)
		},
	}
}

// SessionReleaseCommand wraps `port session release`.
type SessionReleaseCommand struct {
	command *commands.SessionCommand
}

// NewSessionReleaseCommand creates the release command wrapper.
func NewSessionReleaseCommand(command *commands.SessionCommand) *SessionReleaseCommand {
	return &SessionReleaseCommand{command: command}
}

// Generate returns the cobra command for release.
func (c *SessionReleaseCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Short:   "Release a hold and send the robot home",
		Example: `  port session release hold-1`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.Release(cmd.Context(), args[0])
		},
	}
}
