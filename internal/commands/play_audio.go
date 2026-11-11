package commands

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
)

// playAudioMaxBytes is the largest clip Port OS takes inline.
const playAudioMaxBytes = 10 << 20

// audioMimeTypes maps the file extensions play-audio sends to their MIME types.
var audioMimeTypes = map[string]string{
	".ogg":  "audio/ogg",
	".opus": "audio/ogg",
	".mp3":  "audio/mpeg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
}

// PlayAudioCommand plays an audio file on an endpoint: through the held camera's talkback
// track under a camera hold, otherwise as the cloud media-player/play-audio message.
type PlayAudioCommand struct {
	resolver  transport.Resolver
	formatter output.Formatter
	logger    *logging.LoggerRef
}

// NewPlayAudioCommand creates a new play-audio command.
func NewPlayAudioCommand(resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef) *PlayAudioCommand {
	return &PlayAudioCommand{resolver: resolver, formatter: formatter, logger: logger}
}

// PlayAudioParams are the inputs of PlayAudioCommand.Run.
type PlayAudioParams struct {
	EndpointID string
	FilePath   string
	Title      string
}

// PlayAudioResult is what play-audio prints. Seconds is the clip's length when it is Ogg/Opus;
// the clip plays on after the command returns.
type PlayAudioResult struct {
	EndpointID string  `json:"endpointId"`
	Held       bool    `json:"held"`
	Seconds    float64 `json:"seconds,omitempty"`
}

// Run sends the file. Under a camera hold a non-Ogg file is converted to Ogg/Opus with ffmpeg
// first, because the held camera plays Opus only; Port OS converts it on the cloud path.
func (c *PlayAudioCommand) Run(ctx context.Context, params PlayAudioParams) error {
	mime, ok := audioMimeTypes[strings.ToLower(filepath.Ext(params.FilePath))]
	if !ok {
		return exitcode.NewInvalidArgument(fmt.Errorf("play-audio takes .ogg, .opus, .mp3, .wav, .m4a, .aac or .flac, not %q", filepath.Ext(params.FilePath)))
	}
	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_MEDIA_PLAYER)
	if err != nil {
		return err
	}
	held := transport.Held(sender)
	var data []byte
	if held && mime != "audio/ogg" {
		data, err = toOggOpus(ctx, params.FilePath)
		mime = "audio/ogg"
	} else {
		data, err = os.ReadFile(params.FilePath)
	}
	if err != nil {
		return err
	}
	if len(data) > playAudioMaxBytes {
		return exitcode.NewInvalidArgument(fmt.Errorf("the clip is %d bytes; play-audio takes at most 10 MiB", len(data)))
	}
	result := PlayAudioResult{EndpointID: params.EndpointID, Held: held}
	if mime == "audio/ogg" {
		if _, length, err := parseOggOpus(data); err == nil {
			result.Seconds = length.Seconds()
		}
	}

	body := map[string]interface{}{"audio": map[string]interface{}{"data": base64.StdEncoding.EncodeToString(data), "mimeType": mime}}
	if params.Title != "" {
		body["title"] = params.Title
	}
	messageType := api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO_MESSAGE_TYPE
	sync := api.SyncResponseType
	response, err := sender.Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: params.EndpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_MEDIA_PLAYER, Name: api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO, Version: "1.0"},
			Body:   body,
		},
		ResponseType: &sync,
	}, uuid.NewString())
	if err != nil {
		return fmt.Errorf("play-audio: %w", err)
	}
	if err := messageRequestFailure(response); err != nil {
		return fmt.Errorf("play-audio: %w", err)
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	line := "Playing on " + params.EndpointID
	if result.Seconds > 0 {
		line += fmt.Sprintf(" (%.1f s)", result.Seconds)
	}
	return c.formatter.Print(line)
}

// toOggOpus converts an audio file to the 48 kHz stereo Ogg/Opus the held camera plays.
func toOggOpus(ctx context.Context, path string) ([]byte, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("under a camera hold play-audio converts %s to Ogg/Opus with ffmpeg, which is not on PATH; convert it yourself or install ffmpeg", filepath.Ext(path))
	}
	dir, err := os.MkdirTemp("", "portos-play-audio-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	out := filepath.Join(dir, "clip.ogg")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", safePathArg(path), "-vn", "-ac", "2", "-ar", "48000",
		"-c:a", "libopus", "-b:a", "64k", "-frame_duration", "20", "-f", "ogg", out}
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("convert to Ogg/Opus: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return os.ReadFile(out)
}
