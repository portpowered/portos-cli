package motion_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/portos"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/portpowered/portos-cli/internal/services/motion"
)

// fakeState replays a script of state reads; the last entry repeats.
type fakeState struct {
	mu     sync.Mutex
	script []stateStep
	reads  int
}

type stateStep struct {
	state portos.State
	err   error
}

func (f *fakeState) set(steps ...stateStep) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.script = steps
	f.reads = 0
}

func (f *fakeState) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

func (f *fakeState) ReadState(context.Context, string) (portos.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.script) == 0 {
		return portos.State{}, errors.New("no state scripted")
	}
	i := f.reads
	if i >= len(f.script) {
		i = len(f.script) - 1
	}
	f.reads++
	return f.script[i].state, f.script[i].err
}

const floor = "sim-1/floor"

func travelling() stateStep {
	return stateStep{state: portos.State{Operational: api.OperationalStateGoingToPoint}}
}

func arrivedAt(x, y float32) stateStep {
	pose := api.SpatialInterfacePoseAttribute{
		ObservedAt:             start,
		Point:                  api.SpatialPoint{Type: "Point", Coordinates: []float32{x, y}},
		RelativeToGroupRouteId: floor,
	}
	return stateStep{state: portos.State{Operational: api.OperationalStateStopped, Pose: &pose}}
}

func tripBody() map[string]any {
	return map[string]any{
		"point":                  map[string]any{"type": "Point", "coordinates": []float32{1.5, 0}},
		"relativeToGroupRouteId": floor,
	}
}

type tripOutcome struct {
	reply *api.MessageRequest
	err   error
}

// startTrip runs GoToPoint on its own goroutine.
func (r *rig) startTrip(body map[string]any) <-chan tripOutcome {
	out := make(chan tripOutcome, 1)
	go func() {
		reply, err := r.svc.GoToPoint(context.Background(), holdID, body)
		out <- tripOutcome{reply, err}
	}()
	return out
}

// finish advances the fake clock one poll at a time until the trip returns.
func (r *rig) finish(t *testing.T, out <-chan tripOutcome) tripOutcome {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for time.Now().Before(deadline) {
		select {
		case o := <-out:
			return o
		case <-time.After(time.Millisecond):
			r.clock.Advance(motion.TripPollInterval)
		}
	}
	t.Fatal("the trip did not finish")
	return tripOutcome{}
}

