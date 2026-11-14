package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"go.uber.org/zap"
)

const (
	// HeldAudioKeep is how much of the camera's microphone a listening hold keeps, so a capture
	// may start before it is asked for: play-audio --listen captures from the clip's start.
	HeldAudioKeep = 2 * time.Minute
	// HeldCaptureMax is the longest audio capture, the same limit as a held recording.
	HeldCaptureMax = HeldClipMax
	// heldCaptureAhead is how far in the future a capture may start.
	heldCaptureAhead = time.Minute
)

var (
	// ErrNotListening is an audio capture from a held camera that does not receive audio.
	ErrNotListening = errors.New("the held camera does not listen to the camera's microphone")
	// ErrCaptureWindow is a capture that starts before the kept audio or too far ahead.
	ErrCaptureWindow = errors.New("the capture window is outside the audio the held camera keeps")
)

// HeldAudio is microphone audio a listening held camera received, as Ogg/Opus at Path. From and
// Until are the window asked for; FirstPacketAt is when the first packet in it arrived, zero
// when none did. Remove deletes the file.
type HeldAudio struct {
	Path          string
	From          time.Time
	Until         time.Time
	FirstPacketAt time.Time
	Packets       int
}

// Remove deletes the capture's file.
func (a HeldAudio) Remove() { _ = os.Remove(a.Path) }

// heldAudioPacket is one received Opus packet and when it arrived.
type heldAudioPacket struct {
	at     time.Time
	packet *rtp.Packet
}

// heldCapture writes the packets that arrive inside its window to an Ogg file.
type heldCapture struct {
	from, until time.Time
	file        *os.File
	writer      *oggwriter.OggWriter
	first       time.Time
	packets     int
	err         error
}

func (c *heldCapture) write(p heldAudioPacket) {
	if c.err != nil || p.at.Before(c.from) || !p.at.Before(c.until) {
		return
	}
	if c.err = c.writer.WriteRTP(p.packet); c.err != nil {
		return
	}
	if c.first.IsZero() {
		c.first = p.at
	}
	c.packets++
}

// readAudio keeps the camera's Opus track in the rolling buffer and hands every packet to the
// captures in progress, until the stream ends.
func (h *HeldCamera) readAudio(ctx context.Context, track *webrtc.TrackRemote) {
	if !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypeOpus) {
		h.recorder.logger.Logger().Warn("held camera ignores a non-Opus audio track", zap.String("mime", track.Codec().MimeType))
		return
	}
	deadline := time.Now().Add(h.params.Lifetime + time.Minute)
	err := readTrack(ctx, track, fixedDeadline(deadline), func(packet *rtp.Packet) error {
		h.receiveAudio(time.Now(), packet)
		return nil
	})
	if err != nil && ctx.Err() == nil {
		h.recorder.logger.Logger().Warn("held camera audio stopped", zap.Error(err))
	}
}

// receiveAudio keeps one packet for HeldAudioKeep and gives it to every capture in progress.
func (h *HeldCamera) receiveAudio(now time.Time, packet *rtp.Packet) {
	p := heldAudioPacket{at: now, packet: packet}
	h.audioMu.Lock()
	defer h.audioMu.Unlock()
	h.lastAudio = now
	h.kept = append(h.kept, p)
	cut := 0
	for cut < len(h.kept) && now.Sub(h.kept[cut].at) > HeldAudioKeep {
		cut++
	}
	h.kept = h.kept[cut:]
	for _, c := range h.captures {
		c.write(p)
	}
}

// Capture returns the microphone audio that arrives from `from` (now when zero) for the
// duration, as Ogg/Opus. The window may start up to HeldAudioKeep in the past, so audio that
// already arrived is included. It returns once the window has passed.
func (h *HeldCamera) Capture(ctx context.Context, from time.Time, duration time.Duration) (HeldAudio, error) {
	if !h.params.Listen {
		return HeldAudio{}, ErrNotListening
	}
	if duration <= 0 || duration > HeldCaptureMax {
		return HeldAudio{}, fmt.Errorf("%w: a capture lasts between 1 second and %v", ErrCaptureWindow, HeldCaptureMax)
	}
	now := time.Now()
	if from.IsZero() {
		from = now
	}
	if now.Sub(from) > HeldAudioKeep || from.Sub(now) > heldCaptureAhead {
		return HeldAudio{}, fmt.Errorf("%w: it may start at most %v ago and %v ahead", ErrCaptureWindow, HeldAudioKeep, heldCaptureAhead)
	}
	c, err := h.addCapture(from, from.Add(duration))
	if err != nil {
		return HeldAudio{}, err
	}
	timer := time.NewTimer(time.Until(c.until))
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		err = ctx.Err()
	case <-h.done:
		err = errors.New("the held camera stopped during the capture")
	}
	h.removeCapture(c)
	if err == nil {
		err = c.err
	}
	_ = c.writer.Close()
	_ = c.file.Close()
	audio := HeldAudio{Path: c.file.Name(), From: c.from, Until: c.until, FirstPacketAt: c.first, Packets: c.packets}
	if err != nil {
		audio.Remove()
		return HeldAudio{}, err
	}
	return audio, nil
}

// addCapture starts a capture and fills it with the kept packets already in its window.
func (h *HeldCamera) addCapture(from, until time.Time) (*heldCapture, error) {
	file, err := os.CreateTemp("", "portos-held-audio-*.ogg")
	if err != nil {
		return nil, fmt.Errorf("create audio capture: %w", err)
	}
	writer, err := oggwriter.NewWith(file, opusClockRate, opusChannels)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("create audio capture: %w", err)
	}
	c := &heldCapture{from: from, until: until, file: file, writer: writer}
	h.audioMu.Lock()
	defer h.audioMu.Unlock()
	for _, p := range h.kept {
		c.write(p)
	}
	h.captures = append(h.captures, c)
	return c, nil
}

func (h *HeldCamera) removeCapture(c *heldCapture) {
	h.audioMu.Lock()
	defer h.audioMu.Unlock()
	for i, other := range h.captures {
		if other == c {
			h.captures = append(h.captures[:i], h.captures[i+1:]...)
			return
		}
	}
}

// LastAudio is when the newest microphone packet arrived; zero before the first.
func (h *HeldCamera) LastAudio() time.Time {
	h.audioMu.Lock()
	defer h.audioMu.Unlock()
	return h.lastAudio
}
