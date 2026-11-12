package hostservices

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
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
func (s *stubStream) Play(context.Context, []byte) (time.Duration, error) { return time.Second, nil }
func (s *stubStream) Record(context.Context, time.Duration) (camera.Clip, error) {
	return camera.Clip{Path: "held.h264", Codec: "H264", FrameRate: 15}, nil
}
func (s *stubStream) PTZ(context.Context, string, map[string]any) (*api.MessageRequest, error) {
	return &api.MessageRequest{Id: "step", Status: api.SUCCEEDED}, nil
}
func (s *stubStream) ExpiresAt() time.Time        { return time.Time{} }
func (s *stubStream) EndReason() string           { return "" }
func (s *stubStream) Done() <-chan struct{}       { return s.done }
func (s *stubStream) Close(context.Context) error { s.end(); return nil }
func (s *stubStream) end()                        { s.once.Do(func() { close(s.done) }) }

type stubOpener struct {
	mu      sync.Mutex
	streams []*stubStream
	shared  int
}

func (o *stubOpener) Open(context.Context, string, time.Duration) (camera.Stream, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &stubStream{done: make(chan struct{})}
	o.streams = append(o.streams, s)
	return s, nil
}

func (o *stubOpener) OpenShared(ctx context.Context, endpointID string, lifetime time.Duration) (camera.Stream, error) {
	o.mu.Lock()
	o.shared++
	o.mu.Unlock()
	return o.Open(ctx, endpointID, lifetime)
}

func (o *stubOpener) sharedOpens() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.shared
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

func TestRecordingsNeedTheCameraHoldsLease(t *testing.T) {
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default, Camera: &stubOpener{}})
	opened, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"camera"}})
	require.NoError(t, err)

	clip, err := built.Services.Recordings.Record(t.Context(), opened.Lease, heldEndpoint, 3*time.Second)
	require.NoError(t, err)
	require.Equal(t, host.Recording{Path: "held.h264", Codec: "H264", FrameRate: 15}, clip)

	_, err = built.Services.Recordings.Record(t.Context(), "", heldEndpoint, 3*time.Second)
	requireHostCode(t, err, holds.CodeHoldLeaseRequired)
	_, err = built.Services.Recordings.Record(t.Context(), "not-the-lease", heldEndpoint, 3*time.Second)
	requireHostCode(t, err, holds.CodeHoldOwnedByOther)

	noCamera := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default})
	_, err = noCamera.Services.Recordings.Record(t.Context(), opened.Lease, heldEndpoint, 3*time.Second)
	requireHostCode(t, err, holds.CodeCameraUnavailable)
}

func requireHostCode(t *testing.T, err error, code string) {
	t.Helper()
	var coded *host.CodedError
	require.ErrorAs(t, err, &coded)
	require.Equal(t, code, coded.Code)
}

func TestASharedCameraHoldOpensSharedStepsAndRefusesTalkback(t *testing.T) {
	opener := &stubOpener{}
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default, Camera: opener})
	opened, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"camera"}, Shared: true})
	require.NoError(t, err)
	require.Equal(t, 1, opener.sharedOpens())
	require.NotNil(t, opened.Camera)
	require.True(t, opened.Camera.Shared)

	step := &api.CreateMessageRequest{
		Target:  api.CreateMessageTarget{Type: api.EndpointTarget, Id: heldEndpoint},
		Message: api.Message{Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_CAMERA, Name: api.INTERFACE_PORTOS_CAMERA_STEP_PTZ, Version: "1.0"}, Body: map[string]any{"direction": "UP"}},
	}
	reply, err := built.Services.Messages.Dispatch(t.Context(), opened.Lease, step)
	require.NoError(t, err)
	require.Equal(t, api.SUCCEEDED, reply.Status)
	reply, err = built.Services.Messages.Dispatch(t.Context(), "", step)
	require.NoError(t, err)
	require.Equal(t, holds.CodeHoldLeaseRequired, exitcode.ProviderCode(reply))

	clip := base64.StdEncoding.EncodeToString([]byte("OggS"))
	play := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: heldEndpoint},
		Message: api.Message{
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_MEDIA_PLAYER, Name: api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO, Version: "1.0"},
			Body:   map[string]any{"audio": map[string]any{"data": clip, "mimeType": "audio/ogg"}},
		},
	}
	reply, err = built.Services.Messages.Dispatch(t.Context(), opened.Lease, play)
	require.NoError(t, err)
	require.Equal(t, holds.CodeSharedHoldNoTalkback, exitcode.ProviderCode(reply))
	require.Equal(t, exitcode.ExitBusy, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}))
}

func TestASharedHoldNeedsTheCameraKind(t *testing.T) {
	built := New(Options{Clock: clock.NewFake(time.Unix(1000, 0)), Portos: okPortos{}, Keepalive: keepalive.Default, Camera: &stubOpener{}})
	_, err := built.Services.Holds.Open(t.Context(), host.OpenHoldRequest{EndpointID: heldEndpoint, Kinds: []string{"motion"}, Shared: true})
	requireHostCode(t, err, host.CodeInvalidRequest)
}
