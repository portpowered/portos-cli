package commands

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
	"go.uber.org/zap"
)

const (
	opusClockRate = 48000
	opusChannels  = 2
)

// RecorderResult is the result of a successful recording. TempPath is the video (IVF or raw
// H264); AudioPath is an Ogg/Opus file and is empty when the device sent no audio. The caller
// must call Remove when done.
type RecorderResult struct {
	TempPath   string
	VideoCodec string        // videoCodecH264 or videoCodecVP8
	FrameRate  float64       // measured from the RTP clock; 0 when too few frames arrived
	VideoStart time.Time     // when the first recorded frame arrived
	VideoSpan  time.Duration // RTP time from first to last frame, plus one frame
	AudioPath  string
	AudioStart time.Time
}

// HasAudio reports whether an audio file was recorded.
func (r *RecorderResult) HasAudio() bool { return r.AudioPath != "" }

// Remove deletes the recorded temp files.
func (r *RecorderResult) Remove() {
	for _, path := range []string{r.TempPath, r.AudioPath} {
		if path != "" {
			_ = os.Remove(path)
		}
	}
}

// recordState is the shared state between Run and the OnTrack recording workers.
type recordState struct {
	params RecorderParams

	mu           sync.Mutex
	started      bool
	shuttingDown bool
	deadline     time.Time // one capture window for both tracks, from the first track's arrival
	audioStarted bool
	audioPath    string
	audioStart   time.Time

	done      chan struct{} // closed when the video worker returns
	startedCh chan struct{} // closed when the video track arrives
	recordCh  chan recordingResult
	audioDone chan struct{} // closed when the audio worker returns
}

func newRecordState(params RecorderParams) *recordState {
	return &recordState{
		params:    params,
		done:      make(chan struct{}),
		recordCh:  make(chan recordingResult, 1),
		startedCh: make(chan struct{}),
		audioDone: make(chan struct{}),
	}
}

// arm fixes the shared deadline on the first track and returns it. Callers hold st.mu.
func (st *recordState) arm() time.Time {
	if st.deadline.IsZero() {
		st.deadline = time.Now().Add(st.params.Duration)
	}
	return st.deadline
}

func (st *recordState) shutdown(cancelRecord context.CancelFunc, pc *webrtc.PeerConnection, delivered *bool) {
	st.mu.Lock()
	st.shuttingDown = true
	running, audioRunning := st.started, st.audioStarted
	st.mu.Unlock()
	cancelRecord()
	_ = pc.Close()
	if running {
		<-st.done
	}
	if audioRunning {
		<-st.audioDone
	}
	select {
	case result := <-st.recordCh:
		if result.path != "" {
			_ = os.Remove(result.path)
		}
	default:
	}
	if !*delivered && st.audioPath != "" {
		_ = os.Remove(st.audioPath)
	}
}

// finish waits for the audio worker (it ends at the same deadline as the video) and assembles
// the result. On failure it removes the video file it was handed.
func (st *recordState) finish(ctx context.Context, video recordingResult) (*RecorderResult, error) {
	st.mu.Lock()
	audioRunning := st.audioStarted
	st.mu.Unlock()
	if audioRunning {
		select {
		case <-ctx.Done():
			_ = os.Remove(video.path)
			return nil, ctx.Err()
		case <-st.audioDone:
		}
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return &RecorderResult{
		TempPath: video.path, VideoCodec: video.codec, FrameRate: video.frameRate,
		VideoStart: video.start, VideoSpan: video.span,
		AudioPath: st.audioPath, AudioStart: st.audioStart,
	}, nil
}

// addOpusReceiver offers receive-only Opus so a negotiated audio track is always Opus.
func addOpusReceiver(pc *webrtc.PeerConnection) error {
	transceiver, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	})
	if err != nil {
		return fmt.Errorf("add audio transceiver: %w", err)
	}
	opus := webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: opusClockRate, Channels: opusChannels,
			SDPFmtpLine: "minptime=10;useinbandfec=1",
		},
		PayloadType: 111,
	}
	if err := transceiver.SetCodecPreferences([]webrtc.RTPCodecParameters{opus}); err != nil {
		return fmt.Errorf("prefer Opus audio: %w", err)
	}
	return nil
}

