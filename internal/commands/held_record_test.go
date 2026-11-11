package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHeldCameraRecordsWithoutANewSession(t *testing.T) {
	held, camera := holdTestCamera(t, 8)
	clip, err := held.Record(t.Context(), 3*time.Second)
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	defer clip.Remove()
	if clip.Codec != videoCodecH264 {
		t.Fatalf("codec %q, want H264", clip.Codec)
	}
	if clip.FrameRate < 14 || clip.FrameRate > 16 {
		t.Fatalf("frame rate %v, want about 15", clip.FrameRate)
	}
	if got := camera.lifetime.Load(); got != 1800 {
		t.Fatalf("the camera saw a %d s session request, want only the hold's", got)
	}
	out := filepath.Join(t.TempDir(), "held.mp4")
	args, err := buildMuxArgs(muxPlan{VideoPath: clip.Path, VideoCodec: clip.Codec, FrameRate: clip.FrameRate, OutputPath: out, Duration: 3 * time.Second})
	if err != nil {
		t.Fatalf("buildMuxArgs: %v", err)
	}
	if output, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("mux: %v: %s", err, output)
	}
	seconds, err := probeDuration(t.Context(), out)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if seconds < 2.5 || seconds > 3.1 {
		t.Fatalf("the clip plays %.2f s, want about 3", seconds)
	}
	held.mu.Lock()
	taps := len(held.taps)
	held.mu.Unlock()
	if taps != 0 {
		t.Fatalf("%d taps left after the recording", taps)
	}
}

func TestHeldCameraRecordRefusals(t *testing.T) {
	held, _ := holdTestCamera(t, 6)
	if _, err := held.Record(t.Context(), 0); err == nil {
		t.Fatal("a zero-length recording was accepted")
	}
	if _, err := held.Record(t.Context(), HeldClipMax+time.Second); err == nil {
		t.Fatal("a recording over the limit was accepted")
	}
	go func() {
		time.Sleep(time.Second)
		_ = held.Close(t.Context())
	}()
	clip, err := held.Record(t.Context(), 4*time.Second)
	if err == nil {
		clip.Remove()
		t.Fatal("a recording survived the camera closing")
	}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "portos-held-record-*"))
	for _, m := range matches {
		if info, statErr := os.Stat(m); statErr == nil && time.Since(info.ModTime()) < 10*time.Second {
			t.Fatalf("a failed recording left %s behind", m)
		}
	}
}
