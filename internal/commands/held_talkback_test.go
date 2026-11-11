package commands

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// oggClip encodes seconds of a tone as Ogg/Opus with ffmpeg's default paging (many packets a page).
func oggClip(t *testing.T, seconds float64) []byte {
	t.Helper()
	needFFmpeg(t)
	path := filepath.Join(t.TempDir(), "clip.ogg")
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i",
		"sine=frequency=440:sample_rate=48000:d=" + strconv.FormatFloat(seconds, 'f', -1, 64), "-ac", "2", "-c:a", "libopus", path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestParseOggOpusReadsEveryPacket(t *testing.T) {
	packets, length, err := parseOggOpus(oggClip(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	// libopus pads the start with its pre-skip, so the clip runs a frame or so past a second.
	if length < time.Second || length > 1100*time.Millisecond {
		t.Fatalf("length = %s, want about a second", length)
	}
	if len(packets) < 50 {
		t.Fatalf("read %d packets, want one per 20 ms frame", len(packets))
	}
}

func TestParseOggOpusRefusesWhatItCannotPlay(t *testing.T) {
	cases := map[string][]byte{
		"not ogg":   []byte("RIFF....WAVEfmt "),
		"empty":     nil,
		"cut short": oggClip(t, 1)[:200],
	}
	for name, data := range cases {
		if _, _, err := parseOggOpus(data); !errors.Is(err, ErrClipInvalid) {
			t.Errorf("%s: err = %v, want ErrClipInvalid", name, err)
		}
	}
	if _, _, err := parseOggOpus(oggClip(t, 61)); !errors.Is(err, ErrClipInvalid) {
		t.Errorf("a 61 s clip: err = %v, want ErrClipInvalid", err)
	}
}

func TestHeldCameraPlaysAClipOnTheTalkbackTrack(t *testing.T) {
	held, camera := holdTestCamera(t, 6)
	clip := oggClip(t, 1)
	length, err := held.Play(t.Context(), clip)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if length < time.Second {
		t.Fatalf("Play reported %s, want the clip's length", length)
	}
	if _, err := held.Play(t.Context(), clip); !errors.Is(err, ErrTalkbackBusy) {
		t.Fatalf("a second clip while one plays: err = %v, want ErrTalkbackBusy", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for camera.heard.Load() < 40 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := camera.heard.Load(); got < 40 {
		t.Fatalf("the camera heard %d talkback packets, want the clip", got)
	}
	// Once the clip has played out, the track takes the next one.
	time.Sleep(length + talkbackDrain)
	if _, err := held.Play(t.Context(), clip); err != nil {
		t.Fatalf("Play after the first clip ended: %v", err)
	}
}
