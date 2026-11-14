package commands

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
)

const (
	// playAudioMaxBytes is the largest clip Port OS takes inline.
	playAudioMaxBytes = 10 << 20
	// QuietHoursEnd is the local hour before which play-audio refuses unless --allow-quiet-hours,
	// so a script left running overnight never wakes the household.
	QuietHoursEnd = 9
	// ListenMax is the longest play-audio --listen.
	ListenMax = 5 * time.Minute
)

// PlayAudioCommand plays audio on an endpoint: through the held camera's talkback track under a
// camera hold, otherwise as the cloud media-player/play-audio message. Under a listening hold it
// also records what the camera hears while the clip plays and after it.
type PlayAudioCommand struct {
	resolver  transport.Resolver
	formatter output.Formatter
	logger    *logging.LoggerRef
	now       func() time.Time // the quiet-hours clock
}

// NewPlayAudioCommand creates a new play-audio command.
func NewPlayAudioCommand(resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef) *PlayAudioCommand {
	return &PlayAudioCommand{resolver: resolver, formatter: formatter, logger: logger, now: time.Now}
}

// PlayAudioParams are the inputs of PlayAudioCommand.Run. Exactly one of FilePath, Text and
// Chirp names the audio.
type PlayAudioParams struct {
	EndpointID string
	FilePath   string
	// Text is spoken by SpeechModel on SpeechEndpoint (speech-audio-generation), in Voice.
	Text           string
	Voice          string
	SpeechEndpoint string
	SpeechModel    string
	// Chirp plays a short rising tone, which a recording can be aligned on.
	Chirp bool
	Title string
	// Listen records the camera's microphone from the clip's start until Listen after its end,
	// to OutputPath. It needs a camera hold opened with --listen.
	Listen          time.Duration
	OutputPath      string
	AllowQuietHours bool
}

// PlayAudioResult is what play-audio prints. Seconds is the clip's length when it is Ogg/Opus.
// Under a hold, PlayStartedAt is when the host reported the first packet sent and PlayEndedAt
// that plus the clip's length; the clip plays on after the command returns unless it listens.
type PlayAudioResult struct {
	EndpointID    string     `json:"endpointId"`
	Held          bool       `json:"held"`
	Seconds       float64    `json:"seconds,omitempty"`
	PlayStartedAt *time.Time `json:"playStartedAt,omitempty"`
	PlayEndedAt   *time.Time `json:"playEndedAt,omitempty"`
	*ListenResult
}

// Run sends the audio. Under a camera hold a non-Ogg clip is converted to Ogg/Opus with ffmpeg
// first, because the held camera plays Opus only; Port OS converts it on the cloud path.
func (c *PlayAudioCommand) Run(ctx context.Context, params PlayAudioParams) error {
	if err := c.check(params); err != nil {
		return err
	}
	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_MEDIA_PLAYER, api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO)
	if err != nil {
		return err
	}
	held := transport.Held(sender)
	if params.Listen > 0 && !held {
		return exitcode.NewInvalidArgument(errors.New("--listen records through a camera hold: open one with session hold --camera --listen and pass its lease"))
	}
	clip, err := c.clip(ctx, params, held)
	if err != nil {
		return err
	}
	if len(clip.data) > playAudioMaxBytes {
		return exitcode.NewInvalidArgument(fmt.Errorf("the clip is %d bytes; play-audio takes at most 10 MiB", len(clip.data)))
	}
	result := PlayAudioResult{EndpointID: params.EndpointID, Held: held}
	var length time.Duration
	if clip.mime == "audio/ogg" {
		if _, n, err := parseOggOpus(clip.data); err == nil {
			length, result.Seconds = n, n.Seconds()
		}
	}

	sentAt := time.Now()
	if err := c.send(ctx, sender, params, clip); err != nil {
		return err
	}
	if held {
		started := time.Now()
		ended := started.Add(length)
		result.PlayStartedAt, result.PlayEndedAt = &started, &ended
		if params.Listen > 0 {
			if result.ListenResult, err = c.listen(ctx, sender, params, sentAt, ended.Add(params.Listen)); err != nil {
				return err
			}
		}
	}
	return c.print(result)
}

// check refuses impossible flag sets and quiet hours before anything is sent.
func (c *PlayAudioCommand) check(params PlayAudioParams) error {
	sources := 0
	for _, given := range []bool{params.FilePath != "", params.Text != "", params.Chirp} {
		if given {
			sources++
		}
	}
	invalid := func(message string) error { return exitcode.NewInvalidArgument(errors.New(message)) }
	switch {
	case params.EndpointID == "":
		return invalid("endpoint ID is required")
	case sources != 1:
		return invalid("name the audio with exactly one of --file, --text or --chirp")
	case params.Text != "" && (params.SpeechEndpoint == "" || params.SpeechModel == ""):
		return invalid("--text needs --speech-endpoint (an endpoint with speech-audio-generation) and --speech-model (from query-models)")
	case params.Text == "" && (params.Voice != "" || params.SpeechEndpoint != "" || params.SpeechModel != ""):
		return invalid("--voice, --speech-endpoint and --speech-model go with --text")
	case params.Listen < 0 || params.Listen > ListenMax:
		return invalid(fmt.Sprintf("--listen is between 0 and %d seconds", int(ListenMax.Seconds())))
	case params.Listen > 0 && params.OutputPath == "":
		return invalid("--listen writes what the camera hears to --output")
	case params.Listen == 0 && params.OutputPath != "":
		return invalid("--output holds what --listen records; add --listen")
	}
	if params.OutputPath != "" {
		if err := checkAudioOutput(params.OutputPath); err != nil {
			return exitcode.NewInvalidArgument(err)
		}
	}
	if now := c.now(); !params.AllowQuietHours && now.Hour() < QuietHoursEnd {
		return invalid(fmt.Sprintf("it is %s here, before %02d:00, so play-audio stays quiet; pass --allow-quiet-hours to play anyway", now.Format("15:04"), QuietHoursEnd))
	}
	return nil
}

// send plays the clip with the media-player/play-audio message and waits for its reply.
func (c *PlayAudioCommand) send(ctx context.Context, sender transport.Sender, params PlayAudioParams, clip playClip) error {
	body := map[string]interface{}{"audio": map[string]interface{}{"data": base64.StdEncoding.EncodeToString(clip.data), "mimeType": clip.mime}}
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
	return nil
}

func (c *PlayAudioCommand) print(result PlayAudioResult) error {
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	line := "Playing on " + result.EndpointID
	if result.Seconds > 0 {
		line += fmt.Sprintf(" (%.1f s)", result.Seconds)
	}
	if listen := result.ListenResult; listen != nil {
		line += fmt.Sprintf("; recorded %.1f s of the camera's microphone to %s", listen.RecordEndedAt.Sub(listen.RecordStartedAt).Seconds(), listen.OutputPath)
		if listen.MaxVolumeDB != nil {
			line += fmt.Sprintf(" (peak %.1f dBFS)", *listen.MaxVolumeDB)
		}
		if listen.AudioPackets == 0 {
			line += ", but no audio arrived"
		}
	}
	return c.formatter.Print(line)
}
