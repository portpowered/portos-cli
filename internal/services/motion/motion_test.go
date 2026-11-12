package motion_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/portpowered/portos-cli/internal/services/motion"
)

const (
	rcNamespace   = "port1/systems/zero/capability-interfaces/remote-control"
	vacNamespace  = "port1/systems/zero/capability-interfaces/robotic-vacuum-cleaner"
	endpointID    = "port1/principals/me/endpoints/vac"
	holdID        = "hold-1"
	waitFor       = 5 * time.Second
	pollEvery     = 2 * time.Millisecond
	notActiveCode = "remote_control_not_active"
)

var start = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

type call struct {
	endpoint, namespace, name string
	body                      map[string]any
}

// fakeClient records calls and tracks how many sends overlap.
type fakeClient struct {
	mu          sync.Mutex
	calls       []call
	inflight    int
	maxInflight int
	started     chan call
	// handler decides each reply; n is the zero-based call index. It may block.
	handler func(n int, c call) (*api.MessageRequest, error)
}

func newFakeClient() *fakeClient {
	return &fakeClient{started: make(chan call, 64)}
}

func (f *fakeClient) SendSync(_ context.Context, endpoint, namespace, name string, body map[string]any) (*api.MessageRequest, error) {
	c := call{endpoint: endpoint, namespace: namespace, name: name, body: cloneBody(body)}
	f.mu.Lock()
	n := len(f.calls)
	f.calls = append(f.calls, c)
	f.inflight++
	if f.inflight > f.maxInflight {
		f.maxInflight = f.inflight
	}
	f.mu.Unlock()
	f.started <- c
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()
	if f.handler != nil {
		return f.handler(n, c)
	}
	return moveReply(0, 0), nil
}

func cloneBody(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (f *fakeClient) snapshot() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func (f *fakeClient) names() []string {
	var out []string
	for _, c := range f.snapshot() {
		out = append(out, c.name)
	}
	return out
}

func (f *fakeClient) overlap() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInflight
}

// moveReply is a SUCCEEDED SYNC move reply whose pose is at (x, y).
func moveReply(x, y float32) *api.MessageRequest {
	pose := api.SpatialInterfacePoseAttribute{
		ObservedAt:             start,
		Point:                  api.SpatialPoint{Type: "Point", Coordinates: []float32{x, y}},
		RelativeToGroupRouteId: "map",
	}
	raw, _ := json.Marshal(api.RemoteControlInterfaceMoveResponseMessage{Pose: pose})
	var body map[string]interface{}
	_ = json.Unmarshal(raw, &body)
	return &api.MessageRequest{
		Id:      "req",
		Status:  api.SUCCEEDED,
		Results: []api.MessageRequestResult{{TargetId: endpointID, Message: &api.Message{Body: body}}},
		Errors:  []api.MessageRequestError{},
	}
}

func failedReply(providerCode string) *api.MessageRequest {
	return &api.MessageRequest{
		Id:      "req",
		Status:  api.FAILED,
		Results: []api.MessageRequestResult{},
		Errors: []api.MessageRequestError{{
			Code: api.REJECTED, TargetId: endpointID,
			Error: &api.Error{Message: "no", Details: &api.ErrorDetails{ProviderCode: &providerCode}},
		}},
	}
}

type fatal struct {
	holdID string
	reason holds.Reason
	detail string
}

type rig struct {
	svc    motion.Service
	client *fakeClient
	clock  *clock.Fake
	fatals chan fatal
	state  *fakeState
}

func newRig(mode keepalive.Mode) *rig {
	r := &rig{client: newFakeClient(), clock: clock.NewFake(start), fatals: make(chan fatal, 8)}
	r.state = &fakeState{}
	r.svc = motion.New(r.client, r.state, r.clock, mode, func(id string, reason holds.Reason, detail string) {
		r.fatals <- fatal{id, reason, detail}
	})
	return r
}

// open opens the session and consumes the start-session notification.
func (r *rig) open(t *testing.T) {
	t.Helper()
	require.NoError(t, r.svc.Open(context.Background(), holdID, endpointID))
	<-r.client.started
}

