// Package motion drives a robot's remote control for a hold. One goroutine per
// hold owns that hold's FIFO queue of moves, sends them one at a time, and sends
// keepalive moves while the agent is idle so the robot keeps remote control.
package motion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/portos"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

// providerNotActive is the provider code of a move sent to a robot that is not
// in remote control (it is back on its dock).
const providerNotActive = "remote_control_not_active"

const seqNumKey = "seqNum"

const (
	// keepaliveTimeout bounds one keepalive send so a hung request cannot block queued moves or the dock.
	keepaliveTimeout = 15 * time.Second
	// closeTimeout bounds the end-session and return-to-dock pair, which runs even if the caller gave up.
	closeTimeout = 30 * time.Second
)

// Errors a Move can return besides transport errors.
var (
	errUnknownHold = errors.New("motion: no motion session for that hold")
	errEnded       = errors.New("motion: the motion session has ended")
)

// Status is a snapshot of one hold's motion session.
type Status struct {
	SeqNum        int
	LastMoveAt    time.Time
	LastPose      *api.SpatialInterfacePoseAttribute
	KeepaliveMode string
	// Pending counts agent moves queued or in flight.
	Pending int
}

// Service is the motion session of the session host.
type Service interface {
	holds.Session

	// Move queues an agent move for the hold, rewrites its seqNum, waits for it
	// to run and returns Port OS's reply unchanged. A reply that reports a failed
	// move is a reply, not an error.
	Move(ctx context.Context, holdID string, body map[string]any) (*api.MessageRequest, error)
	// LastPose is the pose from the last SUCCEEDED move or keepalive, and when
	// it was cached.
	LastPose(holdID string) (api.SpatialInterfacePoseAttribute, time.Time, bool)
	// Status reports the hold's motion state. An unknown hold reports the zero Status.
	Status(holdID string) Status
}

type service struct {
	client  portos.Client
	clock   clock.Clock
	mode    keepalive.Mode
	onFatal func(holdID string, reason holds.Reason, detail string)

	mu       sync.Mutex
	sessions map[string]*session
	opening  map[string]chan struct{} // closed when that hold's Open finishes
}

var _ Service = (*service)(nil)

// New returns a motion Service. onFatal is called, on its own goroutine, when a
// hold's session cannot continue (the robot left remote control, or Port OS
// rejected the credentials); it may be nil. An unknown mode becomes the default.
func New(client portos.Client, c clock.Clock, mode keepalive.Mode, onFatal func(holdID string, reason holds.Reason, detail string)) Service {
	if !mode.Valid() {
		mode = keepalive.Default
	}
	return &service{client: client, clock: c, mode: mode, onFatal: onFatal, sessions: map[string]*session{}, opening: map[string]chan struct{}{}}
}

type result struct {
	reply *api.MessageRequest
	err   error
}

type job struct {
	ctx      context.Context
	body     map[string]any
	done     chan result
	canceled atomic.Bool
}

type closeRequest struct {
	ctx  context.Context
	done chan error
}

type session struct {
	svc      *service
	id       string
	endpoint string

	wake     chan struct{}
	kick     chan struct{}
	closeReq chan closeRequest
	exited   chan struct{}
	closeErr error // set before exited is closed

	// Loop-owned: only the run goroutine touches these after Open returns.
	timer         clock.Timer
	pendingMirror bool

	mu         sync.Mutex
	queue      []*job
	running    bool
	seq        int
	lastMoveAt time.Time
	pose       *api.SpatialInterfacePoseAttribute
	poseAt     time.Time
	dead       bool
	closed     bool
	deadReason holds.Reason
}

func (m *service) find(holdID string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[holdID]
	if !ok {
		return nil, errUnknownHold
	}
	return s, nil
}

// replyError turns a reply that is not SUCCEEDED, or a transport error, into an error.
func replyError(name string, reply *api.MessageRequest, err error) error {
	if err != nil {
		return err
	}
	if reply == nil {
		return fmt.Errorf("%s: no reply", name)
	}
	if reply.Status != api.SUCCEEDED {
		return fmt.Errorf("%s: request %s ended %s %s", name, reply.Id, reply.Status, strings.Join(portos.ProviderCodes(reply), " "))
	}
	return nil
}

