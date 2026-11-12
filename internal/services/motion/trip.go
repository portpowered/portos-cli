package motion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/portos"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

const frameKey = "relativeToGroupRouteId"

// goToPoint runs one trip on the hold's goroutine. Nothing else sends while it
// runs: queued moves wait behind it and the keepalive timer is not re-armed
// until the session is back.
func (s *session) goToPoint(ctx context.Context, body map[string]any) (*api.MessageRequest, error) {
	if s.svc.state == nil {
		return nil, errors.New("motion: this session host cannot read the robot's state, so it cannot run a go-to-point trip")
	}
	body, err := s.withFrame(ctx, body)
	if err != nil {
		return nil, err
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.pendingMirror = false

	// 1. End the session only (no return-to-dock); the robot refuses go-to-point under remote control.
	end := api.INTERFACE_PORTOS_REMOTE_CONTROL_END_SESSION
	reply, err := s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_REMOTE_CONTROL, end, map[string]any{})
	if err == nil && portos.HasCode(reply, providerNotActive) {
		s.fatal(holds.ReasonRemoteControlNotActive, "the robot is not in remote control; it is probably on its dock")
	} else if err != nil && portos.IsUnauthorized(err) {
		s.fatal(holds.ReasonAuthRejected, err.Error())
	}
	if err := replyError(end, reply, err); err != nil {
		s.arm() // the session is as it was
		return nil, err
	}
	s.mu.Lock()
	s.down = true
	s.mu.Unlock()

	// 2. Send go-to-point. A refusal leaves the robot where it is.
	reply, err = s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_GO_TO_POINT, body)
	if err != nil || reply == nil || reply.Status != api.SUCCEEDED {
		if restartErr := s.restartSession(); restartErr != nil {
			return reply, restartErr
		}
		if err == nil && reply == nil {
			err = errors.New("go-to-point: no reply")
		}
		return reply, err
	}

	// 3. Wait for the robot to leave GOING_TO_POINT.
	arrived, waitErr := s.awaitArrival(ctx)
	if waitErr != nil {
		if errors.Is(waitErr, errEnded) {
			s.stopTrip() // a close is waiting: stop the robot, leave the session down, the close docks it
			return nil, waitErr
		}
		if isTimeout(waitErr) || ctx.Err() != nil {
			s.stopTrip()
		}
		if restartErr := s.restartSession(); restartErr != nil {
			return nil, restartErr
		}
		return nil, waitErr
	}

	// 4. The hold is live again for the last metre, and the pose is the arrival pose.
	if err := s.restartSession(); err != nil {
		return nil, err
	}
	if arrived.Pose == nil {
		// The S8 reports no pose outside remote control; with the session back it does.
		arrived.Pose = s.readPose()
	}
	if arrived.Pose != nil {
		s.mu.Lock()
		s.pose = arrived.Pose
		s.poseAt = s.svc.clock.Now()
		s.mu.Unlock()
	}
	return s.arrivalReply(arrived), nil
}

// withFrame returns a copy of body whose frame is set: the caller's, else the
// frame of the newest pose, else the one the robot reports now.
func (s *session) withFrame(ctx context.Context, body map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	if frame, _ := out[frameKey].(string); frame != "" {
		return out, nil
	}
	s.mu.Lock()
	pose := s.pose
	s.mu.Unlock()
	if pose == nil {
		state, err := s.svc.state.ReadState(ctx, s.endpoint)
		if err != nil {
			return nil, &holds.Error{Code: holds.CodeInvalidRequest, Message: "the go-to-point body names no " + frameKey + " and the robot's pose frame could not be read: " + err.Error()}
		}
		pose = state.Pose
	}
	if pose == nil || pose.RelativeToGroupRouteId == "" {
		return nil, &holds.Error{Code: holds.CodeInvalidRequest, Message: "the go-to-point body names no " + frameKey + " and the robot reports no pose to take it from; send the frame its pose uses"}
	}
	out[frameKey] = pose.RelativeToGroupRouteId
	return out, nil
}

// awaitArrival polls the operational state until it is neither GOING_TO_POINT
// nor unreported. ERROR, the trip timeout, unreadable state, the caller giving
// up and a close all end the wait with an error; a close is errEnded.
func (s *session) awaitArrival(ctx context.Context) (portos.State, error) {
	deadline := s.svc.clock.Now().Add(TripTimeout)
	failures := 0
	for {
		state, err := s.svc.state.ReadState(ctx, s.endpoint)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return portos.State{}, context.Cause(ctx)
			}
			if failures++; failures >= tripStateFailures {
				return portos.State{}, &holds.Error{Code: CodeGoToPointStateUnavailable, Message: "the robot's operational state could not be read during the trip: " + err.Error()}
			}
		case state.Operational == api.OperationalStateError:
			reason := state.ErrorReason
			if reason == "" {
				reason = "no reason given"
			}
			return portos.State{}, &holds.Error{Code: CodeGoToPointFailed, Message: "the robot reported ERROR on its way to the point (" + reason + ")"}
		case state.Operational != "" && state.Operational != api.OperationalStateGoingToPoint:
			return state, nil
		default:
			failures = 0
		}
		if !s.svc.clock.Now().Before(deadline) {
			return portos.State{}, &holds.Error{Code: CodeGoToPointTimeout, Message: fmt.Sprintf("the robot was still travelling after %s; it was told to stop", TripTimeout)}
		}
		if err := s.sleep(ctx, TripPollInterval); err != nil {
			return portos.State{}, err
		}
	}
}

