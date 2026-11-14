package commands

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/services/messages"
)

// recordingHost is a held sender whose camera hold records a prepared clip.
type recordingHost struct {
	heldSender
	clip     hostclient.Recording
	err      error
	duration time.Duration
}

func (h *recordingHost) Record(_ context.Context, _ string, d time.Duration) (hostclient.Recording, error) {
	h.duration = d
	return h.clip, h.err
}

// rawH264 writes seconds of 15 fps test video as raw H264, as the held camera keeps it.
func rawH264(t *testing.T, seconds int) string {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "held.h264")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15",
		"-t", strconv.Itoa(seconds), "-c:v", "libx264", "-x264-params", "threads=1:slices=1", "-f", "h264", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	return path
}

func TestRecordUnderAHoldTapsTheHeldCamera(t *testing.T) {
	host := &recordingHost{clip: hostclient.Recording{Path: rawH264(t, 3), Codec: videoCodecH264, FrameRate: 15}}
	formatter := &recordingFormatter{json: true}
	cmd := NewRecordCommand(nil, fixedResolver{host}, formatter, logging.NewLoggerRef(), nil)
	out := filepath.Join(t.TempDir(), "held.mp4")
	if err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: out, Duration: 3 * time.Second}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if host.duration != 3*time.Second {
		t.Fatalf("asked the host for %v, want 3s", host.duration)
	}
	result := formatter.printed[0].(RecordResult)
	if !result.Success || result.HasAudio || result.VideoCodec != videoCodecH264 || result.DurationSeconds < 2.8 || result.DurationSeconds > 3.1 {
		t.Fatalf("result = %+v", result)
	}
	if _, err := os.Stat(host.clip.Path); !os.IsNotExist(err) {
		t.Fatalf("the host's clip was left behind: %v", err)
	}
}

func TestRecordUnderAHoldRefusals(t *testing.T) {
	needFFmpeg(t)
	lost := &recordingHost{err: &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), "ep/1", "camera_unavailable", "the held camera stopped")}}
	cmd := NewRecordCommand(nil, fixedResolver{lost}, &recordingFormatter{json: true}, logging.NewLoggerRef(), nil)
	err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: filepath.Join(t.TempDir(), "x.mp4"), Duration: time.Second})
	if got := exitcode.Code(err); got != exitcode.ExitCamera {
		t.Fatalf("a lost camera exits %d (%v), want %d", got, err, exitcode.ExitCamera)
	}

	vp8 := &recordingHost{clip: hostclient.Recording{Path: filepath.Join(t.TempDir(), "held.ivf"), Codec: videoCodecVP8, FrameRate: 15}}
	cmd = NewRecordCommand(nil, fixedResolver{vp8}, &recordingFormatter{json: true}, logging.NewLoggerRef(), nil)
	err = cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: filepath.Join(t.TempDir(), "x.mp4"), Duration: time.Second})
	if got := exitcode.Code(err); got != exitcode.ExitInvalid {
		t.Fatalf("VP8 into .mp4 exits %d (%v), want %d", got, err, exitcode.ExitInvalid)
	}
}

// listeningRecordingHost is a held camera that records video and, when its hold listens, audio.
type listeningRecordingHost struct {
	recordingHost
	ogg        string
	captureErr error
	captured   time.Duration
}

func (h *listeningRecordingHost) Capture(_ context.Context, _ string, from time.Time, d time.Duration) (hostclient.AudioCapture, error) {
	h.captured = d
	if h.captureErr != nil {
		return hostclient.AudioCapture{}, h.captureErr
	}
	data, err := os.ReadFile(h.ogg)
	if err != nil {
		return hostclient.AudioCapture{}, err
	}
	file, err := os.CreateTemp("", "held-audio-*.ogg")
	if err != nil {
		return hostclient.AudioCapture{}, err
	}
	_, _ = file.Write(data)
	_ = file.Close()
	if from.IsZero() {
		from = time.Now()
	}
	return hostclient.AudioCapture{Path: file.Name(), From: from, Until: from.Add(d), FirstPacketAt: h.clip.StartedAt, Packets: 150}, nil
}

func TestRecordUnderAListeningHoldAddsTheMicrophone(t *testing.T) {
	ogg := writeFile(t, "mic.ogg", oggClip(t, 3))
	host := &listeningRecordingHost{
		recordingHost: recordingHost{clip: hostclient.Recording{Path: rawH264(t, 3), Codec: videoCodecH264, FrameRate: 15, StartedAt: time.Now()}},
		ogg:           ogg,
	}
	formatter := &recordingFormatter{json: true}
	cmd := NewRecordCommand(nil, fixedResolver{host}, formatter, logging.NewLoggerRef(), nil)
	out := filepath.Join(t.TempDir(), "held.mp4")
	if err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: out, Duration: 3 * time.Second}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if host.captured != 3*time.Second+heldKeyframeSlack {
		t.Fatalf("captured %v of audio, want the video plus the keyframe slack", host.captured)
	}
	result := formatter.printed[0].(RecordResult)
	if !result.Success || !result.HasAudio || result.AudioCodec != "aac" || result.Message != "Recorded from the held camera with its microphone" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRecordUnderADeafHoldIsVideoOnly(t *testing.T) {
	host := &listeningRecordingHost{
		recordingHost: recordingHost{clip: hostclient.Recording{Path: rawH264(t, 2), Codec: videoCodecH264, FrameRate: 15, StartedAt: time.Now()}},
		captureErr:    &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), "ep/1", "hold_not_listening", "hold it with --listen")},
	}
	formatter := &recordingFormatter{json: true}
	cmd := NewRecordCommand(nil, fixedResolver{host}, formatter, logging.NewLoggerRef(), nil)
	if err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: filepath.Join(t.TempDir(), "held.mp4"), Duration: 2 * time.Second}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	result := formatter.printed[0].(RecordResult)
	if result.HasAudio || result.Message != "Recorded from the held camera, which does not listen (hold it with --listen for sound)" {
		t.Fatalf("result = %+v", result)
	}
}

func TestRecordAudioOnlyUnderAHold(t *testing.T) {
	host := &listeningRecordingHost{ogg: writeFile(t, "mic.ogg", oggClip(t, 2))}
	formatter := &recordingFormatter{json: true}
	cmd := NewRecordCommand(nil, fixedResolver{host}, formatter, logging.NewLoggerRef(), nil)
	out := filepath.Join(t.TempDir(), "room.wav")
	if err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: out, Duration: 2 * time.Second, AudioOnly: true}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if host.captured != 2*time.Second || host.duration != 0 {
		t.Fatalf("captured %v and recorded %v, want 2 s of audio and no video", host.captured, host.duration)
	}
	result := formatter.printed[0].(RecordResult)
	if !result.HasAudio || result.AudioCodec != "wav" || result.DurationSeconds < 1.9 || result.DurationSeconds > 2.2 {
		t.Fatalf("result = %+v", result)
	}

	err := cmd.Run(t.Context(), RecordParams{EndpointID: "ep/1", OutputPath: filepath.Join(t.TempDir(), "room.mp4"), Duration: time.Second, AudioOnly: true})
	if exitcode.Code(err) != exitcode.ExitInvalid {
		t.Fatalf("--audio-only into .mp4: %v, want exit %d", err, exitcode.ExitInvalid)
	}
}
