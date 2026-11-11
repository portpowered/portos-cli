// Package camera keeps a held endpoint's camera session open for the session
// host: it opens the stream, renews it before Port OS ends it, serves the
// newest frame, plays clips on its talkback track, and reports a stream
// another viewer took away.
package camera

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

const (
	// Lifetime is the camera session length the host asks for. Roborock allows an hour.
	Lifetime = 30 * time.Minute
	// renewBefore is how long before the session ends the host opens its replacement.
	renewBefore = 2 * time.Minute
	// FreshFrame is the age under which a snapshot uses the newest frame as is.
	FreshFrame = time.Second
	// renewTimeout bounds opening the replacement stream.
	renewTimeout = time.Minute
)

// Errors a Stream's Play returns.
var (
	// ErrTalkbackBusy is a clip sent while another is still playing.
	ErrTalkbackBusy = errors.New("a clip is already playing on the held camera")
	// ErrClipInvalid is audio the talkback track cannot play.
	ErrClipInvalid = errors.New("the clip is not playable Ogg/Opus audio")
)

// Stream is one open camera session.
type Stream interface {
	// Latest returns the newest frame as JPEG and when it arrived.
	Latest(ctx context.Context, maxAge time.Duration) ([]byte, time.Time, error)
	// Play starts an Ogg/Opus clip on the talkback track and returns its length.
	Play(ctx context.Context, ogg []byte) (time.Duration, error)
	// Record copies the video into a file for the duration and returns it.
	Record(ctx context.Context, duration time.Duration) (Clip, error)
	// Done is closed when the stream stops for any reason.
	Done() <-chan struct{}
	Close(ctx context.Context) error
}

// Opener opens a camera session on an endpoint for the given lifetime.
type Opener interface {
	Open(ctx context.Context, endpointID string, lifetime time.Duration) (Stream, error)
}

// LostFunc is told when a hold's camera stops on its own: reason is
// holds.ReasonPreempted when the far side closed it, holds.ReasonEndpointError
// when renewal failed.
type LostFunc func(holdID string, reason holds.Reason, detail string)

// Frame is one snapshot.
type Frame struct {
	JPEG       []byte
	CapturedAt time.Time
}

// Clip is video recorded from a held camera: raw H264 or IVF at Path, to be played at
// FrameRate. The held camera receives no audio. The receiver removes Path.
type Clip struct {
	Path      string
	Codec     string
	FrameRate float64
}

// Status is a hold's camera as `port session status` shows it.
type Status struct {
	Live       bool       `json:"live"`
	OpenedAt   time.Time  `json:"openedAt"`
	RenewsAt   time.Time  `json:"renewsAt"`
	Renewals   int        `json:"renewals"`
	LastFrame  *time.Time `json:"lastFrameAt,omitempty"`
	LostReason string     `json:"lostReason,omitempty"`
}

// Service is the camera part of the session host.
type Service interface {
	holds.Session
	Snapshot(ctx context.Context, holdID string) (Frame, error)
	// PlayAudio starts an Ogg/Opus clip on the held camera's talkback track
	// and returns its length; the clip plays on after it returns.
	PlayAudio(ctx context.Context, holdID string, ogg []byte) (time.Duration, error)
	// Record copies the held video into a file for the duration.
	Record(ctx context.Context, holdID string, duration time.Duration) (Clip, error)
	Status(holdID string) (Status, bool)
}

type held struct {
	endpointID string
	stream     Stream
	timer      clock.Timer
	status     Status
	closing    bool
}

type service struct {
	opener Opener
	clock  clock.Clock
	lost   LostFunc

	mu    sync.Mutex
	holds map[string]*held
}

// New returns the camera Service. lost may be nil.
func New(opener Opener, c clock.Clock, lost LostFunc) Service {
	if lost == nil {
		lost = func(string, holds.Reason, string) {}
	}
	return &service{opener: opener, clock: c, lost: lost, holds: map[string]*held{}}
}

func (s *service) Open(ctx context.Context, holdID, endpointID string) error {
	stream, err := s.opener.Open(ctx, endpointID, Lifetime)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	h := &held{endpointID: endpointID, stream: stream, status: Status{Live: true, OpenedAt: now, RenewsAt: now.Add(Lifetime - renewBefore)}}
	s.mu.Lock()
	if old := s.holds[holdID]; old != nil {
		s.mu.Unlock()
		_ = stream.Close(ctx)
		return fmt.Errorf("camera: hold %s already has a camera", holdID)
	}
	s.holds[holdID] = h
	h.timer = s.clock.AfterFunc(Lifetime-renewBefore, func() { s.renew(holdID) })
	s.mu.Unlock()
	go s.watch(holdID, stream)
	return nil
}

