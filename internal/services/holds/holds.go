// Package holds owns the session host's holds: one hold per endpoint, a lease
// that proves a caller owns it, a dead-man timer that ends an abandoned hold,
// and the recorded ending. Holds live in memory only; nothing is written to disk.
package holds

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/endpointid"
)

// Kind is a capability a hold covers.
type Kind string

// Kinds a hold can cover.
const (
	KindMotion Kind = "motion"
	KindCamera Kind = "camera"
)

// Reason is why a hold ended.
type Reason string

// Ending reasons.
const (
	ReasonReleased               Reason = "released"
	ReasonDeadMan                Reason = "dead-man"
	ReasonHostPaused             Reason = "host-paused"
	ReasonHostShutdown           Reason = "host-shutdown"
	ReasonRobotDocked            Reason = "robot-docked"
	ReasonBatteryLow             Reason = "battery-low"
	ReasonAuthRejected           Reason = "auth-rejected"
	ReasonEndpointError          Reason = "endpoint-error"
	ReasonRemoteControlNotActive Reason = "remote-control-not-active"
	// ReasonPreempted ends a camera-only hold whose stream another viewer took.
	ReasonPreempted Reason = "preempted"
)

// State is where a hold is in its life.
type State string

// Hold states.
const (
	StateOpening State = "opening"
	StateLive    State = "live"
	StateEnding  State = "ending"
	StateEnded   State = "ended"
)

// Error codes.
const (
	CodeHoldLeaseRequired            = "hold_lease_required"
	CodeHoldOwnedByOther             = "hold_owned_by_other"
	CodeHoldOwnsSession              = "hold_owns_session"
	CodeNotServedByHold              = "not_served_by_hold"
	CodeHoldRequiresRefreshableLogin = "hold_requires_refreshable_login"
	CodeHoldEnded                    = "hold_ended"
	CodeInvalidRequest               = "invalid_request"
	// CodeCameraUnavailable is a snapshot the held camera cannot serve: another
	// viewer took the stream, renewal failed, or no frame could be decoded.
	CodeCameraUnavailable = "camera_unavailable"
)

// DeadManCloseTimeout bounds how long a dead-man ending waits for the sessions
// to close, so a hung endpoint cannot leave a hold ending forever.
const DeadManCloseTimeout = 60 * time.Second

// DefaultDeadMan is the dead-man period used when a hold does not name one.
const DefaultDeadMan = 10 * time.Minute

// Error is a typed hold failure carrying one of the Code constants.
type Error struct {
	Code    string
	Message string
}

// Error implements error.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Ending records why and when a hold ended.
type Ending struct {
	Reason Reason
	At     time.Time
	Detail string
}

// Hold is a snapshot of one hold. Lease is set only in the Open result.
type Hold struct {
	ID, EndpointID, RouteID string
	Kinds                   []Kind
	Lease                   string
	State                   State
	OpenedAt, LastCommandAt time.Time
	DeadMan                 time.Duration
	Ending                  *Ending
}

// Session is one capability of a hold, implemented by motion and camera.
type Session interface {
	Open(ctx context.Context, holdID, endpointID string) error
	Close(ctx context.Context, holdID string, ending Ending) error
}

// LoginCheck returns the hold_requires_refreshable_login error when the stored
// credentials have no refresh token.
type LoginCheck func(ctx context.Context) error

// Service manages holds.
type Service interface {
	Open(ctx context.Context, endpointID string, kinds []Kind, deadMan time.Duration) (Hold, error)
	Authorize(lease, endpointID string, kind Kind) (Hold, error)
	Get(id string) (Hold, error)
	List() []Hold
	Release(ctx context.Context, id string, reason Reason, detail string) error
	EndAll(ctx context.Context, reason Reason)
}

type entry struct {
	hold   Hold
	lease  string
	timer  clock.Timer
	gen    int
	opened []Kind        // sessions that opened and are not yet closed
	done   chan struct{} // closed when the hold reaches ended
}

type service struct {
	clock    clock.Clock
	sessions map[Kind]Session
	login    LoginCheck

	mu      sync.Mutex
	holds   map[string]*entry
	order   []string
	counter int
}

// New returns a holds Service. login may be nil.
func New(c clock.Clock, sessions map[Kind]Session, login LoginCheck) Service {
	return &service{clock: c, sessions: sessions, login: login, holds: map[string]*entry{}}
}

// closeOrder is the order sessions are closed in: motion before camera.
var closeOrder = []Kind{KindMotion, KindCamera}