func (m *service) Open(ctx context.Context, holdID, endpointID string) error {
	m.mu.Lock()
	_, live := m.sessions[holdID]
	_, busy := m.opening[holdID]
	if live || busy {
		m.mu.Unlock()
		return fmt.Errorf("motion: hold %s already has a motion session", holdID)
	}
	opened := make(chan struct{})
	m.opening[holdID] = opened
	m.mu.Unlock()
	reply, err := m.client.SendSync(ctx, endpointID, api.INTERFACE_PORTOS_REMOTE_CONTROL, api.INTERFACE_PORTOS_REMOTE_CONTROL_START_SESSION, map[string]any{})
	if err := replyError(api.INTERFACE_PORTOS_REMOTE_CONTROL_START_SESSION, reply, err); err != nil {
		m.mu.Lock()
		delete(m.opening, holdID)
		m.mu.Unlock()
		close(opened)
		return err
	}
	s := &session{
		svc: m, id: holdID, endpoint: endpointID,
		wake: make(chan struct{}, 1), kick: make(chan struct{}, 1),
		closeReq: make(chan closeRequest, 1), exited: make(chan struct{}),
		lastMoveAt: m.clock.Now(),
	}
	s.arm()
	m.mu.Lock()
	delete(m.opening, holdID)
	m.sessions[holdID] = s
	m.mu.Unlock()
	close(opened)
	go s.run()
	return nil
}