func (r *rig) move(t *testing.T, body map[string]any) *api.MessageRequest {
	t.Helper()
	reply, err := r.svc.Move(context.Background(), holdID, body)
	require.NoError(t, err)
	return reply
}

func (r *rig) waitCalls(t *testing.T, n int) []call {
	t.Helper()
	require.Eventually(t, func() bool { return len(r.client.snapshot()) >= n }, waitFor, pollEvery)
	return r.client.snapshot()
}

func agentMove() map[string]any {
	return map[string]any{"velocity": 0.2, "omega": 0.0, "duration": 1000, "seqNum": 7}
}

// gateSecond makes the second send (index 1) block until the returned func is called.
func (r *rig) gateSecond() (release func()) {
	gate := make(chan struct{})
	r.client.handler = func(n int, _ call) (*api.MessageRequest, error) {
		if n == 1 {
			<-gate
		}
		return moveReply(0, 0), nil
	}
	return func() { close(gate) }
}

func TestOpenSendsStartSession(t *testing.T) {
	r := newRig(keepalive.ModePing)
	require.NoError(t, r.svc.Open(context.Background(), holdID, endpointID))
	calls := r.client.snapshot()
	require.Len(t, calls, 1)
	require.Equal(t, call{endpoint: endpointID, namespace: rcNamespace, name: "start-session", body: map[string]any{}}, calls[0])
	require.Equal(t, 0, r.svc.Status(holdID).SeqNum)
}

func TestOpenFailureIsAnError(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = func(int, call) (*api.MessageRequest, error) { return failedReply("remote_control_busy"), nil }
	require.Error(t, r.svc.Open(context.Background(), holdID, endpointID))
	_, err := r.svc.Move(context.Background(), holdID, agentMove())
	require.Error(t, err, "no session was registered")
	require.Equal(t, []string{"start-session"}, r.client.names(), "a refused start reached no robot, so nothing is sent home")
}

// failStart answers start-session with the given reply and error and every other send with success.
func failStart(reply *api.MessageRequest, err error) func(int, call) (*api.MessageRequest, error) {
	return func(_ int, c call) (*api.MessageRequest, error) {
		if c.name == "start-session" {
			return reply, err
		}
		return moveReply(0, 0), nil
	}
}

func timedOutReply() *api.MessageRequest {
	reply := failedReply("timeout")
	reply.Errors[0].Error.Code = api.ErrorCodePROVIDERTIMEOUT
	return reply
}

// openAdvancing opens the hold while advancing the clock past each start-session retry pause.
func (r *rig) openAdvancing(t *testing.T) error {
	t.Helper()
	opened := make(chan error, 1)
	go func() { opened <- r.svc.Open(context.Background(), holdID, endpointID) }()
	for {
		select {
		case err := <-opened:
			return err
		case <-time.After(pollEvery):
			r.clock.Advance(motion.StartRetryPause)
		}
	}
}

// everyStartFailed is the calls of an open whose start-session failed on every try and was sent home.
func everyStartFailed() []string {
	names := make([]string, 0, motion.StartAttempts+2)
	for range motion.StartAttempts {
		names = append(names, "start-session")
	}
	return append(names, "end-session", "return-to-dock")
}

func TestAnOpenTheProviderTimedOutSendsTheRobotHome(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = failStart(timedOutReply(), nil)
	err := r.openAdvancing(t)
	require.ErrorContains(t, err, "PROVIDER_TIMEOUT")
	require.Equal(t, everyStartFailed(), r.client.names())
	r.client.handler = nil
	require.NoError(t, r.svc.Open(context.Background(), holdID, endpointID), "the failed open left nothing registered")
}

func TestAnOpenStillLeavingTheDockSendsTheRobotHome(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = failStart(failedReply("remote_control_not_started"), nil)
	require.ErrorContains(t, r.openAdvancing(t), "remote_control_not_started")
	require.Equal(t, everyStartFailed(), r.client.names(), "the robot may have left its dock, so it is sent home")
}

