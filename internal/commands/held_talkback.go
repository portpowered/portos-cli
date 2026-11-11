package commands

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"go.uber.org/zap"
)

const (
	// TalkbackMaxDuration is the longest clip the held camera plays, the same limit as Port OS.
	TalkbackMaxDuration = 60 * time.Second
	// talkbackDrain lets the last packets reach the robot before the clip counts as played.
	talkbackDrain = 500 * time.Millisecond
	// talkbackClockRate and talkbackChannels are what the robot's talkback channel negotiates.
	talkbackClockRate = 48000
	talkbackChannels  = 2
)

var (
	// ErrTalkbackBusy is a clip sent while another is still playing on the held camera.
	ErrTalkbackBusy = errors.New("a clip is already playing on the held camera")
	// ErrClipInvalid is audio that is not an Ogg/Opus clip of at most TalkbackMaxDuration.
	ErrClipInvalid = errors.New("the clip is not playable Ogg/Opus audio")
)

// talkbackPacket is one Opus packet and how long it plays.
type talkbackPacket struct {
	data     []byte
	duration time.Duration
}

// addTalkback adds the send-only Opus track clips are written to. The camera never sends audio
// back on it, so holding the camera does not turn a robot's microphone on.
func (h *HeldCamera) addTalkback() error {
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: talkbackClockRate, Channels: talkbackChannels,
	}, "talkback", "portos-cli")
	if err != nil {
		return fmt.Errorf("create talkback track: %w", err)
	}
	transceiver, err := h.pc.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	if err != nil {
		return fmt.Errorf("add talkback transceiver: %w", err)
	}
	go func() {
		for {
			if _, _, err := transceiver.Sender().ReadRTCP(); err != nil {
				return
			}
		}
	}()
	h.talkback = track
	return nil
}

// Play starts an Ogg/Opus clip on the held camera's talkback track and returns its length once
// the first packet is sent. The clip plays on in real time; a second clip while it plays is
// ErrTalkbackBusy.
func (h *HeldCamera) Play(ctx context.Context, ogg []byte) (time.Duration, error) {
	packets, length, err := parseOggOpus(ogg)
	if err != nil {
		return 0, err
	}
	h.mu.Lock()
	switch {
	case h.closed:
		h.mu.Unlock()
		return 0, errors.New("the held camera is closed")
	case h.talkback == nil:
		h.mu.Unlock()
		return 0, errors.New("the held camera has no talkback track")
	case h.playing:
		h.mu.Unlock()
		return 0, ErrTalkbackBusy
	}
	h.playing = true
	track, life := h.talkback, h.life
	h.mu.Unlock()

	first := make(chan error, 1)
	go func() {
		err := sendClip(life, track, packets, first)
		h.mu.Lock()
		h.playing = false
		h.mu.Unlock()
		if err != nil && life.Err() == nil {
			h.recorder.logger.Logger().Warn("held camera clip ended early", zap.Error(err))
		}
	}()
	select {
	case err := <-first:
		if err != nil {
			return 0, err
		}
		return length, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// sendClip writes the packets on a real-time clock, reports the first write on first, and waits
// for the last packets to play out.
func sendClip(ctx context.Context, track *webrtc.TrackLocalStaticSample, packets []talkbackPacket, first chan<- error) error {
	start := time.Now()
	var played time.Duration
	for i, packet := range packets {
		err := track.WriteSample(media.Sample{Data: packet.data, Duration: packet.duration})
		if i == 0 {
			first <- err
		}
		if err != nil {
			return fmt.Errorf("write talkback packet: %w", err)
		}
		played += packet.duration
		if err := waitUntil(ctx, start.Add(played)); err != nil {
			return err
		}
	}
	return waitUntil(ctx, start.Add(played+talkbackDrain))
}

func waitUntil(ctx context.Context, moment time.Time) error {
	timer := time.NewTimer(time.Until(moment))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// parseOggOpus splits an Ogg/Opus clip into its audio packets (RFC 7845), skipping the OpusHead
// and OpusTags headers. Packets may span pages and pages may hold many packets.
func parseOggOpus(data []byte) ([]talkbackPacket, time.Duration, error) {
	var packets []talkbackPacket
	var length time.Duration
	var pending []byte
	headers := 0
	for len(data) > 0 {
		if len(data) < 27 || !bytes.Equal(data[:4], []byte("OggS")) {
			return nil, 0, fmt.Errorf("%w: not an Ogg page", ErrClipInvalid)
		}
		segments := int(data[26])
		if len(data) < 27+segments {
			return nil, 0, fmt.Errorf("%w: a page header is cut short", ErrClipInvalid)
		}
		lacing := data[27 : 27+segments]
		body := data[27+segments:]
		for _, size := range lacing {
			if len(body) < int(size) {
				return nil, 0, fmt.Errorf("%w: a page is cut short", ErrClipInvalid)
			}
			pending = append(pending, body[:size]...)
			body = body[size:]
			if size == 255 {
				continue // the packet goes on in the next segment
			}
			packet := pending
			pending = nil
			switch {
			case headers == 0:
				if !bytes.HasPrefix(packet, []byte("OpusHead")) || len(packet) < 19 {
					return nil, 0, fmt.Errorf("%w: it does not start with an Opus header", ErrClipInvalid)
				}
				headers++
			case headers == 1:
				if !bytes.HasPrefix(packet, []byte("OpusTags")) {
					return nil, 0, fmt.Errorf("%w: the Opus comment header is missing", ErrClipInvalid)
				}
				headers++
			case len(packet) > 0:
				duration, err := opusPacketDuration(packet)
				if err != nil {
					return nil, 0, err
				}
				packets = append(packets, talkbackPacket{data: packet, duration: duration})
				length += duration
				if length > TalkbackMaxDuration {
					return nil, 0, fmt.Errorf("%w: it is longer than %s", ErrClipInvalid, TalkbackMaxDuration)
				}
			}
		}
		data = body
	}
	if len(packets) == 0 {
		return nil, 0, fmt.Errorf("%w: it holds no audio", ErrClipInvalid)
	}
	return packets, length, nil
}

// opusPacketDuration reads a packet's table of contents (RFC 6716, section 3.1): the frame size
// from the configuration and the frame count from the code.
func opusPacketDuration(packet []byte) (time.Duration, error) {
	toc := packet[0]
	config := toc >> 3
	var micros int
	switch {
	case config < 12:
		micros = [...]int{10000, 20000, 40000, 60000}[config%4]
	case config < 16:
		micros = [...]int{10000, 20000}[config%2]
	default:
		micros = [...]int{2500, 5000, 10000, 20000}[config%4]
	}
	frames := 1
	switch toc & 3 {
	case 1, 2:
		frames = 2
	case 3:
		if len(packet) < 2 {
			return 0, fmt.Errorf("%w: a packet has no frame count", ErrClipInvalid)
		}
		frames = int(packet[1] & 0x3f)
	}
	duration := time.Duration(micros*frames) * time.Microsecond
	if frames == 0 || duration > 120*time.Millisecond {
		return 0, fmt.Errorf("%w: a packet has an impossible length", ErrClipInvalid)
	}
	return duration, nil
}
