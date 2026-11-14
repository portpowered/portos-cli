package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/stretchr/testify/require"
)

type stubHolds struct {
	holds []host.HoldStatus
}

func (s *stubHolds) List() []host.HoldStatus { return s.holds }
func (s *stubHolds) Open(_ context.Context, req host.OpenHoldRequest) (host.OpenedHold, error) {
	if req.EndpointID == "ep/held" {
		return host.OpenedHold{}, &host.CodedError{Code: "hold_owned_by_other", Message: "already held"}
	}
	h := host.HoldStatus{ID: "hold-1", EndpointID: req.EndpointID, Kinds: req.Kinds, State: "live", DeadManSeconds: req.DeadManSeconds}
	s.holds = append(s.holds, h)
	return host.OpenedHold{HoldStatus: h, Lease: "secret-lease"}, nil
}
func (s *stubHolds) Get(id string) (host.HoldStatus, error) {
	for _, h := range s.holds {
		if h.ID == id {
			return h, nil
		}
	}
	return host.HoldStatus{}, &host.CodedError{Code: host.CodeHoldNotFound, Message: "no hold " + id}
}
func (s *stubHolds) Release(_ context.Context, id string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	for i := range s.holds {
		s.holds[i].State = host.StateEnded
	}
	return nil
}

type stubMessages struct{ lease string }

func (s *stubMessages) Dispatch(_ context.Context, lease string, _ *api.CreateMessageRequest) (*api.MessageRequest, error) {
	s.lease = lease
	return &api.MessageRequest{Id: "r1", Status: api.SUCCEEDED}, nil
}

