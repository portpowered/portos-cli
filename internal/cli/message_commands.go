package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/spf13/cobra"
)

// MoveCliCommand wraps the move command implementation.
type MoveCliCommand struct {
	command *commands.MoveCommand
}

// NewMoveCliCommand returns a new MoveCliCommand.
func NewMoveCliCommand(command *commands.MoveCommand) *MoveCliCommand {
	return &MoveCliCommand{command: command}
}

// Generate returns the cobra command for move.
func (c *MoveCliCommand) Generate() *cobra.Command {
	command := &cobra.Command{
		Aliases: []string{"mv"},
		Short:   "Move a robot vacuum using human-readable instructions",
		Long: `Send movement instructions to a robot vacuum endpoint using the move grammar.

Instructions can specify direction and distance (forward, backward, left, right)
or rotation (rotate with degrees). Multiple instructions are comma-separated.

The command runs one remote-control session: start-session, one move per
instruction, then end-session. Each move is sent synchronously and its reply
(distanceMetres, turnedDegrees, pose) is printed. The run stops at the first
move that is not carried out (exit code 10), and the session is still ended.

Move grammar reference:
  forward <distance>     Move forward (e.g., forward 1m)
  backward <distance>    Move backward (e.g., backward 0.5m)
  left <distance>        Arc left (e.g., left 1m)
  right <distance>       Arc right (e.g., right 0.5m)
  rotate <degrees>       Rotate in place (e.g., rotate 90, rotate -45)

Distance units: m, meter, meters
Separate multiple instructions with commas.`,
		Example: `  # Move forward 1 meter
  port msg move ep/0abc "forward 1m"

  # Multi-step: forward, turn, forward
  port msg move ep/0abc "forward 1m, rotate 90, forward 0.5m"

  # Multiple arguments are joined
  port msg move ep/0abc "forward 1m" "rotate -90" "backward 0.5m"`,
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := commands.MoveParams{
				EndpointID:   args[0],
				Instructions: args[1:],
			}
			return c.command.Run(cmd.Context(), params)
		},
	}
	return command
}

// GenerateGoToPoint returns the cobra command for go-to-point, which shares the
// move command's implementation.
func (c *MoveCliCommand) GenerateGoToPoint() *cobra.Command {
	var frame string
	command := &cobra.Command{
		Short: "Send a robot vacuum to a map point by its own path planning",
		Long: `Send a robot vacuum to a point on its map, in metres, without cleaning.

Under a motion hold (pass the lease with --lease or PORTOS_LEASE) this is one
operation: the host ends the remote-control session, sends go-to-point, waits
for the robot to arrive, starts the session again so the hold is live for the
last metre, and prints the arrival pose. The robot stops near the point, not
always on it. The frame is the relativeToGroupRouteId the robot's pose reports;
under a hold it may be left out.

Without a hold the message goes to Port OS as is. It replies once the robot has
started travelling, the robot must not be under remote control, and --frame is
required.`,
		Example: `  # Cross the room under a hold, then print the arrival pose as JSON
  port msg go-to-point ep/0abc 1.5 0 --lease "$PORTOS_LEASE" --json

  # Name the frame explicitly; put -- before a negative coordinate
  port msg go-to-point ep/0abc --frame 12345/map:0 --lease "$PORTOS_LEASE" -- -2 3.25`,
		Args: cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			x, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return exitcode.NewInvalidArgument(fmt.Errorf("x %q is not a number of metres", args[1]))
			}
			y, err := strconv.ParseFloat(args[2], 64)
			if err != nil {
				return exitcode.NewInvalidArgument(fmt.Errorf("y %q is not a number of metres", args[2]))
			}
			return c.command.GoToPoint(cmd.Context(), commands.GoToPointParams{EndpointID: args[0], X: x, Y: y, Frame: frame})
		},
	}
	command.Flags().StringVar(&frame, "frame", "", "relativeToGroupRouteId of the frame the point uses (required without a hold)")
	return command
}