func snapshot(e *entry) Hold {
	h := e.hold
	h.Lease = ""
	h.Kinds = append([]Kind(nil), h.Kinds...)
	if h.Ending != nil {
		end := *h.Ending
		h.Ending = &end
	}
	return h
}

func (e *entry) covers(k Kind) bool {
	for _, have := range e.hold.Kinds {
		if have == k {
			return true
		}
	}
	return false
}

func newSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// orderedKinds returns kinds de-duplicated in closeOrder.
func orderedKinds(kinds []Kind) []Kind {
	var out []Kind
	for _, k := range closeOrder {
		for _, have := range kinds {
			if have == k {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

// validateEndpointID refuses an id with no canonical key: a hold is matched to
// messages by that key, so such a hold could never be matched.
func validateEndpointID(endpointID string) error {
	if _, ok := endpointid.Key(endpointID); !ok {
		return &Error{Code: CodeInvalidRequest, Message: "use the endpoint's full id or its ep/... form"}
	}
	return nil
}

// checkKinds returns kinds in close order, refusing an empty or repeated set
// and kinds with no session.
func (s *service) checkKinds(kinds []Kind) ([]Kind, error) {
	ordered := orderedKinds(kinds)
	if len(ordered) == 0 || len(ordered) != len(dedupe(kinds)) {
		return nil, fmt.Errorf("holds: kinds must be a non-empty set of %q or %q", KindMotion, KindCamera)
	}
	for _, k := range ordered {
		if s.sessions[k] == nil {
			return nil, fmt.Errorf("holds: no %s session is available", k)
		}
	}
	return ordered, nil
}

func (s *service) Open(ctx context.Context, endpointID string, kinds []Kind, deadMan time.Duration) (Hold, error) {
	if err := validateEndpointID(endpointID); err != nil {
		return Hold{}, err
	}
	if s.login != nil {
		if err := s.login(ctx); err != nil {
			return Hold{}, err
		}
	}
	ordered, err := s.checkKinds(kinds)
	if err != nil {
		return Hold{}, err
	}
	if deadMan <= 0 {
		deadMan = DefaultDeadMan
	}
	lease, err := newSecret(32)
	if err != nil {
		return Hold{}, fmt.Errorf("holds: generate lease: %w", err)
	}

	s.mu.Lock()
	for _, e := range s.holds {
		if endpointid.Same(e.hold.EndpointID, endpointID) && e.hold.State != StateEnded {
			s.mu.Unlock()
			return Hold{}, &Error{Code: CodeHoldOwnedByOther, Message: "endpoint " + endpointID + " is already held"}
		}
	}
	s.counter++
	id := fmt.Sprintf("hold-%d", s.counter)
	now := s.clock.Now()
	e := &entry{lease: lease, done: make(chan struct{}), hold: Hold{
		ID: id, EndpointID: endpointID, Kinds: ordered, State: StateOpening,
		OpenedAt: now, LastCommandAt: now, DeadMan: deadMan,
	}}
	s.holds[id] = e
	s.order = append(s.order, id)
	s.mu.Unlock()

	for _, k := range ordered {
		s.mu.Lock()
		if e.hold.State != StateOpening {
			s.mu.Unlock()
			return Hold{}, &Error{Code: CodeHoldEnded, Message: "hold " + id + " ended while opening"}
		}
		s.mu.Unlock()
		if err := s.sessions[k].Open(ctx, id, endpointID); err != nil {
			detail := fmt.Sprintf("%s session: %v", k, err)
			// Close the sessions that did open, then end the hold.
			_ = s.end(ctx, id, ReasonEndpointError, detail, 0)
			return Hold{}, fmt.Errorf("holds: open %s session: %w", k, err)
		}
		s.mu.Lock()
		if e.hold.State != StateOpening {
			// The hold ended while this session was opening: nobody else will close it.
			ending := Ending{Reason: ReasonReleased}
			if e.hold.Ending != nil {
				ending = *e.hold.Ending
			}
			s.mu.Unlock()
			_ = s.sessions[k].Close(ctx, id, ending)
			return Hold{}, &Error{Code: CodeHoldEnded, Message: "hold " + id + " ended while opening"}
		}
		e.opened = append(e.opened, k)
		s.mu.Unlock()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if e.hold.State != StateOpening {
		return Hold{}, &Error{Code: CodeHoldEnded, Message: "hold " + id + " ended while opening"}
	}
	e.hold.State = StateLive
	s.armLocked(e)
	h := snapshot(e)
	h.Lease = lease
	return h, nil
}

func dedupe(kinds []Kind) []Kind {
	seen := map[Kind]bool{}
	var out []Kind
	for _, k := range kinds {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// armLocked (re)starts the dead-man timer. Callers hold s.mu.
func (s *service) armLocked(e *entry) {
	if e.timer != nil {
		e.timer.Stop()
	}
	e.gen++
	gen, id := e.gen, e.hold.ID
	e.timer = s.clock.AfterFunc(e.hold.DeadMan, func() {
		ctx, cancel := context.WithTimeout(context.Background(), DeadManCloseTimeout)
		defer cancel()
		// end compares gen under the same lock that moves the hold to ending, so an
		// Authorize that re-armed the timer in the meantime wins.
		_ = s.end(ctx, id, ReasonDeadMan, "", gen)
	})
}

func (s *service) Authorize(lease, endpointID string, kind Kind) (Hold, error) {
	if lease == "" {
		return Hold{}, &Error{Code: CodeHoldLeaseRequired, Message: "this endpoint is held; pass the hold's lease"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.order {
		e := s.holds[id]
		if !endpointid.Same(e.hold.EndpointID, endpointID) || subtle.ConstantTimeCompare([]byte(lease), []byte(e.lease)) != 1 {
			continue
		}
		if e.hold.State != StateLive {
			return Hold{}, &Error{Code: CodeHoldEnded, Message: "hold " + e.hold.ID + " has ended"}
		}
		if !e.covers(kind) {
			return Hold{}, &Error{Code: CodeNotServedByHold, Message: "hold " + e.hold.ID + " does not cover " + string(kind)}
		}
		e.hold.LastCommandAt = s.clock.Now()
		s.armLocked(e)
		return snapshot(e), nil
	}
	return Hold{}, &Error{Code: CodeHoldOwnedByOther, Message: "the lease does not belong to a hold on " + endpointID}
}

func (s *service) Get(id string) (Hold, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.holds[id]
	if !ok {
		return Hold{}, fmt.Errorf("holds: no hold %q", id)
	}
	return snapshot(e), nil
}

func (s *service) List() []Hold {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hold, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, snapshot(s.holds[id]))
	}
	return out
}

func (s *service) Release(ctx context.Context, id string, reason Reason, detail string) error {
	s.mu.Lock()
	_, ok := s.holds[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("holds: no hold %q", id)
	}
	return s.end(ctx, id, reason, detail, 0)
}

// end moves a hold to ending, closes its opened sessions (motion first) outside
// the lock, then marks it ended. When expectGen is non-zero and the hold's timer
// generation differs, nothing happens. A hold that is already ending or ended is
// not ended again; the caller waits for it to finish, or for ctx.
func (s *service) end(ctx context.Context, id string, reason Reason, detail string, expectGen int) error {
	s.mu.Lock()
	e := s.holds[id]
	if expectGen != 0 && e.gen != expectGen {
		s.mu.Unlock()
		return nil
	}
	if e.hold.State == StateEnding || e.hold.State == StateEnded {
		done := e.done
		s.mu.Unlock()
		select {
		case <-done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.gen++
	e.hold.State = StateEnding
	ending := Ending{Reason: reason, At: s.clock.Now(), Detail: detail}
	e.hold.Ending = &ending
	kinds := orderedKinds(e.opened)
	s.mu.Unlock()

	var errs []error
	for _, k := range kinds {
		if err := s.sessions[k].Close(ctx, id, ending); err != nil {
			errs = append(errs, fmt.Errorf("close %s session: %w", k, err))
		}
	}
	closeErr := errors.Join(errs...)

	s.mu.Lock()
	defer s.mu.Unlock()
	if closeErr != nil {
		msgs := make([]string, 0, len(errs))
		for _, err := range errs {
			msgs = append(msgs, err.Error())
		}
		joined := strings.Join(msgs, "; ")
		if e.hold.Ending.Detail == "" {
			e.hold.Ending.Detail = joined
		} else {
			e.hold.Ending.Detail += "; " + joined
		}
	}
	e.hold.State = StateEnded
	close(e.done)
	return closeErr
}

func (s *service) EndAll(ctx context.Context, reason Reason) {
	s.mu.Lock()
	var ids []string
	for _, id := range s.order {
		if s.holds[id].hold.State != StateEnded {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		_ = s.Release(ctx, id, reason, "")
	}
}