func (m *service) Close(ctx context.Context, holdID string, _ holds.Ending) error {
	s, err := m.awaitSession(ctx, holdID)
	if s == nil {
		return err // nil when the hold was never opened or is already closed and pruned
	}
	defer m.prune(holdID, s)
	select {
	case <-s.exited:
		return s.closeErr
	default:
	}
	req := closeRequest{ctx: ctx, done: make(chan error, 1)}
	// Hand the request over first, even when ctx is already done: the robot is
	// still sent home, on a context detached from the caller's.
	select {
	case s.closeReq <- req:
		return s.waitClosed(ctx, req)
	default:
	}
	select {
	case s.closeReq <- req:
		return s.waitClosed(ctx, req)
	case <-s.exited:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// awaitSession returns the hold's session, first waiting for an Open still in
// flight to finish so Close cannot miss the session it is about to register.
func (m *service) awaitSession(ctx context.Context, holdID string) (*session, error) {
	for {
		m.mu.Lock()
		s, ok := m.sessions[holdID]
		opened, busy := m.opening[holdID]
		m.mu.Unlock()
		if ok {
			return s, nil
		}
		if !busy {
			return nil, nil
		}
		select {
		case <-opened:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// waitClosed waits for the hold's goroutine to answer. If the goroutine exited
// without reading the request, the earlier ending's result stands.
func (s *session) waitClosed(ctx context.Context, req closeRequest) error {
	select {
	case err := <-req.done:
		return err
	case <-s.exited:
		return s.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// prune forgets a session once its goroutine has exited.
func (m *service) prune(holdID string, s *session) {
	select {
	case <-s.exited:
	default:
		return
	}
	m.mu.Lock()
	if m.sessions[holdID] == s {
		delete(m.sessions, holdID)
	}
	m.mu.Unlock()
}

func (m *service) Move(ctx context.Context, holdID string, body map[string]any) (*api.MessageRequest, error) {
	s, err := m.find(holdID)
	if err != nil {
		return nil, err
	}
	j := &job{ctx: ctx, body: body, done: make(chan result, 1)}
	s.mu.Lock()
	if s.closed || s.dead {
		s.mu.Unlock()
		return nil, errEnded
	}
	s.queue = append(s.queue, j)
	s.mu.Unlock()
	signal(s.wake)
	select {
	case r := <-j.done:
		return r.reply, r.err
	case <-ctx.Done():
		j.canceled.Store(true)
		return nil, ctx.Err()
	}
}

func (m *service) LastPose(holdID string) (api.SpatialInterfacePoseAttribute, time.Time, bool) {
	s, err := m.find(holdID)
	if err != nil {
		return api.SpatialInterfacePoseAttribute{}, time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pose == nil {
		return api.SpatialInterfacePoseAttribute{}, time.Time{}, false
	}
	return *s.pose, s.poseAt, true
}

func (m *service) Status(holdID string) Status {
	s, err := m.find(holdID)
	if err != nil {
		return Status{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{SeqNum: s.seq, LastMoveAt: s.lastMoveAt, KeepaliveMode: string(m.mode), Pending: len(s.queue)}
	if s.running {
		st.Pending++
	}
	if s.pose != nil {
		pose := *s.pose
		st.LastPose = &pose
	}
	return st
}

func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// run is the hold's goroutine: it owns the queue, the keepalive timer and every send.
func (s *session) run() {
	for {
		select {
		case req := <-s.closeReq:
			s.closeErr = s.shutdown(req.ctx)
			close(s.exited) // before answering, so the closer's prune sees it
			req.done <- s.closeErr
			return
		default:
		}
		if j := s.pop(); j != nil {
			s.runAgent(j)
			continue
		}
		if s.pendingMirror {
			s.keepalive(true)
			continue
		}
		select {
		case <-s.wake:
		case <-s.kick:
			s.keepalive(false)
		case req := <-s.closeReq:
			s.closeErr = s.shutdown(req.ctx)
			close(s.exited) // before answering, so the closer's prune sees it
			req.done <- s.closeErr
			return
		}
	}
}

func (s *session) pop() *job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		return nil
	}
	j := s.queue[0]
	s.queue = s.queue[1:]
	s.running = true
	return j
}

func (s *session) runAgent(j *job) {
	s.pendingMirror = false // an agent move supersedes a fidget's mirror
	var r result
	switch {
	case j.canceled.Load() || j.ctx.Err() != nil:
		r.err = context.Cause(j.ctx)
		if r.err == nil {
			r.err = context.Canceled
		}
	case s.isDead():
		r.err = errEnded
	default:
		r.reply, r.err = s.send(j.ctx, j.body)
	}
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	s.arm()
	j.done <- r
}

func (s *session) isDead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dead
}

// keepalive sends a keepalive move if one is due, then re-arms the timer.
func (s *session) keepalive(mirror bool) {
	s.mu.Lock()
	last, dead := s.lastMoveAt, s.dead
	s.mu.Unlock()
	if dead {
		s.pendingMirror = false
		return
	}
	body, due := keepalive.Next(s.svc.mode, last, s.svc.clock.Now(), mirror)
	if !due {
		s.arm()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), keepaliveTimeout)
	defer cancel()
	_, err := s.send(ctx, body)
	s.pendingMirror = s.svc.mode == keepalive.ModeFidget && !mirror && err == nil && !s.isDead()
	s.arm()
}

// arm schedules the next keepalive check for 20 s after the last move started.
func (s *session) arm() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Lock()
	last, dead := s.lastMoveAt, s.dead
	s.mu.Unlock()
	if dead {
		return
	}
	d := keepalive.Interval - s.svc.clock.Now().Sub(last)
	if d <= 0 {
		signal(s.kick)
		return
	}
	s.timer = s.svc.clock.AfterFunc(d, func() { signal(s.kick) })
}

// send assigns the next seqNum to a copy of body, sends the move and reacts to the outcome.
func (s *session) send(ctx context.Context, body map[string]any) (*api.MessageRequest, error) {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	s.mu.Lock()
	s.seq++
	out[seqNumKey] = s.seq
	s.lastMoveAt = s.svc.clock.Now()
	s.mu.Unlock()

	reply, err := s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_REMOTE_CONTROL, api.INTERFACE_PORTOS_REMOTE_CONTROL_MOVE, out)
	switch {
	case err != nil:
		if portos.IsUnauthorized(err) {
			s.fatal(holds.ReasonAuthRejected, err.Error())
		}
	case portos.HasCode(reply, providerNotActive):
		s.fatal(holds.ReasonRemoteControlNotActive, "the robot is not in remote control; it is probably on its dock")
	case reply != nil && reply.Status == api.SUCCEEDED:
		s.cachePose(reply)
	}
	return reply, err
}

func (s *session) cachePose(reply *api.MessageRequest) {
	if len(reply.Results) == 0 || reply.Results[0].Message == nil {
		return
	}
	raw, err := json.Marshal(reply.Results[0].Message.Body)
	if err != nil {
		return
	}
	var body api.RemoteControlInterfaceMoveResponseMessage
	if json.Unmarshal(raw, &body) != nil || len(body.Pose.Point.Coordinates) == 0 {
		return
	}
	s.mu.Lock()
	s.pose = &body.Pose
	s.poseAt = s.svc.clock.Now()
	s.mu.Unlock()
}

// fatal marks the session dead, refuses its queued moves and tells the owner of
// the hold, once. The owner usually releases the hold, which closes this
// session, so it is told from another goroutine.
func (s *session) fatal(reason holds.Reason, detail string) {
	s.mu.Lock()
	if s.dead {
		s.mu.Unlock()
		return
	}
	s.dead = true
	s.deadReason = reason
	queued := s.queue
	s.queue = nil
	s.mu.Unlock()
	s.pendingMirror = false
	for _, j := range queued {
		j.done <- result{err: errEnded}
	}
	if s.svc.onFatal != nil {
		go s.svc.onFatal(s.id, reason, detail)
	}
}

// shutdown runs on the hold's goroutine after any move in flight has finished.
func (s *session) shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	queued := s.queue
	s.queue = nil
	notActive := s.dead && s.deadReason == holds.ReasonRemoteControlNotActive
	s.mu.Unlock()
	for _, j := range queued {
		j.done <- result{err: errEnded}
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if notActive {
		return nil // the robot is already docked: nothing to send
	}
	// Send the robot home even if the caller gave up; other fatal reasons get one best-effort try.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeout)
	defer cancel()
	var errs []error
	end := api.INTERFACE_PORTOS_REMOTE_CONTROL_END_SESSION
	reply, err := s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_REMOTE_CONTROL, end, map[string]any{})
	if err := replyError(end, reply, err); err != nil {
		errs = append(errs, err)
	}
	dock := api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_RETURN_TO_DOCK
	reply, err = s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, dock, map[string]any{})
	if err := replyError(dock, reply, err); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