// sleep waits d on the hold's clock. A close is errEnded, a dead session is
// errEnded, a done context is its cause.
func (s *session) sleep(ctx context.Context, d time.Duration) error {
	tick := make(chan struct{}, 1)
	timer := s.svc.clock.AfterFunc(d, func() { signal(tick) })
	defer timer.Stop()
	select {
	case <-tick:
		if s.isDead() {
			return errEnded
		}
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-s.closing:
		return errEnded
	}
}

func isTimeout(err error) bool {
	var coded *holds.Error
	return errors.As(err, &coded) && coded.Code == CodeGoToPointTimeout
}

// stopTrip tells the robot to stop where it is. It runs even if the caller gave up.
func (s *session) stopTrip() {
	ctx, cancel := context.WithTimeout(context.Background(), tripCleanupTimeout)
	defer cancel()
	_, _ = s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_STOP, map[string]any{})
}

// restartSession starts the remote-control session again after a trip. The
// robot stands off its dock, so a start that timed out or got no answer is
// sent again, up to RestartAttempts tries. When Port OS refuses, or every try
// fails, the hold ends with a fatal reason, like any session failure.
func (s *session) restartSession() error {
	start := api.INTERFACE_PORTOS_REMOTE_CONTROL_START_SESSION
	var err error
	for try := 1; ; try++ {
		reply, sendErr := s.startOnce()
		err = replyError(start, reply, sendErr)
		if err == nil || try == RestartAttempts || !startWorthRetrying(reply, sendErr) {
			break
		}
		if s.sleep(context.Background(), RestartRetryPause) != nil {
			break // a close is waiting: it docks the robot
		}
	}
	if err != nil {
		detail := "the remote-control session could not be started after the go-to-point trip: " + err.Error()
		reason := holds.ReasonEndpointError
		if portos.IsUnauthorized(err) {
			reason = holds.ReasonAuthRejected
		}
		s.fatal(reason, detail)
		return &holds.Error{Code: CodeGoToPointSessionLost, Message: detail + "; the hold has ended"}
	}
	s.mu.Lock()
	s.down = false
	s.lastMoveAt = s.svc.clock.Now() // the trip is activity: the keepalive waits a full interval
	s.mu.Unlock()
	s.arm()
	return nil
}

// startOnce sends one start-session on its own budget.
func (s *session) startOnce() (*api.MessageRequest, error) {
	ctx, cancel := context.WithTimeout(context.Background(), tripCleanupTimeout)
	defer cancel()
	return s.svc.client.SendSync(ctx, s.endpoint, api.INTERFACE_PORTOS_REMOTE_CONTROL, api.INTERFACE_PORTOS_REMOTE_CONTROL_START_SESSION, map[string]any{})
}

// startWorthRetrying reports whether a failed start-session after a trip may
// succeed when sent again: the provider timed out, no answer came back, or the
// robot was still moving when the wait ended.
func startWorthRetrying(reply *api.MessageRequest, err error) bool {
	return startMayHaveLanded(reply, err) || portos.HasCode(reply, providerNotStarted)
}

// readPose reads the pose once after the session is back, or nil when the robot reports none.
func (s *session) readPose() *api.SpatialInterfacePoseAttribute {
	ctx, cancel := context.WithTimeout(context.Background(), tripCleanupTimeout)
	defer cancel()
	state, err := s.svc.state.ReadState(ctx, s.endpoint)
	if err != nil {
		return nil
	}
	return state.Pose
}

// arrivalReply is the host's answer to a finished trip: a SUCCEEDED request
// whose one result carries the arrival pose and operational state.
func (s *session) arrivalReply(state portos.State) *api.MessageRequest {
	body := map[string]any{"operationalState": string(state.Operational)}
	if state.Pose != nil {
		if raw, err := json.Marshal(state.Pose); err == nil {
			var pose map[string]any
			if json.Unmarshal(raw, &pose) == nil {
				body["pose"] = pose
			}
		}
	}
	messageType := strings.Join([]string{api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, "go-to-point-response"}, "/")
	now := s.svc.clock.Now()
	return &api.MessageRequest{
		Id:                  "host-" + uuid.NewString(),
		Status:              api.SUCCEEDED,
		Deadline:            now,
		SyncDeadlineSeconds: 1,
		Results: []api.MessageRequestResult{{
			TargetId: s.endpoint,
			Message: &api.Message{
				Type:   &messageType,
				Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, Name: "go-to-point-response", Version: "1.0"},
				Body:   body,
			},
		}},
		Errors: []api.MessageRequestError{},
	}
}