func replyPose(t *testing.T, reply *api.MessageRequest) (pose api.SpatialInterfacePoseAttribute, state string) {
	t.Helper()
	require.Equal(t, api.SUCCEEDED, reply.Status)
	require.Len(t, reply.Results, 1)
	raw, err := json.Marshal(reply.Results[0].Message.Body)
	require.NoError(t, err)
	var body struct {
		Pose             api.SpatialInterfacePoseAttribute `json:"pose"`
		OperationalState string                            `json:"operationalState"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	return body.Pose, body.OperationalState
}

func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var coded *holds.Error
	require.ErrorAs(t, err, &coded)
	require.Equal(t, code, coded.Code)
}

func TestGoToPointRunsTheTripAndRestartsTheSession(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(travelling(), travelling(), arrivedAt(1.5, 0))
	o := r.finish(t, r.startTrip(tripBody()))
	require.NoError(t, o.err)

	calls := r.client.snapshot()
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "start-session"}, r.client.names())
	require.Equal(t, rcNamespace, calls[1].namespace)
	require.Equal(t, vacNamespace, calls[2].namespace)
	require.Equal(t, tripBody()["relativeToGroupRouteId"], calls[2].body["relativeToGroupRouteId"])
	require.Equal(t, tripBody()["point"], calls[2].body["point"])
	require.Equal(t, 3, r.state.count())

	pose, state := replyPose(t, o.reply)
	require.Equal(t, []float32{1.5, 0}, pose.Point.Coordinates)
	require.Equal(t, "STOPPED", state)
	cached, _, ok := r.svc.LastPose(holdID)
	require.True(t, ok)
	require.Equal(t, []float32{1.5, 0}, cached.Point.Coordinates)

	// The hold is live for the last metre: a move goes through, with the next seqNum.
	r.move(t, agentMove())
	require.Equal(t, "move", r.client.names()[4])
	require.Equal(t, 1, r.client.snapshot()[4].body["seqNum"])
}

// The S8 reports no pose while it travels or stops outside remote control, so
// the arrival pose is read again once the session is back.
func TestGoToPointReadsTheArrivalPoseAfterTheSessionIsBack(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	stopped := stateStep{state: portos.State{Operational: api.OperationalStateStopped}}
	r.state.set(travelling(), stopped, arrivedAt(1.5, 0))
	o := r.finish(t, r.startTrip(tripBody()))
	require.NoError(t, o.err)
	require.Equal(t, 3, r.state.count())

	pose, state := replyPose(t, o.reply)
	require.Equal(t, []float32{1.5, 0}, pose.Point.Coordinates)
	require.Equal(t, "STOPPED", state)
	cached, _, ok := r.svc.LastPose(holdID)
	require.True(t, ok)
	require.Equal(t, []float32{1.5, 0}, cached.Point.Coordinates)
}

func TestGoToPointTakesTheFrameFromTheLastPose(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.move(t, agentMove()) // moveReply's pose is in the frame "map"
	r.state.set(arrivedAt(1, 1))
	body := tripBody()
	delete(body, "relativeToGroupRouteId")
	require.NoError(t, r.finish(t, r.startTrip(body)).err)
	require.Equal(t, "map", r.client.snapshot()[3].body["relativeToGroupRouteId"])
	require.NotContains(t, body, "relativeToGroupRouteId", "the caller's body is not modified")
}

func TestGoToPointReadsTheFrameWhenNoPoseIsCached(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(arrivedAt(0, 0))
	body := tripBody()
	delete(body, "relativeToGroupRouteId")
	require.NoError(t, r.finish(t, r.startTrip(body)).err)
	require.Equal(t, floor, r.client.snapshot()[2].body["relativeToGroupRouteId"])
}

func TestGoToPointWithoutAFrameIsRefused(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(stateStep{state: portos.State{Operational: api.OperationalStateStopped}})
	body := tripBody()
	delete(body, "relativeToGroupRouteId")
	o := r.finish(t, r.startTrip(body))
	requireCode(t, o.err, holds.CodeInvalidRequest)
	require.Equal(t, []string{"start-session"}, r.client.names(), "nothing was sent: the session is untouched")
}

func TestGoToPointRefusalPassesThroughAndRestartsTheSession(t *testing.T) {
	for _, code := range []string{"invalid_value", "value_out_of_range", "map_not_active", "go_to_point_not_started", "remote_control_active"} {
		t.Run(code, func(t *testing.T) {
			r := newRig(keepalive.ModePing)
			r.open(t)
			refusal := failedReply(code)
			r.client.handler = func(_ int, c call) (*api.MessageRequest, error) {
				if c.name == "go-to-point" {
					return refusal, nil
				}
				return moveReply(0, 0), nil
			}
			o := r.finish(t, r.startTrip(tripBody()))
			require.NoError(t, o.err)
			require.Same(t, refusal, o.reply)
			require.Equal(t, []string{"start-session", "end-session", "go-to-point", "start-session"}, r.client.names())
			require.Zero(t, r.state.count(), "a refused trip waits for nothing")
			r.move(t, agentMove())
		})
	}
}

func TestGoToPointRobotErrorIsTyped(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	reason := "Robot trapped"
	r.state.set(travelling(), stateStep{state: portos.State{Operational: api.OperationalStateError, ErrorReason: reason}})
	o := r.finish(t, r.startTrip(tripBody()))
	requireCode(t, o.err, motion.CodeGoToPointFailed)
	require.Contains(t, o.err.Error(), reason)
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "start-session"}, r.client.names(), "the hold is live again")
}

func TestGoToPointTimeoutStopsTheRobot(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(travelling())
	o := r.finish(t, r.startTrip(tripBody()))
	requireCode(t, o.err, motion.CodeGoToPointTimeout)
	// No keepalive move went out while the session was down for 180 s of fake time.
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "stop", "start-session"}, r.client.names())
	require.Equal(t, vacNamespace, r.client.snapshot()[3].namespace)
	require.GreaterOrEqual(t, r.state.count(), int(motion.TripTimeout/motion.TripPollInterval))
}

func TestGoToPointUnreadableStateIsTyped(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(stateStep{err: errors.New("endpoint query failed")})
	o := r.finish(t, r.startTrip(tripBody()))
	requireCode(t, o.err, motion.CodeGoToPointStateUnavailable)
	require.Equal(t, 3, r.state.count())
	require.Equal(t, "start-session", r.client.names()[len(r.client.names())-1])
}

func TestGoToPointRefusedRestartEndsTheHold(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(arrivedAt(1.5, 0))
	r.client.handler = func(n int, c call) (*api.MessageRequest, error) {
		if c.name == "start-session" {
			return failedReply("remote_control_not_started"), nil
		}
		return moveReply(0, 0), nil
	}
	o := r.finish(t, r.startTrip(tripBody()))
	requireCode(t, o.err, motion.CodeGoToPointSessionLost)
	f := <-r.fatals
	require.Equal(t, holds.ReasonEndpointError, f.reason)
	require.Equal(t, holdID, f.holdID)
	_, err := r.svc.Move(context.Background(), holdID, agentMove())
	require.Error(t, err, "a dead session takes no more moves")
}

func TestGoToPointEndSessionFailureLeavesTheSessionAlone(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.client.handler = func(n int, c call) (*api.MessageRequest, error) {
		if c.name == "end-session" {
			return failedReply("remote_control_busy"), nil
		}
		return moveReply(0, 0), nil
	}
	r.state.set(arrivedAt(1.5, 0))
	o := r.finish(t, r.startTrip(tripBody()))
	require.Error(t, o.err)
	require.Equal(t, []string{"start-session", "end-session"}, r.client.names(), "no go-to-point, and no second start-session")
	r.client.handler = nil
	r.move(t, agentMove())
}

func TestAMoveSentDuringATripWaitsBehindIt(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(travelling(), travelling(), arrivedAt(1.5, 0))
	trip := r.startTrip(tripBody())
	require.Eventually(t, func() bool { return len(r.client.snapshot()) >= 3 }, waitFor, pollEvery) // end-session, go-to-point sent

	moved := make(chan error, 1)
	go func() { _, err := r.svc.Move(context.Background(), holdID, agentMove()); moved <- err }()
	require.Eventually(t, func() bool { return r.svc.Status(holdID).Pending == 2 }, waitFor, pollEvery)
	require.NotContains(t, r.client.names(), "move", "the move did not run during the trip")

	require.NoError(t, r.finish(t, trip).err)
	require.NoError(t, <-moved)
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "start-session", "move"}, r.client.names())
	require.Equal(t, 1, r.client.overlap())
}

func TestKeepaliveWaitsAFullIntervalAfterATrip(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(arrivedAt(1.5, 0))
	require.NoError(t, r.finish(t, r.startTrip(tripBody())).err)
	n := len(r.client.snapshot())
	r.clock.Advance(19 * time.Second)
	require.Len(t, r.client.snapshot(), n, "the trip restarted the 20 s wait")
	r.clock.Advance(time.Second)
	r.waitCalls(t, n+1)
}

func TestCloseDuringATripStopsTheRobotAndDocksIt(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(travelling())
	trip := r.startTrip(tripBody())
	require.Eventually(t, func() bool { return len(r.client.snapshot()) >= 3 }, waitFor, pollEvery)

	closed := make(chan error, 1)
	go func() {
		closed <- r.svc.Close(context.Background(), holdID, holds.Ending{Reason: holds.ReasonReleased})
	}()
	require.NoError(t, <-closed)
	o := <-trip
	require.Error(t, o.err)
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "stop", "return-to-dock"}, r.client.names(), "the session was already down, so close sends only the dock")
}

func TestCallerGivingUpStopsTheTripAndRestartsTheSession(t *testing.T) {
	r := newRig(keepalive.ModePing)
	r.open(t)
	r.state.set(travelling())
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan tripOutcome, 1)
	go func() {
		reply, err := r.svc.GoToPoint(ctx, holdID, tripBody())
		out <- tripOutcome{reply, err}
	}()
	require.Eventually(t, func() bool { return len(r.client.snapshot()) >= 3 }, waitFor, pollEvery)
	cancel()
	o := <-out
	require.ErrorIs(t, o.err, context.Canceled)
	require.Equal(t, []string{"start-session", "end-session", "go-to-point", "stop", "start-session"}, r.client.names())
}

func TestGoToPointWithoutAStateReaderIsRefused(t *testing.T) {
	r := newRig(keepalive.ModePing)
	svc := motion.New(r.client, nil, r.clock, keepalive.ModePing, nil)
	require.NoError(t, svc.Open(context.Background(), holdID, endpointID))
	_, err := svc.GoToPoint(context.Background(), holdID, tripBody())
	require.Error(t, err)
	require.Equal(t, []string{"start-session"}, r.client.names())
}
