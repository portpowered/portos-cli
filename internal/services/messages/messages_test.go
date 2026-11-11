package messages

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/stretchr/testify/require"
)

type fakeAuth struct {
	err             error
	lease, endpoint string
	kind            holds.Kind
}

func (f *fakeAuth) Authorize(lease, endpointID string, kind holds.Kind) (holds.Hold, error) {
	f.lease, f.endpoint, f.kind = lease, endpointID, kind
	if f.err != nil {
		return holds.Hold{}, f.err
	}
	return holds.Hold{ID: "hold-1", EndpointID: endpointID}, nil
}

type fakeMover struct {
	err    error
	holdID string
	body   map[string]any
	reply  *api.MessageRequest
}

func (f *fakeMover) Move(_ context.Context, holdID string, body map[string]any) (*api.MessageRequest, error) {
	f.holdID, f.body = holdID, body
	return f.reply, f.err
}

func request(name string) *api.CreateMessageRequest {
	ns := api.INTERFACE_PORTOS_REMOTE_CONTROL
	return &api.CreateMessageRequest{
		Target:  api.CreateMessageTarget{Type: api.EndpointTarget, Id: "ep/1"},
		Message: api.Message{Header: &api.MessageHeader{Namespace: ns, Name: name, Version: "1.0"}, Body: map[string]any{"velocity": 0.2}},
	}
}

type fakeSnapshots struct {
	holdID string
	jpeg   []byte
	err    error
}

func (f *fakeSnapshots) Snapshot(_ context.Context, holdID string) ([]byte, time.Time, error) {
	f.holdID = holdID
	return f.jpeg, time.Unix(2000, 0), f.err
}

func newService(auth *fakeAuth, mover *fakeMover) Service {
	return New(auth, mover, &fakeSnapshots{}, clock.NewFake(time.Unix(1000, 0)))
}

func TestDispatchRoutesMove(t *testing.T) {
	auth := &fakeAuth{}
	want := &api.MessageRequest{Id: "from-port-os", Status: api.SUCCEEDED}
	mover := &fakeMover{reply: want}
	reply, err := newService(auth, mover).Dispatch(t.Context(), "the-lease", request("move"))
	require.NoError(t, err)
	require.Same(t, want, reply)
	require.Equal(t, "hold-1", mover.holdID)
	require.Equal(t, 0.2, mover.body["velocity"])
	require.Equal(t, "the-lease", auth.lease)
	require.Equal(t, "ep/1", auth.endpoint)
	require.Equal(t, holds.KindMotion, auth.kind)
}

func TestDispatchRejectsStartSession(t *testing.T) {
	for _, name := range []string{"start-session", "end-session"} {
		mover := &fakeMover{}
		reply, err := newService(&fakeAuth{}, mover).Dispatch(t.Context(), "the-lease", request(name))
		require.NoError(t, err)
		require.Equal(t, api.FAILED, reply.Status)
		require.Equal(t, holds.CodeHoldOwnsSession, exitcode.ProviderCode(reply))
		require.Equal(t, api.REJECTED, reply.Errors[0].Code)
		require.Equal(t, exitcode.ExitHold, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}))
		require.Empty(t, mover.holdID, "a rejected session message must not move the robot")
	}
}

func TestDispatchNotServed(t *testing.T) {
	req := request("turn-on")
	req.Message.Header.Namespace = api.INTERFACE_PORTOS_POWER
	reply, err := newService(&fakeAuth{}, &fakeMover{}).Dispatch(t.Context(), "the-lease", req)
	require.NoError(t, err)
	require.Equal(t, holds.CodeNotServedByHold, exitcode.ProviderCode(reply))
}

func TestDispatchHoldErrorsBecomeReplies(t *testing.T) {
	auth := &fakeAuth{err: &holds.Error{Code: holds.CodeHoldLeaseRequired, Message: "pass the lease"}}
	reply, err := newService(auth, &fakeMover{}).Dispatch(t.Context(), "", request("move"))
	require.NoError(t, err)
	require.Equal(t, holds.CodeHoldLeaseRequired, exitcode.ProviderCode(reply))
	require.Equal(t, exitcode.ExitHold, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}))
	require.Equal(t, "ep/1", reply.Errors[0].TargetId)
}

// authSequence authorizes the first call and reports the hold ended afterwards.
type authSequence struct{ calls int }

