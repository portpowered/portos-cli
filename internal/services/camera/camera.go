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

	"github.com/portpowered/portos-cli/internal/api"
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
	// cappedSlack is how far short of a full Lifetime a shared session's expiry may fall
	// (the time the first frame took) and still count as a fresh Lifetime. Further short
	// means the session reached the shared upstream's own end.
	cappedSlack = 30 * time.Second
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
	// PTZ sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) for the stream's own
	// session and returns the reply from Port OS. Any sessionId in body is replaced.
	PTZ(ctx context.Context, name string, body map[string]any) (*api.MessageRequest, error)
	// ExpiresAt is when a shared stream's session ends; zero for a single-peer stream.
	ExpiresAt() time.Time
	// EndReason says why a shared stream's session hold ended (DETACHED, SESSION_CLOSED,
	// EXPIRED, or a local reason); empty while it runs and for a single-peer stream.
	EndReason() string
	// Done is closed when the stream stops for any reason.
	Done() <-chan struct{}
	Close(ctx context.Context) error
}

// Opener opens a camera session on an endpoint for the given lifetime.
type Opener interface {
	Open(ctx context.Context, endpointID string, lifetime time.Duration) (Stream, error)
	// OpenShared attaches to the endpoint's shared session as a receive-only subscriber
	// with no talkback track, for at most the given lifetime.
	OpenShared(ctx context.Context, endpointID string, lifetime time.Duration) (Stream, error)
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
	// Shared marks a stream attached to a shared session. ExpiresAt is when that session
	// ends; the hold ends with session_expired then.
	Shared    bool       `json:"shared,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
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
	// PTZ sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) with the hold's own
	// session and returns the reply from Port OS.
	PTZ(ctx context.Context, holdID, name string, body map[string]any) (*api.MessageRequest, error)
	Status(holdID string) (Status, bool)
}

type held struct {
	endpointID string
	stream     Stream
	timer      clock.Timer
	status     Status
	closing    bool
	shared     bool
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
	shared := holds.IsShared(ctx)
	stream, err := s.openStream(ctx, endpointID, shared)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	h := &held{endpointID: endpointID, stream: stream, shared: shared, status: Status{Live: true, OpenedAt: now, Shared: shared}}
	s.mu.Lock()
	if old := s.holds[holdID]; old != nil {
		s.mu.Unlock()
		_ = stream.Close(ctx)
		return fmt.Errorf("camera: hold %s already has a camera", holdID)
	}
	s.holds[holdID] = h
	s.armLocked(holdID, h, now)
	s.mu.Unlock()
	go s.watch(holdID, stream)
	return nil
}

func (s *service) openStream(ctx context.Context, endpointID string, shared bool) (Stream, error) {
	if shared {
		return s.opener.OpenShared(ctx, endpointID, Lifetime)
	}
	return s.opener.Open(ctx, endpointID, Lifetime)
}

// armLocked schedules the hold's next camera event for the stream it holds now. A single-peer
// stream is renewed renewBefore its Lifetime runs out. A shared stream is renewed the same way
// while its session outlasts the renewal; once its expiry falls short of a full Lifetime the
// session is at the shared upstream's end, so nothing can replace it and the hold ends with
// session_expired at that time.
func (s *service) armLocked(holdID string, h *held, now time.Time) {
	renewIn := Lifetime - renewBefore
	h.status.RenewsAt = now.Add(renewIn)
	if h.shared {
		expires := h.stream.ExpiresAt()
		h.status.ExpiresAt = &expires
		if expires.Before(now.Add(Lifetime - cappedSlack)) {
			h.status.RenewsAt = time.Time{}
			h.timer = s.clock.AfterFunc(max(expires.Sub(now), 0), func() { s.expire(holdID) })
			return
		}
	}
	h.timer = s.clock.AfterFunc(renewIn, func() { s.renew(holdID) })
}

// expire ends a shared camera at its session's expiry.
func (s *service) expire(holdID string) {
	s.mu.Lock()
	h := s.holds[holdID]
	if h == nil || h.closing || !h.status.Live {
		s.mu.Unlock()
		return
	}
	h.status.Live = false
	h.status.LostReason = string(holds.ReasonSessionExpired)
	stream := h.stream
	s.mu.Unlock()
	_ = stream.Close(context.Background())
	s.lost(holdID, holds.ReasonSessionExpired, "the shared camera session reached its expiry and is not renewed")
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
	reason, detail := endOf(stream)
	h.status.Live = false
	h.status.LostReason = string(reason)
	h.timer.Stop()
	s.mu.Unlock()
	_ = stream.Close(context.Background())
	s.lost(holdID, reason, detail)
}

// endOf says why a stream that stopped on its own did. A shared stream knows the reason its
// session hold ended; an EXPIRED one is the session's expiry and a SESSION_CLOSED one closed for
// every viewer before it.
func endOf(stream Stream) (holds.Reason, string) {
	switch ended := stream.EndReason(); ended {
	case "":
		return holds.ReasonPreempted, "the camera stream was closed by the far side; another viewer may have opened the camera"
	case string(api.EXPIRED):
		return holds.ReasonSessionExpired, "the shared camera session reached its expiry"
	case string(api.SESSIONCLOSED):
		return holds.ReasonSessionClosed, "the shared camera session closed for every viewer, for example because the camera ended its live view (Ring does after about 12 minutes); hold the camera again to keep looking"
	default:
		return holds.ReasonPreempted, "the shared camera session ended (" + ended + ")"
	}
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
	endpointID, shared := h.endpointID, h.shared
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), renewTimeout)
	defer cancel()
	stream, err := s.openStream(ctx, endpointID, shared)

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
	s.armLocked(holdID, h, now)
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
	if s.isShared(holdID) {
		return 0, &holds.Error{Code: holds.CodeSharedHoldNoTalkback, Message: "a shared camera hold has no talkback track because a call owns the speaker; hold the camera without --shared to play audio"}
	}
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

func (s *service) isShared(holdID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.holds[holdID]
	return h != nil && h.shared
}

func (s *service) PTZ(ctx context.Context, holdID, name string, body map[string]any) (*api.MessageRequest, error) {
	stream, err := s.liveStream(holdID)
	if err != nil {
		return nil, err
	}
	reply, err := stream.PTZ(ctx, name, body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, unavailable("turn the held camera: " + err.Error())
	}
	return reply, nil
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