func TestAnOpenRidesOutASlowStart(t *testing.T) {
	for name, failure := range map[string]*api.MessageRequest{
		"provider timeout": timedOutReply(),
		"still moving":     failedReply("remote_control_not_started"),
		"request deadline": deadlineReply(),
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(keepalive.ModePing)
			var starts atomic.Int32
			r.client.handler = func(_ int, c call) (*api.MessageRequest, error) {
				if c.name == "start-session" && int(starts.Add(1)) < motion.StartAttempts {
					return failure, nil
				}
				return moveReply(0, 0), nil
			}
			require.NoError(t, r.openAdvancing(t))
			require.Equal(t, motion.StartAttempts, int(starts.Load()))
			_, err := r.svc.Move(context.Background(), holdID, agentMove())
			require.NoError(t, err, "the session is registered")
		})
	}
}

// deadlineReply is a start-session Port OS stopped waiting for at its request
// deadline: the dispatch ended TIMED_OUT and its error body is a generic INTERNAL.
func deadlineReply() *api.MessageRequest {
	reply := failedReply("")
	reply.Errors[0].Code = api.TIMEDOUT
	reply.Errors[0].Error = &api.Error{Code: api.ErrorCodeINTERNAL, Message: "internal error"}
	return reply
}

func TestAnOpenCutOffAtTheRequestDeadlineSendsTheRobotHome(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = failStart(deadlineReply(), nil)
	err := r.openAdvancing(t)
	require.ErrorContains(t, err, "TIMED_OUT")
	require.Equal(t, everyStartFailed(), r.client.names())
}

func TestAnOpenWithNoReplySendsTheRobotHome(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = failStart(nil, context.DeadlineExceeded)
	require.ErrorIs(t, r.openAdvancing(t), context.DeadlineExceeded)
	require.Equal(t, everyStartFailed(), r.client.names())
}

func TestAnOpenRejectedForAuthSendsNothingHome(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.client.handler = failStart(nil, &apiclient.APIError{StatusCode: 401, Err: errors.New("unauthorized")})
	require.Error(t, r.svc.Open(context.Background(), holdID, endpointID))
	require.Equal(t, []string{"start-session"}, r.client.names())
}

func TestMoveRewritesSeqNum(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	in := agentMove()
	for i := 0; i < 3; i++ {
		r.move(t, in)
	}
	calls := r.client.snapshot()
	require.Len(t, calls, 4)
	for i, want := range []int{1, 2, 3} {
		require.Equal(t, "move", calls[i+1].name)
		require.Equal(t, want, calls[i+1].body["seqNum"])
	}
	require.Equal(t, 7, in["seqNum"], "the caller's body is not modified")
	require.Equal(t, 3, r.svc.Status(holdID).SeqNum)
}

func TestMoveReturnsReplyUnchanged(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	want := moveReply(3, 4)
	r.client.handler = func(int, call) (*api.MessageRequest, error) { return want, nil }
	require.Same(t, want, r.move(t, agentMove()))
}

func TestMovesAreSerialized(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	release := r.gateSecond()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := r.svc.Move(context.Background(), holdID, agentMove())
			require.NoError(t, err)
		}()
	}
	<-r.client.started
	require.Eventually(t, func() bool { return r.svc.Status(holdID).Pending == 2 }, waitFor, pollEvery)
	release()
	wg.Wait()
	require.Len(t, r.client.snapshot(), 3)
	require.Equal(t, 1, r.client.overlap())
	require.Equal(t, 0, r.svc.Status(holdID).Pending)
}

func TestPingDueAfter20s(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.clock.Advance(19 * time.Second)
	require.Len(t, r.client.snapshot(), 1, "nothing is due at 19 s")
	r.clock.Advance(time.Second)
	calls := r.waitCalls(t, 2)
	require.Equal(t, "move", calls[1].name)
	require.Equal(t, rcNamespace, calls[1].namespace)
	require.Equal(t, map[string]any{"velocity": 0.0, "omega": 0.0, "duration": 1000, "seqNum": 1}, calls[1].body)
	require.Equal(t, string(keepalive.ModePing), r.svc.Status(holdID).KeepaliveMode)
}

