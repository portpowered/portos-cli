package hostservices

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/services/camera"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/stretchr/testify/require"
)

type stubStream struct {
	done chan struct{}
	once sync.Once
}

func (s *stubStream) Latest(context.Context, time.Duration) ([]byte, time.Time, error) {
	return []byte{0xff, 0xd8}, time.Unix(1000, 0), nil
}
func (s *stubStream) Done() <-chan struct{}       { return s.done }
func (s *stubStream) Close(context.Context) error { s.end(); return nil }
func (s *stubStream) end()                        { s.once.Do(func() { close(s.done) }) }

type stubOpener struct {
	mu      sync.Mutex
	streams []*stubStream
}

func (o *stubOpener) Open(context.Context, string, time.Duration) (camera.Stream, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &stubStream{done: make(chan struct{})}
	o.streams = append(o.streams, s)
	return s, nil
}

func (o *stubOpener) last() *stubStream {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streams[len(o.streams)-1]
}

const heldEndpoint = "ep/AAAAAAAAAAAAAAAAAAAAAA"

func snapshotThroughHost(t *testing.T, built Built, lease string) *api.MessageRequest {
	t.Helper()
	reply, err := built.Services.Messages.Dispatch(t.Context(), lease, &api.CreateMessageRequest{
		Target:  api.CreateMessageTarget{Type: api.EndpointTarget, Id: heldEndpoint},
		Message: api.Message{Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_SNAPSHOT, Name: "get-snapshot", Version: "1.0"}},
	})
	require.NoError(t, err)
	return reply
}

func TestACameraOnlyHoldEndsWhenAnotherViewerTakesTheCamera(t *testing.T) {
	opener := &stubOpener{}
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default, Camera: opener})
	opened, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"camera"}})
	require.NoError(t, err)
	require.NotNil(t, opened.Camera)
	require.True(t, opened.Camera.Live)
	require.Nil(t, opened.Motion)

	reply := snapshotThroughHost(t, built, opened.Lease)
	require.Equal(t, api.SUCCEEDED, reply.Status)

	opener.last().end()
	require.Eventually(t, func() bool {
		got, err := built.Services.Holds.Get(opened.ID)
		return err == nil && got.State == host.StateEnded && got.Ending.Reason == string(holds.ReasonPreempted)
	}, time.Second, 10*time.Millisecond)
}

func TestAMotionAndCameraHoldKeepsDrivingWhenTheCameraIsTaken(t *testing.T) {
	opener := &stubOpener{}
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default, Camera: opener})
	opened, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"motion", "camera"}})
	require.NoError(t, err)
	require.NotNil(t, opened.Motion)
	require.NotNil(t, opened.Camera)

	opener.last().end()
	require.Eventually(t, func() bool {
		got, err := built.Services.Holds.Get(opened.ID)
		return err == nil && got.Camera != nil && !got.Camera.Live && got.Camera.LostReason == string(holds.ReasonPreempted)
	}, time.Second, 10*time.Millisecond)
	got, err := built.Services.Holds.Get(opened.ID)
	require.NoError(t, err)
	require.Equal(t, string(holds.StateLive), got.State)

	reply := snapshotThroughHost(t, built, opened.Lease)
	require.Equal(t, holds.CodeCameraUnavailable, exitcode.ProviderCode(reply))
}

func TestWithoutACameraOpenerAHoldCannotCoverTheCamera(t *testing.T) {
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default})
	_, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"camera"}})
	require.Error(t, err)
}
