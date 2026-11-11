package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/pion/rtp"
)

// HeldClipMax is the longest clip a held camera records in one go.
const HeldClipMax = 10 * time.Minute

// HeldClip is video recorded from a held camera: raw H264 or IVF at Path, played at FrameRate.
// The held peer receives no audio, so a held clip has none. Remove deletes the file.
type HeldClip struct {
	Path      string
	Codec     string
	FrameRate float64
}

// Remove deletes the clip's file.
func (c HeldClip) Remove() { _ = os.Remove(c.Path) }

// heldTap copies the held video into its own file from the next keyframe for a duration.
type heldTap struct {
	file     *os.File
	writer   frameWriter
	duration time.Duration
	until    time.Time // set at the first written frame
	done     chan struct{}
}

// Record copies the held video into a new file for the duration, starting at the next keyframe,
// without opening another camera session. It returns once the duration has passed.
func (h *HeldCamera) Record(ctx context.Context, duration time.Duration) (HeldClip, error) {
	if duration <= 0 || duration > HeldClipMax {
		return HeldClip{}, fmt.Errorf("a held recording lasts between 1 second and %v", HeldClipMax)
	}
	tap, codec, err := h.addTap(duration)
	if err != nil {
		return HeldClip{}, err
	}
	h.requestKeyframe()
	timer := time.NewTimer(duration + frameWaitTimeout)
	defer timer.Stop()
	select {
	case <-tap.done:
	case <-ctx.Done():
		err = ctx.Err()
	case <-h.done:
		err = errors.New("the held camera stopped during the recording")
	case <-timer.C:
		err = errors.New("the held camera sent no video for the recording")
	}
	h.removeTap(tap)
	stamps := tap.writer.WrittenStamps()
	_ = tap.writer.Close()
	_ = tap.file.Close()
	clip := HeldClip{Path: tap.file.Name(), Codec: codec, FrameRate: measureFrameRate(stamps)}
	if err == nil && clip.FrameRate <= 0 {
		err = errors.New("the held camera sent too few video frames to time the recording")
	}
	if err != nil {
		clip.Remove()
		return HeldClip{}, err
	}
	return clip, nil
}

func (h *HeldCamera) addTap(duration time.Duration) (*heldTap, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, "", errors.New("the held camera is closed")
	}
	if h.ext == "" {
		return nil, "", errors.New("the held camera has no video yet")
	}
	codec := videoCodecH264
	if h.ext == "ivf" {
		codec = videoCodecVP8
	}
	file, err := os.CreateTemp("", "portos-held-record-*."+h.ext)
	if err != nil {
		return nil, "", fmt.Errorf("create recording: %w", err)
	}
	writer, err := newTrackWriter(file, h.ext)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, "", err
	}
	tap := &heldTap{file: file, writer: writer, duration: duration, done: make(chan struct{})}
	h.taps = append(h.taps, tap)
	return tap, codec, nil
}

func (h *HeldCamera) removeTap(tap *heldTap) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, t := range h.taps {
		if t == tap {
			h.taps = append(h.taps[:i], h.taps[i+1:]...)
			return
		}
	}
}

// writeTapsLocked gives the packet to every tap, and ends a tap whose duration has passed.
// Callers hold h.mu.
func (h *HeldCamera) writeTapsLocked(packet *rtp.Packet, now time.Time) error {
	kept := h.taps[:0]
	for _, tap := range h.taps {
		if !tap.until.IsZero() && !now.Before(tap.until) {
			close(tap.done)
			continue
		}
		before := len(tap.writer.WrittenStamps())
		if err := tap.writer.WriteRTP(packet); err != nil {
			return fmt.Errorf("write recording: %w", err)
		}
		if tap.until.IsZero() && len(tap.writer.WrittenStamps()) > before {
			tap.until = now.Add(tap.duration)
		}
		kept = append(kept, tap)
	}
	h.taps = kept
	return nil
}