type trackWriter interface {
	WriteRTP(*rtp.Packet) error
	Close() error
}

// frameWriter is a trackWriter that reports the RTP timestamp of every frame it wrote. Writers
// drop packets before the first keyframe and incomplete frames, so only these frames count
// toward the file's length and frame rate.
type frameWriter interface {
	trackWriter
	WrittenStamps() []uint32
}

func newTrackWriter(tmpFile *os.File, ext string) (frameWriter, error) {
	if ext != "ivf" {
		return newRecordingH264Writer(tmpFile), nil
	}
	return newRecordingVP8Writer(tmpFile)
}

func (r *Recorder) handleTrack(recordCtx context.Context, st *recordState, track *webrtc.TrackRemote) {
	r.logger.Logger().Info("received track", zap.String("kind", track.Kind().String()))
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		r.startVideo(recordCtx, st, track)
	case webrtc.RTPCodecTypeAudio:
		r.startAudio(recordCtx, st, track)
	}
}

func (r *Recorder) startVideo(recordCtx context.Context, st *recordState, track *webrtc.TrackRemote) {
	st.mu.Lock()
	if st.started || st.shuttingDown {
		st.mu.Unlock()
		return
	}
	st.started = true
	deadline := st.arm()
	st.mu.Unlock()
	defer close(st.done)
	close(st.startedCh)
	r.recordVideoTrack(recordCtx, st, deadline, track)
}

func (r *Recorder) startAudio(recordCtx context.Context, st *recordState, track *webrtc.TrackRemote) {
	if !st.params.Audio || !strings.Contains(strings.ToLower(track.Codec().MimeType), "opus") {
		r.logger.Logger().Warn("ignoring audio track", zap.String("mime", track.Codec().MimeType))
		return
	}
	st.mu.Lock()
	if st.audioStarted || st.shuttingDown {
		st.mu.Unlock()
		return
	}
	st.audioStarted = true
	deadline := st.arm()
	st.mu.Unlock()
	defer close(st.audioDone)
	r.recordAudioTrack(recordCtx, st, deadline, track)
}

// readTrack feeds every packet to handle until the deadline, cancellation or a read error.
// Reaching the deadline is not an error.
func readTrack(ctx context.Context, track *webrtc.TrackRemote, deadline time.Time, handle func(*rtp.Packet) error) error {
	_ = track.SetReadDeadline(deadline)
	stopInterrupt := context.AfterFunc(ctx, func() { _ = track.SetReadDeadline(time.Now()) })
	defer stopInterrupt()
	for time.Now().Before(deadline) {
		packet, _, err := track.ReadRTP()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() && !time.Now().Before(deadline) {
				return nil
			}
			return err
		}
		if err := handle(packet); err != nil {
			return err
		}
	}
	return nil
}

// videoFormat maps a negotiated MIME type to the temp-file extension and codec name.
func videoFormat(mime string) (ext, name string, err error) {
	switch {
	case strings.Contains(mime, "vp8"):
		return "ivf", videoCodecVP8, nil
	case strings.Contains(mime, "h264"):
		return "h264", videoCodecH264, nil
	}
	return "", "", fmt.Errorf("unsupported video codec: %s (VP8 or H264 required)", mime)
}

// clipStart is when the recorded clip begins. A writer that waits for a keyframe starts the
// clip there, not at the first packet.
func clipStart(written []uint32, firstSeen time.Time, firstTS uint32) time.Time {
	if len(written) > 0 && written[0]-firstTS < 1<<31 {
		return firstSeen.Add(time.Duration(float64(written[0]-firstTS) / videoRTPClock * float64(time.Second)))
	}
	return firstSeen
}

