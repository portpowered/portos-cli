package commands

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
	"go.uber.org/zap"
)

var errFFmpegMissing = errors.New("ffmpeg is required to write the recording but was not found on PATH; " +
	"install it (winget install ffmpeg, brew install ffmpeg or apt install ffmpeg) and try again")

// RecordCommand handles the `port message <endpoint-id> record --output <path> --duration <seconds>` subcommand.
type RecordCommand struct {
	recorder       *Recorder
	resolver       transport.Resolver
	formatter      output.Formatter
	logger         *logging.LoggerRef
	configProvider *config.ManagerProvider
}

// NewRecordCommand creates a new record command.
func NewRecordCommand(recorder *Recorder, resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *RecordCommand {
	return &RecordCommand{
		recorder:       recorder,
		resolver:       resolver,
		formatter:      formatter,
		logger:         logger,
		configProvider: configProvider,
	}
}

// RecordResult represents the result of a recording.
type RecordResult struct {
	Success         bool    `json:"success"`
	OutputPath      string  `json:"output_path,omitempty"`
	Message         string  `json:"message,omitempty"`
	HasAudio        bool    `json:"hasAudio"`
	DurationSeconds float64 `json:"durationSeconds"`
	VideoCodec      string  `json:"videoCodec"`
	// AudioCodec is the codec stored in the file ("aac" or "opus"); empty without audio.
	AudioCodec string `json:"audioCodec,omitempty"`
}

// RecordParams contains parameters for the Run command.
type RecordParams struct {
	EndpointID string
	OutputPath string // required for record
	Duration   time.Duration
	// RelayOnly connects only through the Port OS relay.
	RelayOnly bool
	// AudioOnly writes only the sound, to an audio file (.ogg, .opus, .wav, .mp3, .m4a, .flac).
	AudioOnly bool
}

// Run establishes an RTC session via the recorder for the given duration, then muxes the video
// (and the audio, when the device sends it) to outputPath using ffmpeg. Under a camera hold it
// records the held camera instead, without opening a session of its own.
func (c *RecordCommand) Run(ctx context.Context, params RecordParams) error {
	if params.EndpointID == "" {
		return fmt.Errorf("endpoint ID is required")
	}
	if params.OutputPath == "" {
		return fmt.Errorf("output path is required (use --output or -o)")
	}
	if params.Duration <= 0 {
		return fmt.Errorf("duration must be positive (use --duration)")
	}
	if params.AudioOnly {
		if err := checkAudioOutput(params.OutputPath); err != nil {
			return exitcode.NewInvalidArgument(err)
		}
	}

	// Muxing is the last step; fail before a 30 s capture rather than after it.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errFFmpegMissing
	}

	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_RTC_SESSION, "")
	if err != nil {
		return err
	}
	if transport.Held(sender) {
		return c.runHeld(ctx, sender, params)
	}

	manager, err := c.configProvider.GetManager()
	if err != nil {
		return fmt.Errorf("failed to resolve config: %w", err)
	}
	endpointID := params.EndpointID

	sessionDomain := "cli"
	if cfg, loadErr := manager.LoadConfig(); loadErr == nil && cfg.APIBaseURL != "" {
		if u, err := url.Parse(cfg.APIBaseURL); err == nil && u.Host != "" {
			sessionDomain = u.Hostname()
		}
	}

	sessionID := fmt.Sprintf("record-%d-%s", time.Now().UnixNano(), endpointID[:min(8, len(endpointID))])

	c.logger.Logger().Info("starting record",
		zap.String("endpoint_id", endpointID),
		zap.String("session_domain", sessionDomain),
		zap.String("output", params.OutputPath),
		zap.Duration("duration", params.Duration),
	)

	result, err := c.recorder.Run(ctx, RecorderParams{
		EndpointID:    endpointID,
		SessionDomain: sessionDomain,
		SessionID:     sessionID,
		Duration:      params.Duration,
		Audio:         true,
		RelayOnly:     params.RelayOnly,
		CheckVideoCodec: func(codec string) error {
			return checkContainerSupportsVideo(params.OutputPath, codec)
		},
	})
	if err != nil {
		return err
	}
	defer result.Remove()

	if params.AudioOnly {
		if !result.HasAudio() {
			return errors.New("the device sent no audio, so there is nothing to write")
		}
		return c.writeAudioOnly(ctx, result.AudioPath, params.OutputPath)
	}
	res, err := c.mux(ctx, result, params.OutputPath, params.Duration)
	if err != nil {
		return err
	}
	return c.print(res)
}

