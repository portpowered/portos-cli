// Package camera keeps a held endpoint's camera session open for the session
// host: it opens the stream, renews it before Port OS ends it, serves the
// newest frame, and reports a stream another viewer took away.
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

// Stream is one open camera session.
type Stream interface {
	// Latest returns the newest frame as JPEG and when it arrived.
	Latest(ctx context.Context, maxAge time.Duration) ([]byte, time.Time, error)
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

func (s *service) Snapshot(ctx context.Context, holdID string) (Frame, error) {
	s.mu.Lock()
	h := s.holds[holdID]
	if h == nil {
		s.mu.Unlock()
		return Frame{}, unavailable("hold " + holdID + " has no camera")
	}
	if !h.status.Live {
		reason := h.status.LostReason
		s.mu.Unlock()
		return Frame{}, unavailable("the held camera stopped (" + reason + "); release the hold and hold the camera again")
	}
	stream := h.stream
	s.mu.Unlock()
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
