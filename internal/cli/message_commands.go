package cli

import (
	"time"

	"github.com/portpowered/portos-cli/internal/commands"
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
		Long:    `Establishes an RTC session and records video, with sound when the device sends it, for the specified duration to a file. Playback runs at real speed for the true length. The container follows the extension: .mp4 and .mov (audio encoded as AAC), .mkv (everything copied), .webm (VP8 video only). A device with no audio track is saved video-only and the result says so.`,
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
