package commands

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
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

	// Muxing is the last step; fail before a 30 s capture rather than after it.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errFFmpegMissing
	}

	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_RTC_SESSION)
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

	res, err := c.mux(ctx, result, params.OutputPath, params.Duration)
	if err != nil {
		return err
	}
	return c.print(res)
}

// runHeld records the held camera through the session host. The held camera receives no audio
// (holding it never turns a robot microphone on), so the file is video only.
func (c *RecordCommand) runHeld(ctx context.Context, sender transport.Sender, params RecordParams) error {
	c.logger.Logger().Info("recording the held camera",
		zap.String("endpoint_id", params.EndpointID), zap.Duration("duration", params.Duration))
	clip, err := transport.RecordHeld(ctx, sender, params.EndpointID, params.Duration)
	if err != nil {
		return fmt.Errorf("record the held camera: %w", err)
	}
	defer func() { _ = os.Remove(clip.Path) }()
	if err := checkContainerSupportsVideo(params.OutputPath, clip.Codec); err != nil {
		return exitcode.NewInvalidArgument(err)
	}
	rec := &RecorderResult{TempPath: clip.Path, VideoCodec: clip.Codec, FrameRate: clip.FrameRate, VideoSpan: params.Duration}
	res, err := c.mux(ctx, rec, params.OutputPath, params.Duration)
	if err != nil {
		return err
	}
	res.Message = "Recorded from the held camera, which carries no audio"
	return c.print(res)
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