func (r *Recorder) recordVideoTrack(recordCtx context.Context, st *recordState, deadline time.Time, track *webrtc.TrackRemote) {
	mime := strings.ToLower(track.Codec().MimeType)
	r.logger.Logger().Info("video codec negotiated", zap.String("mime", track.Codec().MimeType))

	ext, name, err := videoFormat(mime)
	if err != nil {
		st.recordCh <- recordingResult{err: err}
		return
	}
	if check := st.params.CheckVideoCodec; check != nil {
		if err := check(name); err != nil {
			st.recordCh <- recordingResult{err: err}
			return
		}
	}

	tmpFile, err := os.CreateTemp("", "portos-record-*."+ext)
	if err != nil {
		st.recordCh <- recordingResult{err: fmt.Errorf("create temp file: %w", err)}
		return
	}
	recordPath := tmpFile.Name()
	transferred := false
	defer func() {
		_ = tmpFile.Close()
		if !transferred {
			_ = os.Remove(recordPath)
		}
	}()

	writer, err := newTrackWriter(tmpFile, ext)
	if err != nil {
		st.recordCh <- recordingResult{err: err}
		return
	}
	defer func() { _ = writer.Close() }()

	var (
		firstSeen time.Time
		firstTS   uint32
	)
	err = readTrack(recordCtx, track, deadline, func(packet *rtp.Packet) error {
		if firstSeen.IsZero() {
			firstSeen, firstTS = time.Now(), packet.Timestamp
		}
		if err := writer.WriteRTP(packet); err != nil {
			return fmt.Errorf("write RTP: %w", err)
		}
		return nil
	})
	if err != nil {
		if recordCtx.Err() == nil {
			r.logger.Logger().Error("error reading RTP packet", zap.Error(err))
		}
		st.recordCh <- recordingResult{err: err}
		return
	}
	if err := writer.Close(); err != nil {
		st.recordCh <- recordingResult{err: fmt.Errorf("close writer: %w", err)}
		return
	}
	var clock frameClock
	for _, stamp := range writer.WrittenStamps() {
		clock.observe(stamp)
	}
	start := clipStart(writer.WrittenStamps(), firstSeen, firstTS)
	rate := clock.frameRate()
	span := clock.span()
	if rate > 0 {
		span += time.Duration(float64(time.Second) / rate)
	}
	r.logger.Logger().Debug("recorded video to temp file", zap.String("path", recordPath), zap.Float64("fps", rate))
	transferred = true
	st.recordCh <- recordingResult{path: recordPath, codec: name, frameRate: rate, start: start, span: span}
}

// recordAudioTrack writes the Opus track to an Ogg file. Audio is best effort: any failure
// leaves the recording video-only.
func (r *Recorder) recordAudioTrack(recordCtx context.Context, st *recordState, deadline time.Time, track *webrtc.TrackRemote) {
	tmpFile, err := os.CreateTemp("", "portos-record-*.ogg")
	if err != nil {
		r.logger.Logger().Warn("audio temp file", zap.Error(err))
		return
	}
	path := tmpFile.Name()
	writer, err := oggwriter.NewWith(tmpFile, opusClockRate, opusChannels)
	if err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(path)
		r.logger.Logger().Warn("audio ogg writer", zap.Error(err))
		return
	}
	var first time.Time
	err = readTrack(recordCtx, track, deadline, func(packet *rtp.Packet) error {
		if first.IsZero() {
			first = time.Now()
		}
		return writer.WriteRTP(packet)
	})
	if err != nil {
		r.logger.Logger().Warn("audio recording ended early", zap.Error(err))
	}
	_ = writer.Close()
	_ = tmpFile.Close()
	if first.IsZero() {
		_ = os.Remove(path)
		return
	}
	st.mu.Lock()
	st.audioPath, st.audioStart = path, first
	st.mu.Unlock()
}