// runHeld records the held camera through the session host. A hold opened with --listen also
// captures the camera's microphone over the same window and the file gets sound; otherwise the
// camera never turns a microphone on and the file is video only.
func (c *RecordCommand) runHeld(ctx context.Context, sender transport.Sender, params RecordParams) error {
	c.logger.Logger().Info("recording the held camera",
		zap.String("endpoint_id", params.EndpointID), zap.Duration("duration", params.Duration))
	if params.AudioOnly {
		capture, err := transport.CaptureHeld(ctx, sender, params.EndpointID, time.Time{}, params.Duration)
		if err != nil {
			return fmt.Errorf("record the held camera's audio: %w", err)
		}
		defer func() { _ = os.Remove(capture.Path) }()
		if capture.Packets == 0 {
			return errors.New("the held camera heard no audio in the window")
		}
		return c.writeAudioOnly(ctx, capture.Path, params.OutputPath)
	}
	// The video starts at the next keyframe, so the capture runs a little longer and the mux
	// trims it to the video.
	captureCtx, cancelCapture := context.WithCancel(ctx)
	defer cancelCapture()
	audio := make(chan heldAudioResult, 1)
	go func() {
		capture, err := transport.CaptureHeld(captureCtx, sender, params.EndpointID, time.Time{}, params.Duration+heldKeyframeSlack)
		audio <- heldAudioResult{capture: capture, err: err}
	}()
	clip, err := transport.RecordHeld(ctx, sender, params.EndpointID, params.Duration)
	if err != nil {
		cancelCapture() // no video, so the sound is not wanted
	}
	sound := <-audio
	if sound.err == nil {
		defer func() { _ = os.Remove(sound.capture.Path) }()
	}
	if err != nil {
		return fmt.Errorf("record the held camera: %w", err)
	}
	defer func() { _ = os.Remove(clip.Path) }()
	if err := checkContainerSupportsVideo(params.OutputPath, clip.Codec); err != nil {
		return exitcode.NewInvalidArgument(err)
	}
	rec := &RecorderResult{TempPath: clip.Path, VideoCodec: clip.Codec, FrameRate: clip.FrameRate, VideoSpan: params.Duration, VideoStart: clip.StartedAt}
	message := "Recorded from the held camera, which does not listen (hold it with --listen for sound)"
	switch {
	case sound.err != nil && exitcode.Code(sound.err) != exitcode.ExitHold:
		c.logger.Logger().Warn("held camera audio capture failed; the recording is video only", zap.Error(sound.err))
		message = "Recorded from the held camera; its audio capture failed, so the file is video only"
	case sound.err == nil && sound.capture.Packets > 0 && !clip.StartedAt.IsZero():
		rec.AudioPath, rec.AudioStart = sound.capture.Path, sound.capture.FirstPacketAt
		message = "Recorded from the held camera with its microphone"
	case sound.err == nil:
		message = "Recorded from the held camera, which heard no audio"
	}
	res, err := c.mux(ctx, rec, params.OutputPath, params.Duration)
	if err != nil {
		return err
	}
	res.Message = message
	return c.print(res)
}

// heldKeyframeSlack is how much longer than the video a held recording's audio capture runs,
// to cover the wait for the keyframe the video starts at.
const heldKeyframeSlack = 5 * time.Second

type heldAudioResult struct {
	capture hostclient.AudioCapture
	err     error
}

// writeAudioOnly writes received Ogg/Opus to the audio output and prints the result.
func (c *RecordCommand) writeAudioOnly(ctx context.Context, oggPath, outPath string) error {
	if err := writeAudio(ctx, oggPath, outPath); err != nil {
		return err
	}
	res := RecordResult{Success: true, OutputPath: outPath, Message: "Audio saved", HasAudio: true, AudioCodec: "opus"}
	if seconds, err := probeDuration(ctx, outPath); err == nil {
		res.DurationSeconds = math.Round(seconds*100) / 100
	}
	if ext := strings.ToLower(filepath.Ext(outPath)); ext != ".ogg" && ext != ".opus" {
		res.AudioCodec = strings.TrimPrefix(ext, ".")
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(res)
	}
	return c.formatter.Print(fmt.Sprintf("Audio saved to %s (%.1f s)", outPath, res.DurationSeconds))
}

func (c *RecordCommand) print(res RecordResult) error {
	if c.formatter.IsJSON() {
		return c.formatter.Print(res)
	}
	return c.formatter.Print(fmt.Sprintf("Recording saved to %s (%s)", res.OutputPath,
		recordingSummary(res.OutputPath, res.DurationSeconds, res.VideoCodec, res.HasAudio)))
}

// mux writes the recorded tracks to outPath at their real speed and measures the result.
func (c *RecordCommand) mux(ctx context.Context, rec *RecorderResult, outPath string, duration time.Duration) (RecordResult, error) {
	if rec.FrameRate <= 0 {
		return RecordResult{}, fmt.Errorf("the camera sent too few video frames to time the recording")
	}
	plan := muxPlan{
		VideoPath: rec.TempPath, VideoCodec: rec.VideoCodec, FrameRate: rec.FrameRate, OutputPath: outPath, Duration: duration,
	}
	if rec.HasAudio() {
		plan.AudioPath = rec.AudioPath
		plan.AudioOffset, plan.AudioSkip = audioAlignment(rec.VideoStart, rec.AudioStart)
	}
	if err := c.ffmpegMux(ctx, plan); err != nil {
		return RecordResult{}, fmt.Errorf("ffmpeg write output: %w", err)
	}
	seconds, probeErr := probeDuration(ctx, outPath)
	if probeErr != nil {
		c.logger.Logger().Warn("ffprobe unavailable; using the RTP duration", zap.Error(probeErr))
		seconds = min(rec.VideoSpan, duration).Seconds()
	}
	res := RecordResult{
		Success: true, OutputPath: outPath, Message: "Recording saved successfully",
		HasAudio: rec.HasAudio(), DurationSeconds: math.Round(seconds*100) / 100, VideoCodec: rec.VideoCodec,
	}
	if res.HasAudio {
		res.AudioCodec = audioStoredCodec(outPath)
	}
	return res, nil
}

// ffmpegMux runs ffmpeg on the plan. Output format is inferred from the file extension.
func (c *RecordCommand) ffmpegMux(ctx context.Context, plan muxPlan) error {
	args, err := buildMuxArgs(plan)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
