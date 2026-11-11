package commands

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/portpowered/portos-cli/internal/transport"
	"go.uber.org/zap"
)

const (
	snapshotRecordDur = 3 * time.Second // record this long so we have at least one keyframe for ffmpeg
)

// SnapshotCommand handles the `port message <endpoint-id> snapshot [output-path]` subcommand.
type SnapshotCommand struct {
	recorder       *Recorder
	resolver       transport.Resolver
	formatter      output.Formatter
	logger         *logging.LoggerRef
	configProvider *config.ManagerProvider
}

// NewSnapshotCommand creates a new snapshot command (used for snapshot subcommand of message).
func NewSnapshotCommand(recorder *Recorder, resolver transport.Resolver, formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *SnapshotCommand {
	return &SnapshotCommand{
		recorder:       recorder,
		resolver:       resolver,
		formatter:      formatter,
		logger:         logger,
		configProvider: configProvider,
	}
}

// SnapshotResult represents the result of capturing a snapshot. CapturedAt is set for a frame
// from a held camera.
type SnapshotResult struct {
	Success    bool       `json:"success"`
	OutputPath string     `json:"output_path,omitempty"`
	Message    string     `json:"message,omitempty"`
	CapturedAt *time.Time `json:"captured_at,omitempty"`
}

// SnapshotParams contains parameters for the Run command
type SnapshotParams struct {
	EndpointID string
	OutputPath string // empty means write to stdout
	// RelayOnly connects only through the Port OS relay.
	RelayOnly bool
}

// Run executes the snapshot: establish RTC session via recorder, capture one frame with ffmpeg, write to file or stdout.
func (c *SnapshotCommand) Run(ctx context.Context, params SnapshotParams) error {
	if params.EndpointID == "" {
		return fmt.Errorf("endpoint ID is required")
	}

	sender, err := c.resolver.For(ctx, params.EndpointID, api.INTERFACE_PORTOS_SNAPSHOT)
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

	sessionID := fmt.Sprintf("snapshot-%d-%s", time.Now().UnixNano(), endpointID[:min(8, len(endpointID))])

	c.logger.Logger().Info("starting snapshot",
		zap.String("endpoint_id", endpointID),
		zap.String("session_domain", sessionDomain),
		zap.String("output", params.OutputPath),
	)

	result, err := c.recorder.Run(ctx, snapshotRecorderParams(endpointID, sessionDomain, sessionID, params.RelayOnly))
	if err != nil {
		return err
	}
	recordPath := result.TempPath
	defer result.Remove()

	var imgData []byte
	if params.OutputPath != "" {
		if err := c.ffmpegExtractFrameToFile(ctx, recordPath, params.OutputPath); err != nil {
			return fmt.Errorf("ffmpeg extract frame: %w", err)
		}
	} else {
		var err error
		imgData, err = c.ffmpegExtractFrameToPipe(ctx, recordPath)
		if err != nil {
			return fmt.Errorf("ffmpeg extract frame: %w", err)
		}
	}

	if c.formatter.IsJSON() {
		result := SnapshotResult{
			Success:    true,
			OutputPath: params.OutputPath,
			Message:    "Snapshot captured successfully",
		}
		return c.formatter.Print(result)
	}
	if params.OutputPath != "" {
		msg := fmt.Sprintf("Snapshot saved to %s", params.OutputPath)
		return c.formatter.Print(msg)
	}
	if !c.formatter.IsQuiet() {
		if _, err := os.Stdout.Write(imgData); err != nil {
			return fmt.Errorf("write snapshot to stdout: %w", err)
		}
	}
	return nil
}

// runHeld takes the newest frame from the session host's held camera.
func (c *SnapshotCommand) runHeld(ctx context.Context, sender transport.Sender, params SnapshotParams) error {
	messageType := api.INTERFACE_PORTOS_SNAPSHOT + "/" + api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT
	sync := api.SyncResponseType
	response, err := sender.Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: params.EndpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_SNAPSHOT, Name: api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT, Version: "1.0"},
			Body:   map[string]interface{}{"responseFormat": string(api.BinaryResponseFormatBase64)},
		},
		ResponseType: &sync,
	}, uuid.NewString())
	if err != nil {
		return fmt.Errorf("get-snapshot: %w", err)
	}
	if err := messageRequestFailure(response); err != nil {
		return fmt.Errorf("get-snapshot: %w", err)
	}
	jpeg, capturedAt, err := decodeHeldSnapshot(response)
	if err != nil {
		return err
	}
	if params.OutputPath != "" {
		if err := os.WriteFile(params.OutputPath, jpeg, 0o644); err != nil {
			return fmt.Errorf("write snapshot: %w", err)
		}
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(SnapshotResult{Success: true, OutputPath: params.OutputPath, Message: "Snapshot taken from the held camera", CapturedAt: &capturedAt})
	}
	if params.OutputPath != "" {
		return c.formatter.Print(fmt.Sprintf("Snapshot saved to %s (held camera, %s old)", params.OutputPath, time.Since(capturedAt).Round(100*time.Millisecond)))
	}
	if !c.formatter.IsQuiet() {
		if _, err := os.Stdout.Write(jpeg); err != nil {
			return fmt.Errorf("write snapshot to stdout: %w", err)
		}
	}
	return nil
}

// decodeHeldSnapshot reads the inline JPEG and its capture time from a get-snapshot reply.
func decodeHeldSnapshot(response *api.MessageRequest) ([]byte, time.Time, error) {
	if response == nil || response.Status != api.SUCCEEDED {
		return nil, time.Time{}, errors.New("get-snapshot did not succeed")
	}
	for _, result := range response.Results {
		if result.Message == nil || result.Message.Header == nil || result.Message.Header.Name != api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT_RESPONSE {
			continue
		}
		image, _ := result.Message.Body["image"].(map[string]interface{})
		data, _ := image["data"].(string)
		jpeg, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(jpeg) == 0 {
			return nil, time.Time{}, errors.New("the held camera's reply has no image")
		}
		stamp, _ := result.Message.Body["timeOfSample"].(string)
		capturedAt, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return nil, time.Time{}, fmt.Errorf("the held camera's reply has no capture time: %w", err)
		}
		return jpeg, capturedAt, nil
	}
	return nil, time.Time{}, errors.New("the held camera returned no snapshot")
}

// snapshotRecorderParams are video-only: a snapshot must never turn a robot microphone on.
func snapshotRecorderParams(endpointID, sessionDomain, sessionID string, relayOnly bool) RecorderParams {
	return RecorderParams{EndpointID: endpointID, SessionDomain: sessionDomain, SessionID: sessionID, Duration: snapshotRecordDur, RelayOnly: relayOnly}
}

// ffmpegExtractFrameToFile runs ffmpeg to decode the recorded video and write a single JPEG frame to outPath.
func (c *SnapshotCommand) ffmpegExtractFrameToFile(ctx context.Context, inputPath, outPath string) error {
	args := []string{"-y", "-i", inputPath, "-vframes", "1", "-f", "image2", "-q:v", "2", safePathArg(outPath)}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// ffmpegExtractFrameToPipe runs ffmpeg and returns the first frame as JPEG bytes from stdout.
func (c *SnapshotCommand) ffmpegExtractFrameToPipe(ctx context.Context, inputPath string) ([]byte, error) {
	args := []string{"-y", "-i", inputPath, "-vframes", "1", "-f", "image2", "-q:v", "2", "pipe:1"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return out, nil
}