// watch reports a stream that stopped while it was still the hold's stream.
func (s *service) watch(holdID string, stream Stream) {
	<-stream.Done()
	s.mu.Lock()
	h := s.holds[holdID]
	if h == nil || h.closing || h.stream != stream || !h.status.Live {
		s.mu.Unlock()
		return
	}
	h.status.Live = false
	h.status.LostReason = string(holds.ReasonPreempted)
	h.timer.Stop()
	s.mu.Unlock()
	_ = stream.Close(context.Background())
	s.lost(holdID, holds.ReasonPreempted, "the camera stream was closed by the far side; another viewer may have opened the camera")
}

// renew opens a replacement stream before the current session ends, then
// closes the old one.
func (s *service) renew(holdID string) {
	s.mu.Lock()
	h := s.holds[holdID]
	if h == nil || h.closing || !h.status.Live {
		s.mu.Unlock()
		return
	}
	endpointID := h.endpointID
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), renewTimeout)
	defer cancel()
	stream, err := s.opener.Open(ctx, endpointID, Lifetime)

	s.mu.Lock()
	h = s.holds[holdID]
	if h == nil || h.closing || !h.status.Live {
		s.mu.Unlock()
		if stream != nil {
			_ = stream.Close(ctx)
		}
		return
	}
	if err != nil {
		old := h.stream
		h.status.Live = false
		h.status.LostReason = string(holds.ReasonEndpointError)
		s.mu.Unlock()
		_ = old.Close(ctx)
		s.lost(holdID, holds.ReasonEndpointError, "renew the camera session: "+err.Error())
		return
	}
	old := h.stream
	h.stream = stream
	now := s.clock.Now()
	h.status.Renewals++
	h.status.RenewsAt = now.Add(Lifetime - renewBefore)
	h.timer = s.clock.AfterFunc(Lifetime-renewBefore, func() { s.renew(holdID) })
	s.mu.Unlock()
	go s.watch(holdID, stream)
	_ = old.Close(ctx)
}

func (s *service) Close(ctx context.Context, holdID string, _ holds.Ending) error {
	s.mu.Lock()
	h := s.holds[holdID]
	if h == nil {
		s.mu.Unlock()
		return nil
	}
	h.closing = true
	h.status.Live = false
	if h.timer != nil {
		h.timer.Stop()
	}
	stream := h.stream
	delete(s.holds, holdID)
	s.mu.Unlock()
	return stream.Close(ctx)
}

// liveStream returns the hold's current stream, or camera_unavailable.
func (s *service) liveStream(holdID string) (Stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.holds[holdID]
	if h == nil {
		return nil, unavailable("hold " + holdID + " has no camera")
	}
	if !h.status.Live {
		return nil, unavailable("the held camera stopped (" + h.status.LostReason + "); release the hold and hold the camera again")
	}
	return h.stream, nil
}

func (s *service) PlayAudio(ctx context.Context, holdID string, ogg []byte) (time.Duration, error) {
	stream, err := s.liveStream(holdID)
	if err != nil {
		return 0, err
	}
	length, err := stream.Play(ctx, ogg)
	switch {
	case err == nil:
		return length, nil
	case errors.Is(err, ErrTalkbackBusy):
		return 0, &holds.Error{Code: holds.CodeTalkbackBusy, Message: "a clip is already playing on the held camera; send the next one when it ends"}
	case errors.Is(err, ErrClipInvalid):
		return 0, &holds.Error{Code: holds.CodeInvalidRequest, Message: err.Error()}
	case ctx.Err() != nil:
		return 0, ctx.Err()
	default:
		return 0, unavailable("play on the held camera: " + err.Error())
	}
}

func (s *service) Record(ctx context.Context, holdID string, duration time.Duration) (Clip, error) {
	stream, err := s.liveStream(holdID)
	if err != nil {
		return Clip{}, err
	}
	clip, err := stream.Record(ctx, duration)
	if err != nil {
		if ctx.Err() != nil {
			return Clip{}, ctx.Err()
		}
		return Clip{}, unavailable("record the held camera: " + err.Error())
	}
	return clip, nil
}

func (s *service) Snapshot(ctx context.Context, holdID string) (Frame, error) {
	stream, err := s.liveStream(holdID)
	if err != nil {
		return Frame{}, err
	}
	jpeg, at, err := stream.Latest(ctx, FreshFrame)
	if err != nil {
		if ctx.Err() != nil {
			return Frame{}, ctx.Err()
		}
		return Frame{}, unavailable("read the held camera: " + err.Error())
	}
	s.mu.Lock()
	if h := s.holds[holdID]; h != nil {
		last := at
		h.status.LastFrame = &last
	}
	s.mu.Unlock()
	return Frame{JPEG: jpeg, CapturedAt: at}, nil
}

func (s *service) Status(holdID string) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.holds[holdID]
	if h == nil {
		return Status{}, false
	}
	return h.status, true
}

func unavailable(message string) error {
	return &holds.Error{Code: holds.CodeCameraUnavailable, Message: message}
}

// IsUnavailable reports whether err is the typed camera_unavailable error.
func IsUnavailable(err error) bool {
	var holdErr *holds.Error
	return errors.As(err, &holdErr) && holdErr.Code == holds.CodeCameraUnavailable
}