func TestMoveDefersPing(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.clock.Advance(15 * time.Second)
	r.move(t, agentMove())
	r.clock.Advance(15 * time.Second)
	require.Len(t, r.client.snapshot(), 2, "the move restarted the 20 s wait")
	r.clock.Advance(5 * time.Second)
	r.waitCalls(t, 3)
}

func TestFidgetSendsPairAndMirror(t *testing.T) {
	r := newRig(keepalive.ModeFidget)
	r.open(t)
	r.clock.Advance(20 * time.Second)
	calls := r.waitCalls(t, 3)
	require.Equal(t, 0.4, calls[1].body["omega"])
	require.Equal(t, 1, calls[1].body["seqNum"])
	require.Equal(t, -0.4, calls[2].body["omega"])
	require.Equal(t, 2, calls[2].body["seqNum"])
	require.Equal(t, 1, r.client.overlap())
}

func TestAgentMoveDropsPendingMirror(t *testing.T) {
	r := newRig(keepalive.ModeFidget)
	r.open(t)
	release := r.gateSecond()
	r.clock.Advance(20 * time.Second)
	<-r.client.started // the fidget's first half is in flight
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := r.svc.Move(context.Background(), holdID, agentMove())
		require.NoError(t, err)
	}()
	require.Eventually(t, func() bool { return r.svc.Status(holdID).Pending == 1 }, waitFor, pollEvery)
	release()
	<-done
	// Close runs on the same goroutine after any mirror, so it fixes the full order.
	require.NoError(t, r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased}))
	require.Equal(t, []string{"start-session", "move", "move", "end-session", "return-to-dock"}, r.client.names(), "no mirror")
	calls := r.client.snapshot()
	require.Equal(t, 0.4, calls[1].body["omega"])
	require.Equal(t, 0.2, calls[2].body["velocity"])
	require.Equal(t, 1, r.client.overlap())
}

func TestKeepaliveRefreshesPose(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	_, _, ok := r.svc.LastPose(holdID)
	require.False(t, ok)
	r.client.handler = func(int, call) (*api.MessageRequest, error) { return moveReply(5, 6), nil }
	r.clock.Advance(20 * time.Second)
	require.Eventually(t, func() bool { _, _, ok := r.svc.LastPose(holdID); return ok }, waitFor, pollEvery)
	pose, at, _ := r.svc.LastPose(holdID)
	require.Equal(t, []float32{5, 6}, pose.Point.Coordinates)
	require.Equal(t, start.Add(20*time.Second), at)
	status := r.svc.Status(holdID)
	require.NotNil(t, status.LastPose)
	require.Equal(t, start.Add(20*time.Second), status.LastMoveAt)
}

func TestNotActiveEndsHoldWithoutRestart(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	reply := failedReply(notActiveCode)
	r.client.handler = func(int, call) (*api.MessageRequest, error) { return reply, nil }
	got, err := r.svc.Move(context.Background(), holdID, agentMove())
	require.NoError(t, err)
	require.Same(t, reply, got)
	f := <-r.fatals
	require.Equal(t, holdID, f.holdID)
	require.Equal(t, holds.ReasonRemoteControlNotActive, f.reason)
	_, err = r.svc.Move(context.Background(), holdID, agentMove())
	require.Error(t, err, "a dead session takes no more moves")
	r.clock.Advance(time.Minute)
	require.Equal(t, []string{"start-session", "move"}, r.client.names(), "no start-session after the first")
	require.NoError(t, r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonRemoteControlNotActive}))
	require.Equal(t, []string{"start-session", "move"}, r.client.names(), "close sends nothing to a dead session")
}

func TestKeepaliveNotActiveEndsHold(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = func(int, call) (*api.MessageRequest, error) { return failedReply(notActiveCode), nil }
	r.clock.Advance(20 * time.Second)
	require.Equal(t, holds.ReasonRemoteControlNotActive, (<-r.fatals).reason)
}

