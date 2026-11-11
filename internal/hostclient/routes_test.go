package hostclient

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
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
