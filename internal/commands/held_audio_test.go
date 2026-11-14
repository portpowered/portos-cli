package commands

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pion/rtp"
)

// opusSilence is one 20 ms stereo Opus frame.
var opusSilence = []byte{0xfc, 0xff, 0xfe}

func listeningCamera() *HeldCamera {
	return &HeldCamera{params: HeldCameraParams{Listen: true}, done: make(chan struct{})}
}

func opusPacket(seq uint16) *rtp.Packet {
	return &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 960, SSRC: 7}, Payload: opusSilence}
}

func TestHeldCameraKeepsTwoMinutesOfAudio(t *testing.T) {
	h := listeningCamera()
	start := time.Unix(1000, 0)
	h.receiveAudio(start, opusPacket(1))
	h.receiveAudio(start.Add(time.Minute), opusPacket(2))
	h.receiveAudio(start.Add(HeldAudioKeep+time.Second), opusPacket(3))
	if len(h.kept) != 2 || h.kept[0].packet.SequenceNumber != 2 {
		t.Fatalf("kept %d packets starting at %d, want the last two", len(h.kept), h.kept[0].packet.SequenceNumber)
	}
	if got := h.LastAudio(); !got.Equal(start.Add(HeldAudioKeep + time.Second)) {
		t.Fatalf("LastAudio = %v", got)
	}
}

func TestHeldCameraCaptureStartsInThePastAndRunsLive(t *testing.T) {
	h := listeningCamera()
	now := time.Now()
	h.receiveAudio(now.Add(-1500*time.Millisecond), opusPacket(1)) // before the window
	h.receiveAudio(now.Add(-500*time.Millisecond), opusPacket(2))  // kept, inside it
	from := now.Add(-time.Second)
	go func() {
		time.Sleep(50 * time.Millisecond)
		h.receiveAudio(time.Now(), opusPacket(3))
	}()
	audio, err := h.Capture(t.Context(), from, 1200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Remove()
	if audio.Packets != 2 || !audio.FirstPacketAt.Equal(now.Add(-500*time.Millisecond)) {
		t.Fatalf("capture = %+v, want the kept packet and the live one", audio)
	}
	if !audio.From.Equal(from) || !audio.Until.Equal(from.Add(1200*time.Millisecond)) {
		t.Fatalf("window = %v..%v", audio.From, audio.Until)
	}
	data, err := os.ReadFile(audio.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, length, err := parseOggOpus(data); err != nil || length != 40*time.Millisecond {
		t.Fatalf("the Ogg file holds %v of audio (%v), want two 20 ms frames", length, err)
	}
	if len(h.captures) != 0 {
		t.Fatal("the capture was left registered")
	}
}

func TestHeldCameraCaptureRefusals(t *testing.T) {
	deaf := &HeldCamera{done: make(chan struct{})}
	if _, err := deaf.Capture(t.Context(), time.Time{}, time.Second); !errors.Is(err, ErrNotListening) {
		t.Fatalf("a camera that does not listen: %v", err)
	}
	h := listeningCamera()
	for name, tc := range map[string]struct {
		from     time.Time
		duration time.Duration
	}{
		"before the kept audio": {time.Now().Add(-HeldAudioKeep - time.Second), time.Second},
		"too far ahead":         {time.Now().Add(2 * time.Minute), time.Second},
		"no duration":           {time.Time{}, 0},
		"too long":              {time.Time{}, HeldCaptureMax + time.Second},
	} {
		if _, err := h.Capture(t.Context(), tc.from, tc.duration); !errors.Is(err, ErrCaptureWindow) {
			t.Fatalf("%s: %v, want ErrCaptureWindow", name, err)
		}
	}
}

func TestHeldCameraCaptureEndsWithTheCamera(t *testing.T) {
	h := listeningCamera()
	close(h.done)
	if _, err := h.Capture(t.Context(), time.Time{}, time.Minute); err == nil {
		t.Fatal("a capture outlived its camera")
	}
}