func TestUnauthorizedEndsHold(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = func(int, call) (*api.MessageRequest, error) {
		return nil, &apiclient.APIError{StatusCode: 401, Err: errors.New("unauthorized")}
	}
	_, err := r.svc.Move(context.Background(), holdID, agentMove())
	require.Error(t, err)
	f := <-r.fatals
	require.Equal(t, holds.ReasonAuthRejected, f.reason)
	require.Equal(t, holdID, f.holdID)
}

func TestCloseOrder(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	require.NoError(t, r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased}))
	calls := r.client.snapshot()
	require.Len(t, calls, 3)
	require.Equal(t, "end-session", calls[1].name)
	require.Equal(t, rcNamespace, calls[1].namespace)
	require.Equal(t, "return-to-dock", calls[2].name)
	require.Equal(t, vacNamespace, calls[2].namespace)
	r.clock.Advance(time.Minute)
	require.Len(t, r.client.snapshot(), 3, "a closed session sends no keepalives")
	require.NoError(t, r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased}), "close is idempotent")
}

func TestCloseDrainsMoveInFlight(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	release := r.gateSecond()
	moved := make(chan error, 1)
	go func() { _, err := r.svc.Move(context.Background(), holdID, agentMove()); moved <- err }()
	<-r.client.started
	closed := make(chan error, 1)
	go func() {
		closed <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	release()
	require.NoError(t, <-moved)
	require.NoError(t, <-closed)
	require.Equal(t, []string{"start-session", "move", "end-session", "return-to-dock"}, r.client.names())
	require.Equal(t, 1, r.client.overlap())
}

func TestCloseFailsQueuedMoves(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	release := r.gateSecond()
	first := make(chan error, 1)
	go func() { _, err := r.svc.Move(context.Background(), holdID, agentMove()); first <- err }()
	<-r.client.started
	second := make(chan error, 1)
	go func() { _, err := r.svc.Move(context.Background(), holdID, agentMove()); second <- err }()
	require.Eventually(t, func() bool { return r.svc.Status(holdID).Pending == 2 }, waitFor, pollEvery)
	closed := make(chan error, 1)
	go func() {
		closed <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	// Close is parked once its request sits in the hold's channel.
	require.Eventually(t, func() bool { return motion.ClosePending(r.svc, holdID) }, waitFor, pollEvery)
	release()
	require.NoError(t, <-first)
	require.ErrorIs(t, <-second, motion.ErrEnded)
	require.NoError(t, <-closed)
	require.Equal(t, []string{"start-session", "move", "end-session", "return-to-dock"}, r.client.names())
	require.Equal(t, 1, r.client.overlap())
}

func TestMoveOnUnknownHoldFails(t *testing.T) {
	r := newRig(keepalive.ModePing)
	_, err := r.svc.Move(context.Background(), "nope", agentMove())
	require.Error(t, err)
}

func TestCloseAfterAuthRejectedStillTriesToDock(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = func(n int, _ call) (*api.MessageRequest, error) {
		return nil, &apiclient.APIError{StatusCode: 401, Err: errors.New("unauthorized")}
	}
	_, _ = r.svc.Move(context.Background(), holdID, agentMove())
	require.Equal(t, holds.ReasonAuthRejected, (<-r.fatals).reason)
	err := r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonAuthRejected})
	require.Error(t, err, "the best-effort attempt fails with the 401")
	require.Equal(t, []string{"start-session", "move", "end-session", "return-to-dock"}, r.client.names())
}

func TestCloseSendsRobotHomeEvenWithCancelledContext(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.svc.Close(ctx, holdID, holds.Ending{Reason: holds.ReasonReleased})
	require.Eventually(t, func() bool { return len(r.client.snapshot()) == 3 }, waitFor, pollEvery)
	require.Equal(t, "return-to-dock", r.client.names()[2])
}

