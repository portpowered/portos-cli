package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"go.uber.org/zap"
)

const (
	// heldSegmentSpan is how much video a held camera keeps per segment. A snapshot decodes at
	// most two segments' worth, so it stays well under a second.
	heldSegmentSpan = 4 * time.Second
	// heldFrameWait bounds how long a snapshot waits for a fresh frame after asking for a keyframe.
	heldFrameWait = 2 * time.Second
)

// HeldCameraParams configure HoldCamera.
type HeldCameraParams struct {
	EndpointID    string
	SessionDomain string
	SessionID     string
	// Lifetime is the camera session length asked of Port OS; the caller renews before it ends.
	Lifetime  time.Duration
	RelayOnly bool
}

// HeldCamera is a camera peer the session host keeps open. It keeps the newest few seconds of
// video so a snapshot is one local decode, with no new camera session, and plays clips on a
// send-only talkback track.
type HeldCamera struct {
	recorder *Recorder
	params   HeldCameraParams
	pc       *webrtc.PeerConnection
	life     context.Context // ends when the camera closes
	cancel   context.CancelFunc
	dir      string
	talkback *webrtc.TrackLocalStaticSample

	done     chan struct{}
	doneOnce sync.Once

	mu         sync.Mutex
	ext        string
	ssrc       webrtc.SSRC
	current    *heldSegment
	previous   *heldSegment
	lastFrame  time.Time
	frameReady chan struct{} // closed and replaced on every new frame
	counter    int
	closed     bool
	playing    bool
	taps       []*heldTap // recordings in progress
}

// heldSegment is one file of video that starts at a keyframe.
type heldSegment struct {
	path    string
	file    *os.File
	writer  frameWriter
	started time.Time
	frames  int
}

// HoldCamera opens a camera session with a receive-only video track and a send-only talkback
// track, and returns once the first frame has arrived. Close ends the session.
func (r *Recorder) HoldCamera(ctx context.Context, params HeldCameraParams) (*HeldCamera, error) {
	if params.EndpointID == "" || params.SessionID == "" {
		return nil, errors.New("endpoint ID and session ID are required")
	}
	if params.Lifetime < time.Second {
		return nil, errors.New("the camera session lifetime must be at least a second")
	}
	config, err := r.iceConfiguration(ctx, params.RelayOnly)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "portos-held-camera-*")
	if err != nil {
		return nil, fmt.Errorf("create frame directory: %w", err)
	}
	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	pc, err := r.peerConnectionAPI(streamCtx).NewPeerConnection(config)
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("create peer connection: %w", err)
	}
	h := &HeldCamera{recorder: r, params: params, pc: pc, life: streamCtx, cancel: cancel, dir: dir, done: make(chan struct{}), frameReady: make(chan struct{})}
	opened, initiated := false, false
	defer func() {
		switch {
		case opened:
		case initiated:
			if err := h.Close(ctx); err != nil {
				r.logger.Logger().Warn("disconnect held camera session", zap.Error(err))
			}
		default:
			h.shutdown(ctx)
		}
	}()

	started := make(chan error, 1)
	if err = h.addTracks(started); err != nil {
		return nil, err
	}
	offerSDP, err := gatherOffer(ctx, pc)
	if err != nil {
		return nil, err
	}
	response, err := r.apiClient.Messages().Send(ctx, sessionRequest(params.EndpointID, params.SessionDomain, params.SessionID, params.Lifetime, offerSDP))
	if err != nil {
		return nil, fmt.Errorf("send initiate session: %w", err)
	}
	initiated = true
	if err = r.applyAnswer(pc, response); err != nil {
		return nil, err
	}
	timer := time.NewTimer(frameWaitTimeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("the camera sent no video frame in time")
	case err := <-started:
		if err != nil {
			return nil, err
		}
	}
	opened = true
	return h, nil
}

// addTracks offers the receive-only video and the send-only talkback track, and reads the video
// into segments once it arrives. started receives the first frame's outcome.
func (h *HeldCamera) addTracks(started chan<- error) error {
	if _, err := h.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return fmt.Errorf("add video transceiver: %w", err)
	}
	if err := h.addTalkback(); err != nil {
		return err
	}
	h.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		h.recorder.logger.Logger().Info("held camera connection state changed", zap.String("state", state.String()))
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			h.markDone()
		}
	})
	h.pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if track.Kind() != webrtc.RTPCodecTypeVideo {
			return
		}
		h.readVideo(h.life, track, started)
	})
	return nil
}

// readVideo writes the track into rolling segments until the stream ends. started receives nil
// at the first frame, or the error that stopped the track before one arrived.
func (h *HeldCamera) readVideo(ctx context.Context, track *webrtc.TrackRemote, started chan<- error) {
	defer h.markDone()
	ext, _, err := videoFormat(strings.ToLower(track.Codec().MimeType))
	if err != nil {
		started <- err
		return
	}
	h.mu.Lock()
	h.ext, h.ssrc = ext, track.SSRC()
	h.mu.Unlock()
	first := true
	deadline := time.Now().Add(h.params.Lifetime + time.Minute)
	err = readTrack(ctx, track, fixedDeadline(deadline), func(packet *rtp.Packet) error {
		framed, err := h.write(packet)
		if err == nil && framed && first {
			first = false
			started <- nil
		}
		return err
	})
	if first {
		if err == nil {
			err = errors.New("the camera video track ended")
		}
		started <- err
	}
	if err != nil && ctx.Err() == nil {
		h.recorder.logger.Logger().Warn("held camera video stopped", zap.Error(err))
	}
}