func (a *authSequence) Authorize(_, endpointID string, _ holds.Kind) (holds.Hold, error) {
	a.calls++
	if a.calls > 1 {
		return holds.Hold{}, &holds.Error{Code: holds.CodeHoldEnded, Message: "hold-1 has ended"}
	}
	return holds.Hold{ID: "hold-1", EndpointID: endpointID}, nil
}

func TestDispatchSessionEndedBetweenAuthorizeAndMove(t *testing.T) {
	mover := &fakeMover{err: errors.New("motion: the motion session has ended")}
	reply, err := New(&authSequence{}, mover, &fakeSnapshots{}, clock.NewFake(time.Unix(1000, 0))).Dispatch(t.Context(), "lease", request("move"))
	require.NoError(t, err)
	require.Equal(t, holds.CodeHoldEnded, exitcode.ProviderCode(reply))
	require.Equal(t, exitcode.ExitHold, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}))
}

func TestDispatchMoveErrorWhileHoldLiveStaysAnError(t *testing.T) {
	mover := &fakeMover{err: errors.New("transport down")}
	_, err := newService(&fakeAuth{}, mover).Dispatch(t.Context(), "lease", request("move"))
	require.Error(t, err)
}

func snapshotRequest(name string) *api.CreateMessageRequest {
	return &api.CreateMessageRequest{
		Target:  api.CreateMessageTarget{Type: api.EndpointTarget, Id: "ep/1"},
		Message: api.Message{Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_SNAPSHOT, Name: name, Version: "1.0"}, Body: map[string]any{"responseFormat": "BASE64"}},
	}
}

func TestDispatchServesASnapshotFromTheHeldCamera(t *testing.T) {
	auth := &fakeAuth{}
	snaps := &fakeSnapshots{jpeg: []byte{0xff, 0xd8, 0x01}}
	reply, err := New(auth, &fakeMover{}, snaps, clock.NewFake(time.Unix(1000, 0))).Dispatch(t.Context(), "the-lease", snapshotRequest("get-snapshot"))
	require.NoError(t, err)
	require.Equal(t, holds.KindCamera, auth.kind)
	require.Equal(t, "hold-1", snaps.holdID)
	require.Equal(t, api.SUCCEEDED, reply.Status)
	require.Len(t, reply.Results, 1)
	message := reply.Results[0].Message
	require.Equal(t, api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT_RESPONSE, message.Header.Name)
	image := message.Body["image"].(map[string]any)
	require.Equal(t, "image/jpeg", image["mimeType"])
	require.Equal(t, "/9gB", image["data"]) // base64 of ff d8 01
	require.Equal(t, "1970-01-01T00:33:20Z", message.Body["timeOfSample"])
}

func TestDispatchSnapshotRefusals(t *testing.T) {
	unavailable := &holds.Error{Code: holds.CodeCameraUnavailable, Message: "the held camera stopped"}
	reply, err := newServiceWithSnapshots(&fakeAuth{}, &fakeSnapshots{err: unavailable}).Dispatch(t.Context(), "lease", snapshotRequest("get-snapshot"))
	require.NoError(t, err)
	require.Equal(t, holds.CodeCameraUnavailable, exitcode.ProviderCode(reply))
	require.Equal(t, exitcode.ExitCamera, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}))

	noCamera := &fakeAuth{err: &holds.Error{Code: holds.CodeHoldLeaseRequired, Message: "no lease"}}
	reply, err = newServiceWithSnapshots(noCamera, &fakeSnapshots{}).Dispatch(t.Context(), "", snapshotRequest("get-snapshot"))
	require.NoError(t, err)
	require.Equal(t, holds.CodeHoldLeaseRequired, exitcode.ProviderCode(reply))

	reply, err = newServiceWithSnapshots(&fakeAuth{}, &fakeSnapshots{}).Dispatch(t.Context(), "lease", snapshotRequest("get-event-image"))
	require.NoError(t, err)
	require.Equal(t, holds.CodeNotServedByHold, exitcode.ProviderCode(reply))

	_, err = newServiceWithSnapshots(&fakeAuth{}, &fakeSnapshots{err: errors.New("disk full")}).Dispatch(t.Context(), "lease", snapshotRequest("get-snapshot"))
	require.Error(t, err)
}

func newServiceWithSnapshots(auth *fakeAuth, snaps *fakeSnapshots) Service {
	return New(auth, &fakeMover{}, snaps, clock.NewFake(time.Unix(1000, 0)))
}