// GenerateStepPTZ returns the cobra command for step-ptz, which shares the move
// command's implementation.
func (c *MoveCliCommand) GenerateStepPTZ() *cobra.Command {
	var degrees float64
	command := &cobra.Command{
		Short: "Turn a held camera left, right, up or down by a step or by degrees",
		Long: `Turn a camera in the session of a camera hold, by one pan or tilt step or by
about --degrees. The camera rounds degrees to whole steps and stops at the end of
its range, so --degrees 360 turns it all the way to that end.

The reply says how far the camera turned and where it now points: pan in degrees
right of the left end and tilt in degrees up from the bottom end, once the camera
has been turned to an end in this session, and from the session start before
that. Turn to an end first (LEFT --degrees 360, DOWN --degrees 360) to know
exactly where the camera points.

Pass the hold's lease with --lease or PORTOS_LEASE. The host sends the turn in
the hold's own camera session, so there is no session ID to handle. Without a
live hold behind a lease the turn is refused with hold_lease_required (exit 13).
Take a snapshot after the turn to see the new view.`,
		Example: `  port msg step-ptz ep/0abc LEFT --degrees 360 --lease "$PORTOS_LEASE"
  port msg step-ptz ep/0abc UP --degrees 30 --lease "$PORTOS_LEASE"
  port msg step-ptz ep/0abc RIGHT --lease "$PORTOS_LEASE"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.StepPTZ(cmd.Context(), commands.StepPTZParams{EndpointID: args[0], Direction: args[1], Degrees: degrees})
		},
	}
	command.Flags().Float64Var(&degrees, "degrees", 0, "about how far to turn, 1 to 360; 360 turns to the end (default one step)")
	return command
}

// GenerateMovePTZ returns the cobra command for move-ptz, which shares the move
// command's implementation.
func (c *MoveCliCommand) GenerateMovePTZ() *cobra.Command {
	var speed, seconds float64
	command := &cobra.Command{
		Short: "Turn a held camera continuously left, right, up or down",
		Long: `Turn a camera continuously in the session of a camera hold, at --speed (a
fraction of full speed, default full) for --seconds.

With --seconds the reply comes once the camera has stopped and says how far it
turned and where it points; sub-second turns are fine. Without --seconds the reply
comes as soon as the camera turns and says the axis is still turning; it keeps
turning until stop-ptz, another move-ptz or step-ptz on that axis, or the end of
its range. A pan and a tilt can run at once, so LEFT and UP together turn the
camera diagonally. --seconds 60 turns to the end of the range in that direction.

Pass the hold's lease with --lease or PORTOS_LEASE. The host sends the turn in
the hold's own camera session, so there is no session ID to handle. Without a
live hold behind a lease the turn is refused with hold_lease_required (exit 13).`,
		Example: `  port msg move-ptz ep/0abc LEFT --seconds 60 --lease "$PORTOS_LEASE"
  port msg move-ptz ep/0abc RIGHT --seconds 0.5 --lease "$PORTOS_LEASE"
  port msg move-ptz ep/0abc UP --speed 0.5 --lease "$PORTOS_LEASE"`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.MovePTZ(cmd.Context(), commands.MovePTZParams{EndpointID: args[0], Direction: args[1], Speed: speed, Seconds: seconds})
		},
	}
	command.Flags().Float64Var(&speed, "speed", 0, "fraction of full speed, 0.1 to 1 (default full speed)")
	command.Flags().Float64Var(&seconds, "seconds", 0, "how long to turn, 0.1 to 60 (default until stop-ptz or the end of the range)")
	return command
}

// GenerateStopPTZ returns the cobra command for stop-ptz, which shares the move
// command's implementation.
func (c *MoveCliCommand) GenerateStopPTZ() *cobra.Command {
	var axis string
	command := &cobra.Command{
		Short: "Stop a held camera's continuous turn",
		Long: `Stop the continuous turn a move-ptz started on one axis, or on both without
--axis. The reply says how far the camera turned and where it now points.

Pass the hold's lease with --lease or PORTOS_LEASE.`,
		Example: `  port msg stop-ptz ep/0abc --lease "$PORTOS_LEASE"
  port msg stop-ptz ep/0abc --axis PAN --lease "$PORTOS_LEASE"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return c.command.StopPTZ(cmd.Context(), commands.StopPTZParams{EndpointID: args[0], Axis: axis})
		},
	}
	command.Flags().StringVar(&axis, "axis", "", "PAN or TILT (default both)")
	return command
}

// SnapshotCommand wraps the snapshot command implementation.
type SnapshotCommand struct {
	command *commands.SnapshotCommand
}

// NewSnapshotCommand returns a new SnapshotCommand.
func NewSnapshotCommand(command *commands.SnapshotCommand) *SnapshotCommand {
	return &SnapshotCommand{command: command}
}

// Generate returns the cobra command for snapshot.
func (c *SnapshotCommand) Generate() *cobra.Command {
	command := &cobra.Command{
		Aliases: []string{"snap"},
		Short:   "Capture snapshots from endpoints",
		Long:    `Attempts to take a snapshot image for an endpoint.`,
		Example: `port msg snap ep-123`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			outputPath, _ := cmd.Flags().GetString("output")
			relayOnly, _ := cmd.Flags().GetBool("relay-only")

			params := commands.SnapshotParams{
				EndpointID: args[0],
				OutputPath: outputPath,
				RelayOnly:  relayOnly,
			}
			return c.command.Run(ctx, params)
		},
	}

	command.Flags().StringP("output", "o", "", "Output path for the snapshot")
	command.Flags().Bool("relay-only", false, "Connect only through the Port OS relay (for networks without UDP)")

	return command
}

// PlayAudioCliCommand wraps the play-audio command implementation.
type PlayAudioCliCommand struct {
	command *commands.PlayAudioCommand
}

// NewPlayAudioCliCommand returns a new PlayAudioCliCommand.
func NewPlayAudioCliCommand(command *commands.PlayAudioCommand) *PlayAudioCliCommand {
	return &PlayAudioCliCommand{command: command}
}

// Generate returns the cobra command for play-audio.
func (c *PlayAudioCliCommand) Generate() *cobra.Command {
	command := &cobra.Command{
		Short: "Play an audio file on an endpoint",
		Long: `Plays an audio file on an endpoint's speaker. Under a camera hold (session hold --camera,
with the lease) the clip goes through the held camera session: the CLI converts it to Ogg/Opus
with ffmpeg when it is not Ogg already, and the reply comes once it starts. Otherwise it is the
media-player/play-audio message, and Port OS converts the audio. Clips are at most 60 seconds on
a robot; a second clip while one plays is refused as busy (exit 12).`,
		Example: `port message play-audio ep/a1b2c3d4 --file ./hello.ogg`,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			file, _ := cmd.Flags().GetString("file")
			title, _ := cmd.Flags().GetString("title")
			return c.command.Run(cmd.Context(), commands.PlayAudioParams{EndpointID: args[0], FilePath: file, Title: title})
		},
	}
	command.Flags().StringP("file", "f", "", "Audio file to play (.ogg, .opus, .mp3, .wav, .m4a, .aac, .flac)")
	command.MarkFlagRequired("file")
	command.Flags().String("title", "", "Now-playing name for endpoints with a display")
	return command
}

// RecordCommand wraps the record command implementation.
type RecordCommand struct {
	command *commands.RecordCommand
}

// NewRecordCommand returns a new RecordCommand.
func NewRecordCommand(command *commands.RecordCommand) *RecordCommand {
	return &RecordCommand{command: command}
}

// Generate returns the cobra command for record.
func (c *RecordCommand) Generate() *cobra.Command {
	command := &cobra.Command{
		Aliases: []string{"rec"},
		Short:   "Record video and audio from an endpoint",
		Long:    `Establishes an RTC session and records video, with sound when the device sends it, for the specified duration to a file. Playback runs at real speed for the true length. The container follows the extension: .mp4 and .mov (audio encoded as AAC), .mkv (everything copied), .webm (VP8 video only). A device with no audio track is saved video-only and the result says so. Under a camera hold (session hold --camera, with the lease) it records the held camera instead, without opening a camera session of its own; that clip is video only, because the held camera never turns the microphone on, and lasts at most 600 seconds. While a camera hold is live, record needs its lease.`,
		Example: `port msg record ep0 --output ./file.mp4 --duration 30`,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			outputPath, _ := cmd.Flags().GetString("output")
			durationSec, _ := cmd.Flags().GetInt("duration")
			relayOnly, _ := cmd.Flags().GetBool("relay-only")

			params := commands.RecordParams{
				EndpointID: args[0],
				OutputPath: outputPath,
				Duration:   time.Duration(durationSec) * time.Second,
				RelayOnly:  relayOnly,
			}
			return c.command.Run(ctx, params)
		},
	}

	command.Flags().StringP("output", "o", "", "Output path for the recording; the extension picks the container (.mp4, .mov, .mkv, .webm)")
	command.MarkFlagRequired("output")
	command.Flags().IntP("duration", "d", 0, "Recording duration in seconds")
	command.MarkFlagRequired("duration")
	command.Flags().Bool("relay-only", false, "Connect only through the Port OS relay (for networks without UDP)")

	return command
}