// write adds one packet to the current segment, rotating to a new segment when the current one
// is full. Until the new segment has its first frame (it waits for a keyframe), packets also go
// to the previous one, so the newest frame is always in a decodable file. It reports whether
// the packet completed a frame.
func (h *HeldCamera) write(packet *rtp.Packet) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false, errors.New("the held camera is closed")
	}
	now := time.Now()
	if h.current == nil || (h.current.frames > 0 && now.Sub(h.current.started) >= heldSegmentSpan) {
		segment, err := h.newSegmentLocked(now)
		if err != nil {
			return false, err
		}
		h.previous.remove()
		h.previous, h.current = h.current, segment
		go h.requestKeyframe()
	}
	framed := false
	targets := []*heldSegment{h.current}
	if h.current.frames == 0 && h.previous != nil {
		targets = append(targets, h.previous)
	}
	for _, segment := range targets {
		before := len(segment.writer.WrittenStamps())
		if err := segment.writer.WriteRTP(packet); err != nil {
			return false, fmt.Errorf("write RTP: %w", err)
		}
		if added := len(segment.writer.WrittenStamps()) - before; added > 0 {
			segment.frames += added
			framed = true
		}
	}
	if err := h.writeTapsLocked(packet, now); err != nil {
		return false, err
	}
	if framed {
		h.lastFrame = now
		close(h.frameReady)
		h.frameReady = make(chan struct{})
	}
	return framed, nil
}

func (h *HeldCamera) newSegmentLocked(now time.Time) (*heldSegment, error) {
	h.counter++
	path := filepath.Join(h.dir, fmt.Sprintf("segment-%d.%s", h.counter, h.ext))
	file, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("create segment: %w", err)
	}
	writer, err := newTrackWriter(file, h.ext)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &heldSegment{path: path, file: file, writer: writer, started: now}, nil
}

func (s *heldSegment) remove() {
	if s == nil {
		return
	}
	_ = s.writer.Close()
	_ = s.file.Close()
	_ = os.Remove(s.path)
}

// requestKeyframe asks the camera for a keyframe so a new segment starts promptly.
func (h *HeldCamera) requestKeyframe() {
	h.mu.Lock()
	ssrc := h.ssrc
	h.mu.Unlock()
	_ = h.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}})
}

// Latest returns the newest frame as JPEG and when it arrived. A frame older than maxAge makes it
// ask for a keyframe and wait briefly for a newer one; it then returns the newest it has.
func (h *HeldCamera) Latest(ctx context.Context, maxAge time.Duration) ([]byte, time.Time, error) {
	h.mu.Lock()
	ready, last := h.frameReady, h.lastFrame
	h.mu.Unlock()
	if time.Since(last) > maxAge {
		h.requestKeyframe()
		timer := time.NewTimer(heldFrameWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, time.Time{}, ctx.Err()
		case <-h.done:
		case <-ready:
		case <-timer.C:
		}
		timer.Stop()
	}
	clip, ext, at, err := h.copyNewest()
	if err != nil {
		return nil, time.Time{}, err
	}
	defer func() { _ = os.Remove(clip) }()
	jpeg, err := lastFrameJPEG(ctx, clip, ext)
	if err != nil {
		return nil, time.Time{}, err
	}
	return jpeg, at, nil
}

// copyNewest copies the newest segment that has a frame, so the decode reads a stable file.
func (h *HeldCamera) copyNewest() (string, string, time.Time, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", "", time.Time{}, errors.New("the held camera is closed")
	}
	segment := h.current
	if segment == nil || segment.frames == 0 {
		segment = h.previous
	}
	if segment == nil || segment.frames == 0 {
		return "", "", time.Time{}, errors.New("the held camera has no frame yet")
	}
	data, err := os.ReadFile(segment.path)
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("read segment: %w", err)
	}
	h.counter++
	clip := filepath.Join(h.dir, fmt.Sprintf("snapshot-%d.%s", h.counter, h.ext))
	if err := os.WriteFile(clip, data, 0o600); err != nil {
		return "", "", time.Time{}, fmt.Errorf("copy segment: %w", err)
	}
	return clip, h.ext, h.lastFrame, nil
}

// lastFrameJPEG decodes a short clip and returns its last frame as JPEG.
func lastFrameJPEG(ctx context.Context, clip, ext string) ([]byte, error) {
	out := clip + ".jpg"
	defer func() { _ = os.Remove(out) }()
	format := "h264"
	if ext == "ivf" {
		format = "ivf"
	}
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-f", format, "-i", safePathArg(clip), "-update", "1", "-q:v", "2", safePathArg(out)}
	if output, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode frame: %w: %s", err, strings.TrimSpace(string(output)))
	}
	jpeg, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("read frame: %w", err)
	}
	return jpeg, nil
}

// Done is closed when the stream stops: the far side closed it, the connection failed, or Close.
func (h *HeldCamera) Done() <-chan struct{} { return h.done }

func (h *HeldCamera) markDone() { h.doneOnce.Do(func() { close(h.done) }) }

// Close disconnects the camera session and removes the kept video.
func (h *HeldCamera) Close(ctx context.Context) error {
	h.shutdown(ctx)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rtcSessionCleanupTimeout)
	defer cancel()
	return h.recorder.sendDisconnect(cleanupCtx, h.params.EndpointID, h.params.SessionID)
}

func (h *HeldCamera) shutdown(context.Context) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.current.remove()
	h.previous.remove()
	h.current, h.previous = nil, nil
	h.mu.Unlock()
	h.cancel()
	_ = h.pc.Close()
	h.markDone()
	_ = os.RemoveAll(h.dir)
}
