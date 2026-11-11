package commands

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	videoCodecH264 = "H264"
	videoCodecVP8  = "VP8"
)

// muxPlan is everything ffmpeg needs to combine the recorded video (raw H264 or IVF) and the
// optional Ogg/Opus audio into the output file. Offsets delay the track that started later.
type muxPlan struct {
	VideoPath  string
	VideoCodec string
	FrameRate  float64
	AudioPath  string // empty when the device sent no audio
	// AudioOffset delays audio that started after the first video frame; AudioSkip trims audio
	// that started before it. The output timeline starts at the first video frame.
	AudioOffset time.Duration
	AudioSkip   time.Duration
	// Duration caps the output at the requested length; zero means no cap.
	Duration   time.Duration
	OutputPath string
}

func containerExt(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// checkContainerSupportsVideo rejects a video codec the output container cannot hold, naming
// containers that can. The recorder calls it as soon as the codec is negotiated, so an
// impossible request fails before a 30 second capture.
func checkContainerSupportsVideo(outPath, videoCodec string) error {
	ext := containerExt(outPath)
	switch {
	case ext == "webm" && videoCodec != videoCodecVP8:
		return fmt.Errorf("this camera sends %s video, which .webm cannot hold; use a .mp4 or .mkv output path", videoCodec)
	case (ext == "mp4" || ext == "mov") && videoCodec == videoCodecVP8:
		return fmt.Errorf("this camera sends VP8 video, which .%s cannot hold; use a .webm or .mkv output path", ext)
	}
	return nil
}

// audioStoredCodec is the audio codec written to the file: AAC for MP4-family containers and
// unknown extensions (widest playback), Opus copied for Matroska and WebM.
func audioStoredCodec(outPath string) string {
	switch containerExt(outPath) {
	case "mkv", "webm":
		return "opus"
	}
	return "aac"
}

// safePathArg keeps ffmpeg and ffprobe from reading a path as an option or a protocol. The path
// comes from the caller (an agent may supply it): "-x.mp4" would be an option and "rtmp:x.mp4"
// a network URL. Windows drive paths (C:\ or C:/) are left alone.
func safePathArg(path string) string {
	if strings.HasPrefix(path, "-") {
		return "./" + path
	}
	if len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\') &&
		(path[0] >= 'a' && path[0] <= 'z' || path[0] >= 'A' && path[0] <= 'Z') {
		return path
	}
	if i := strings.IndexAny(path, ":/\\"); i >= 0 && path[i] == ':' {
		return "file:" + path
	}
	return path
}

func seconds3(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 3, 64) }

func rateArg(rate float64) string { return strconv.FormatFloat(rate, 'f', -1, 64) }

func buildMuxArgs(p muxPlan) ([]string, error) {
	if err := checkContainerSupportsVideo(p.OutputPath, p.VideoCodec); err != nil {
		return nil, err
	}
	if p.FrameRate <= 0 {
		return nil, fmt.Errorf("frame rate must be positive")
	}
	args := []string{"-y"}
	// Raw H264 and IVF carry no usable timestamps. -r as an input option discards whatever the
	// stream claims (an H264 VUI block can announce a rate the camera does not deliver, which
	// -framerate would not override) and spaces the frames at the measured rate.
	args = append(args, "-r", rateArg(p.FrameRate))
	args = append(args, "-i", p.VideoPath)
	if p.AudioPath != "" {
		if p.AudioSkip > 0 {
			args = append(args, "-ss", seconds3(p.AudioSkip))
		}
		if p.AudioOffset > 0 {
			args = append(args, "-itsoffset", seconds3(p.AudioOffset))
		}
		args = append(args, "-i", p.AudioPath, "-map", "0:v:0", "-map", "1:a:0")
	} else {
		args = append(args, "-map", "0:v:0")
	}
	args = append(args, "-c:v", "copy")
	if p.AudioPath != "" {
		audio := "copy"
		if audioStoredCodec(p.OutputPath) == "aac" {
			audio = "aac"
		}
		args = append(args, "-c:a", audio)
	}
	if ext := containerExt(p.OutputPath); ext == "mp4" || ext == "mov" {
		args = append(args, "-movflags", "+faststart")
	}
	if p.Duration > 0 {
		args = append(args, "-t", seconds3(p.Duration))
	}
	return append(args, safePathArg(p.OutputPath)), nil
}

// audioAlignment lines the audio up with the first video frame: audio that started later is
// delayed by offset, audio that started earlier has its head trimmed by skip.
func audioAlignment(videoStart, audioStart time.Time) (offset, skip time.Duration) {
	if d := audioStart.Sub(videoStart); d > 0 {
		return d, 0
	}
	return 0, videoStart.Sub(audioStart)
}

func recordingSummary(outPath string, seconds float64, videoCodec string, hasAudio bool) string {
	audio := "no audio track"
	if hasAudio {
		audio = "audio Opus"
		if audioStoredCodec(outPath) == "aac" {
			audio = "audio Opus→AAC"
		}
	}
	return fmt.Sprintf("%.1f s, video %s, %s", seconds, videoCodec, audio)
}

// probeDuration measures the written file with ffprobe.
func probeDuration(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", safePathArg(path)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}
