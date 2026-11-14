package commands

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/portpowered/portos-cli/internal/transport"
)

// audioOutputExts are the files received audio is written to: Ogg/Opus as received, the rest
// converted with ffmpeg.
var audioOutputExts = map[string]bool{".ogg": true, ".opus": true, ".wav": true, ".mp3": true, ".m4a": true, ".flac": true}

// ListenResult is what play-audio --listen recorded: the camera's microphone from RecordStartedAt
// to RecordEndedAt, written to OutputPath. FirstAudioAt is when the first packet in that window
// arrived (absent when none did). The volumes are ffmpeg's volumedetect over the file, in dBFS,
// when ffmpeg could measure them.
type ListenResult struct {
	OutputPath      string     `json:"outputPath"`
	RecordStartedAt time.Time  `json:"recordStartedAt"`
	RecordEndedAt   time.Time  `json:"recordEndedAt"`
	FirstAudioAt    *time.Time `json:"firstAudioAt,omitempty"`
	AudioPackets    int        `json:"audioPackets"`
	MeanVolumeDB    *float64   `json:"meanVolumeDb,omitempty"`
	MaxVolumeDB     *float64   `json:"maxVolumeDb,omitempty"`
}

// listen captures the held camera's microphone from `from` until `until` and writes it out.
func (c *PlayAudioCommand) listen(ctx context.Context, sender transport.Sender, params PlayAudioParams, from, until time.Time) (*ListenResult, error) {
	capture, err := transport.CaptureHeld(ctx, sender, params.EndpointID, from, until.Sub(from))
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	defer func() { _ = os.Remove(capture.Path) }()
	if err := writeAudio(ctx, capture.Path, params.OutputPath); err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	// The host's headers parse as UTC; report local time like playStartedAt so the times read side by side.
	result := &ListenResult{OutputPath: params.OutputPath, RecordStartedAt: capture.From.Local(), RecordEndedAt: capture.Until.Local(), AudioPackets: capture.Packets}
	if !capture.FirstPacketAt.IsZero() {
		first := capture.FirstPacketAt.Local()
		result.FirstAudioAt = &first
	}
	if capture.Packets > 0 {
		result.MeanVolumeDB, result.MaxVolumeDB = audioLevels(ctx, capture.Path)
	}
	return result, nil
}

// checkAudioOutput refuses an output path whose extension no audio file has.
func checkAudioOutput(path string) error {
	if !audioOutputExts[strings.ToLower(filepath.Ext(path))] {
		return fmt.Errorf("audio is written to .ogg, .opus, .wav, .mp3, .m4a or .flac, not %q", filepath.Ext(path))
	}
	return nil
}

// writeAudio writes received Ogg/Opus to out: copied for .ogg and .opus, converted with ffmpeg
// for the other extensions.
func writeAudio(ctx context.Context, oggPath, out string) error {
	switch strings.ToLower(filepath.Ext(out)) {
	case ".ogg", ".opus":
		data, err := os.ReadFile(oggPath)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("writing %s needs ffmpeg, which is not on PATH; write .ogg instead", filepath.Ext(out))
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", safePathArg(oggPath), safePathArg(out)}
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("convert the audio: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

var volumeLine = regexp.MustCompile(`(mean|max)_volume: (-?[0-9.]+|-inf) dB`)

// audioLevels measures a file's mean and peak volume in dBFS with ffmpeg's volumedetect. Either
// is nil when ffmpeg is missing or reports none.
func audioLevels(ctx context.Context, path string) (mean, peak *float64) {
	output, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-nostats", "-i", safePathArg(path), "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		return nil, nil
	}
	return parseVolumes(string(output))
}

// parseVolumes reads volumedetect's mean_volume and max_volume lines.
func parseVolumes(output string) (mean, peak *float64) {
	for _, match := range volumeLine.FindAllStringSubmatch(output, -1) {
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil || math.IsInf(value, 0) {
			continue // -inf: a silent file has no level
		}
		if match[1] == "mean" {
			mean = &value
		} else {
			peak = &value
		}
	}
	return mean, peak
}