// dockFails answers every return-to-dock before try `succeedOn` with a provider
// timeout (succeedOn 0: every try fails) and every other send with success.
func dockFails(succeedOn int) func(int, call) (*api.MessageRequest, error) {
	var tries atomic.Int32
	return func(_ int, c call) (*api.MessageRequest, error) {
		if c.name != "return-to-dock" {
			return moveReply(0, 0), nil
		}
		if try := int(tries.Add(1)); succeedOn == 0 || try < succeedOn {
			return failedReply("provider_timeout"), nil
		}
		return moveReply(0, 0), nil
	}
}

// closeAdvancing closes the hold while advancing the clock past each dock retry pause.
func (r *rig) closeAdvancing(t *testing.T) error {
	t.Helper()
	closed := make(chan error, 1)
	go func() {
		closed <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	for {
		select {
		case err := <-closed:
			return err
		case <-time.After(pollEvery):
			r.clock.Advance(motion.DockRetryPause)
		}
	}
}

func (r *rig) docks() int {
	n := 0
	for _, name := range r.client.names() {
		if name == "return-to-dock" {
			n++
		}
	}
	return n
}

func TestCloseSendsTheDockAgainAfterAProviderTimeout(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = dockFails(2)
	require.NoError(t, r.closeAdvancing(t))
	require.Equal(t, []string{"start-session", "end-session", "return-to-dock", "return-to-dock"}, r.client.names())
}

func TestCloseGivesUpOnTheDockAfterThreeTries(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = dockFails(0)
	err := r.closeAdvancing(t)
	require.ErrorContains(t, err, "sent 3 times")
	require.Equal(t, motion.DockAttempts, r.docks())
}

func TestCloseStillDocksWhenEndSessionTimesOut(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = func(_ int, c call) (*api.MessageRequest, error) {
		if c.name == "end-session" {
			return nil, context.DeadlineExceeded
		}
		return moveReply(0, 0), nil
	}
	err := r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	require.ErrorIs(t, err, context.DeadlineExceeded, "the end-session failure is reported")
	require.Equal(t, []string{"start-session", "end-session", "return-to-dock"}, r.client.names(), "and the robot is still sent home once")
}

func TestAHoldEndingWaitsLongEnoughForEveryDockTry(t *testing.T) {
	require.LessOrEqual(t, motion.CloseBudget, holds.DeadManCloseTimeout)
}

func TestSecondOpenForSameHoldIsRejected(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	require.Error(t, r.svc.Open(context.Background(), holdID, endpointID))
	require.Len(t, r.client.snapshot(), 1, "the rejected open sent nothing")
}

func TestSecondCloseNeverHangsAfterTheFirstGaveUp(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = r.svc.Close(ctx, holdID, holds.Ending{Reason: holds.ReasonReleased})
	r.waitCalls(t, 3) // the goroutine finished the dock on its own
	second := make(chan error, 1)
	go func() {
		second <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	select {
	case err := <-second:
		require.NoError(t, err)
	case <-time.After(waitFor):
		t.Fatal("the second Close hung")
	}
	require.Len(t, r.client.snapshot(), 3, "the robot was sent home once")
}

func TestCloseThenOpenSameHoldSucceeds(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	require.NoError(t, r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased}))
	require.NoError(t, r.svc.Open(context.Background(), holdID, endpointID), "the closed session was pruned")
	require.Equal(t, []string{"start-session", "end-session", "return-to-dock", "start-session"}, r.client.names())
}

func TestCloseWaitsForOpenInFlight(t *testing.T) {
	r := newRig(keepalive.ModePing)
	gate := make(chan struct{})
	r.client.handler = func(n int, _ call) (*api.MessageRequest, error) {
		if n == 0 {
			<-gate
		}
		return moveReply(0, 0), nil
	}
	opened := make(chan error, 1)
	go func() { opened <- r.svc.Open(context.Background(), holdID, endpointID) }()
	<-r.client.started

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, r.svc.Close(cancelled, holdID, holds.Ending{}), context.Canceled, "waiting for Open is bounded by ctx")

	closed := make(chan error, 1)
	go func() {
		closed <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	close(gate)
	require.NoError(t, <-opened)
	require.NoError(t, <-closed)
	require.Equal(t, []string{"start-session", "end-session", "return-to-dock"}, r.client.names(), "the robot is not left undocked")
}