func runInProcess(t *testing.T, services host.Services) string {
	t.Helper()
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- host.Run(ctx, host.Options{ConfigDir: dir, Services: services}) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	require.Eventually(t, func() bool {
		_, err := Dial(context.Background(), dir)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return dir
}

func TestHoldRoutesRoundTrip(t *testing.T) {
	messages := &stubMessages{}
	dir := runInProcess(t, host.Services{Holds: &stubHolds{}, Messages: messages})
	ctx := context.Background()
	c, err := Dial(ctx, dir)
	require.NoError(t, err)

	opened, err := c.OpenHold(ctx, OpenHoldRequest{EndpointID: "ep/1", Kinds: []string{"motion"}, DeadManSeconds: 60})
	require.NoError(t, err)
	require.Equal(t, "secret-lease", opened.Lease)
	require.Equal(t, 60, opened.DeadManSeconds)

	got, err := c.GetHold(ctx, opened.ID)
	require.NoError(t, err)
	require.Equal(t, "ep/1", got.EndpointID)
	listed, err := c.ListHolds(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	body, err := json.Marshal(listed)
	require.NoError(t, err)
	require.NotContains(t, string(body), "secret-lease", "the lease is returned only when the hold opens")

	reply, err := c.Send(ctx, "secret-lease", &api.CreateMessageRequest{})
	require.NoError(t, err)
	require.Equal(t, "r1", reply.Id)
	require.Equal(t, "secret-lease", messages.lease)

	ended, err := c.ReleaseHold(ctx, opened.ID)
	require.NoError(t, err)
	require.Equal(t, host.StateEnded, ended.State)
}

func TestHoldErrorsCarryTheHostCode(t *testing.T) {
	dir := runInProcess(t, host.Services{Holds: &stubHolds{}})
	ctx := context.Background()
	c, err := Dial(ctx, dir)
	require.NoError(t, err)

	_, err = c.OpenHold(ctx, OpenHoldRequest{EndpointID: "ep/held", Kinds: []string{"motion"}})
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, "hold_owned_by_other", statusErr.Code)
	require.Equal(t, exitcode.ExitHold, exitcode.Code(err))

	_, err = c.GetHold(ctx, "nope")
	require.False(t, errors.Is(err, ErrRouteNotServed), "a missing hold is not a missing route")
	require.Equal(t, 404, func() int { var e *StatusError; _ = errors.As(err, &e); return e.StatusCode }())
}

// A host that refuses with 503 is closing: OpenHold must wait for it to go and
// retry on a fresh host. The fresh host here serves no holds, so the retry
// shows as a 404 route-not-served rather than the first host's 503.
func TestOpenHoldRetriesOnClosingHost(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	old := hostExecutable
	hostExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { hostExecutable = old })
	t.Setenv(testHostEnv, "1")

	dir := shortDir(t)
	require.NoError(t, os.MkdirAll(dir+"/run", 0o700))
	listener, err := net.Listen("unix", host.SocketPath(dir))
	require.NoError(t, err)
	mux := http.NewServeMux()
	var srv *http.Server
	mux.HandleFunc("POST /holds", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"host_closing","message":"shutting down"}`))
		go func() {
			time.Sleep(100 * time.Millisecond)
			_ = srv.Close()
			_ = os.Remove(host.SocketPath(dir))
		}()
	})
	srv = &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()

	c, err := Dial(context.Background(), dir)
	require.NoError(t, err)
	_, err = c.OpenHold(context.Background(), OpenHoldRequest{EndpointID: "ep/1", Kinds: []string{"motion"}})
	require.ErrorIs(t, err, ErrRouteNotServed)
}

type stubRecordings struct{ t *testing.T }

func (s stubRecordings) Record(_ context.Context, lease, endpointID string, duration time.Duration) (host.Recording, error) {
	if lease != "secret-lease" {
		return host.Recording{}, &host.CodedError{Code: "hold_lease_required", Message: "pass the lease"}
	}
	file, err := os.CreateTemp(s.t.TempDir(), "held-*.h264")
	require.NoError(s.t, err)
	_, err = file.WriteString(endpointID + " " + duration.String())
	require.NoError(s.t, err)
	require.NoError(s.t, file.Close())
	return host.Recording{Path: file.Name(), Codec: "H264", FrameRate: 14.97}, nil
}

func (s stubRecordings) Capture(_ context.Context, lease, endpointID string, from time.Time, duration time.Duration) (host.AudioCapture, error) {
	if lease != "secret-lease" {
		return host.AudioCapture{}, &host.CodedError{Code: "hold_lease_required", Message: "pass the lease"}
	}
	if endpointID == "ep/deaf" {
		return host.AudioCapture{}, &host.CodedError{Code: "hold_not_listening", Message: "hold it with --listen"}
	}
	if from.IsZero() {
		from = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	}
	file, err := os.CreateTemp(s.t.TempDir(), "held-*.ogg")
	require.NoError(s.t, err)
	_, err = file.WriteString("OggS " + endpointID + " " + duration.String())
	require.NoError(s.t, err)
	require.NoError(s.t, file.Close())
	return host.AudioCapture{Path: file.Name(), From: from, Until: from.Add(duration), FirstPacketAt: from.Add(20 * time.Millisecond), Packets: 150}, nil
}

func TestCaptureCarriesTheHeldAudioAndItsWindow(t *testing.T) {
	dir := runInProcess(t, host.Services{Recordings: stubRecordings{t: t}})
	client, err := Dial(t.Context(), dir)
	require.NoError(t, err)

	from := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	capture, err := client.Capture(t.Context(), "secret-lease", "ep/robot", from, 3*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(capture.Path) })
	require.True(t, capture.From.Equal(from), capture.From)
	require.True(t, capture.Until.Equal(from.Add(3*time.Second)), capture.Until)
	require.True(t, capture.FirstPacketAt.Equal(from.Add(20*time.Millisecond)), capture.FirstPacketAt)
	require.Equal(t, 150, capture.Packets)
	require.Equal(t, ".ogg", filepath.Ext(capture.Path))
	data, err := os.ReadFile(capture.Path)
	require.NoError(t, err)
	require.Equal(t, "OggS ep/robot 3s", string(data))

	// From now: the host picks the start.
	capture, err = client.Capture(t.Context(), "secret-lease", "ep/robot", time.Time{}, time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(capture.Path) })
	require.False(t, capture.From.IsZero())

	_, err = client.Capture(t.Context(), "", "ep/robot", from, time.Second)
	require.Equal(t, exitcode.ExitHold, exitcode.Code(err))
	_, err = client.Capture(t.Context(), "secret-lease", "ep/deaf", from, time.Second)
	require.Equal(t, exitcode.ExitHold, exitcode.Code(err))
	_, err = client.Capture(t.Context(), "secret-lease", "ep/robot", from, 11*time.Minute)
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, host.CodeInvalidRequest, statusErr.Code)
}

func TestRecordCarriesTheHeldVideoAndItsTiming(t *testing.T) {
	dir := runInProcess(t, host.Services{Recordings: stubRecordings{t: t}})
	client, err := Dial(t.Context(), dir)
	require.NoError(t, err)

	clip, err := client.Record(t.Context(), "secret-lease", "ep/robot", 3*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(clip.Path) })
	require.Equal(t, "H264", clip.Codec)
	require.InDelta(t, 14.97, clip.FrameRate, 1e-9)
	data, err := os.ReadFile(clip.Path)
	require.NoError(t, err)
	require.Equal(t, "ep/robot 3s", string(data))

	_, err = client.Record(t.Context(), "", "ep/robot", 3*time.Second)
	require.Equal(t, exitcode.ExitHold, exitcode.Code(err))
	_, err = client.Record(t.Context(), "secret-lease", "ep/robot", 11*time.Minute)
	var statusErr *StatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, host.CodeInvalidRequest, statusErr.Code)
}
