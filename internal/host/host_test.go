package host

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/stretchr/testify/require"
)

func waitForSocket(t *testing.T, dir string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.Dial("unix", filepath.Join(dir, "run", "host.sock"))
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)
}

func TestWatchdogReportsPause(t *testing.T) {
	fake := clock.NewFake(time.Unix(1000, 0))
	pauses := make(chan time.Duration, 4)
	stop := StartWatchdog(fake, time.Second, 20*time.Second, func(gap time.Duration) { pauses <- gap })
	defer stop()

	fake.Advance(1500 * time.Millisecond)
	fake.Advance(time.Second)
	select {
	case gap := <-pauses:
		t.Fatalf("unexpected pause %v", gap)
	default:
	}

	fake.Advance(25 * time.Second)
	fake.Advance(time.Second) // the next delivery proves the previous tick was handled
	require.Equal(t, 25*time.Second, <-pauses)
}

// TestWatchdogUsesWallClock feeds the watchdog times that carry a monotonic
// reading (from time.Now) and then a wall-only jump, as a suspended laptop
// produces. Go offers no way to forge a monotonic reading that disagrees with
// the wall clock, so this pins the wall-only path; Round(0) in StartWatchdog
// is what makes the real suspend case work.
func TestWatchdogUsesWallClock(t *testing.T) {
	start := time.Now()
	fake := clock.NewFake(start)
	pauses := make(chan time.Duration, 4)
	stop := StartWatchdog(fake, time.Second, 20*time.Second, func(gap time.Duration) { pauses <- gap })
	defer stop()

	fake.Advance(time.Second)
	fake.Advance(time.Second)
	select {
	case gap := <-pauses:
		t.Fatalf("unexpected pause %v", gap)
	default:
	}
	fake.Advance(time.Minute)
	fake.Advance(time.Second)
	require.GreaterOrEqual(t, <-pauses, time.Minute)
}

func TestIdleExit(t *testing.T) {
	dir := shortDir(t)
	fake := clock.NewFake(time.Unix(1000, 0))
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{ConfigDir: dir, IdleExit: 5 * time.Minute, Clock: fake})
	}()
	waitForSocket(t, dir)

	select {
	case <-done:
		t.Fatal("host exited before the idle period")
	default:
	}
	fake.Advance(5 * time.Minute)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("host did not exit after the idle period")
	}
}

func TestIdleWindowRestartsWhenLastHoldGoes(t *testing.T) {
	dir := shortDir(t)
	fake := clock.NewFake(time.Unix(1000, 0))
	stub := &mutableHolds{holds: []HoldStatus{{ID: "h1"}}}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{ConfigDir: dir, IdleExit: time.Minute, Clock: fake, Services: Services{Holds: stub}})
	}()
	waitForSocket(t, dir)

	fake.Advance(5 * time.Minute) // far past IdleExit, but a hold is open
	requireRunning(t, done)
	stub.set(nil)
	fake.Advance(30 * time.Second) // hold gone half an IdleExit ago
	requireRunning(t, done)
	fake.Advance(30 * time.Second)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("host did not exit IdleExit after the last hold went away")
	}
}

func requireRunning(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case <-done:
		t.Fatal("host exited too early")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRunRefusesSecondHost(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{ConfigDir: dir}) }()
	waitForSocket(t, dir)

	err := Run(context.Background(), Options{ConfigDir: dir})
	require.ErrorIs(t, err, ErrAlreadyRunning)

	cancel()
	require.NoError(t, <-done)
}

func TestStatusServesHoldsAndNothingElse(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	holds := &mutableHolds{holds: []HoldStatus{{ID: "h1", EndpointID: "ep/1"}}}
	go func() { done <- Run(ctx, Options{ConfigDir: dir, Version: "9.9", Services: Services{Holds: holds}}) }()
	waitForSocket(t, dir)

	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "run", "host.sock"))
	}}}
	var status StatusResponse
	resp, err := client.Get("http://host/status")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, decodeJSON(resp.Body, &status))
	_ = resp.Body.Close()
	require.Equal(t, "9.9", status.Version)
	require.Len(t, status.Holds, 1)
	require.Equal(t, "h1", status.Holds[0].ID)

	resp, err = client.Get("http://host/message-requests")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "no message service is mounted")

	cancel()
	require.NoError(t, <-done)
	require.ElementsMatch(t, []string{"host.pid"}, listDir(t, filepath.Join(dir, "run")))
}

func TestOpenHoldRefusedOnceClosing(t *testing.T) {
	g := &gate{}
	h := newHandler("v", time.Unix(0, 0), Services{Holds: &mutableHolds{}}, g)
	require.True(t, g.tryClose(func() bool { return false }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/holds", strings.NewReader(`{"endpointId":"ep/1","kinds":["motion"]}`)))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), CodeHostClosing)
}

func TestGateDoesNotCloseWhileOpening(t *testing.T) {
	g := &gate{}
	require.True(t, g.enter())
	require.False(t, g.tryClose(func() bool { return false }), "an open in flight keeps the host alive")
	g.leave()
	require.False(t, g.tryClose(func() bool { return true }), "an active hold keeps the host alive")
	require.True(t, g.tryClose(func() bool { return false }))
	require.False(t, g.enter(), "no open starts after the gate closed")
}

func TestEndedHoldsDoNotKeepTheHostAlive(t *testing.T) {
	dir := shortDir(t)
	fake := clock.NewFake(time.Unix(1000, 0))
	stub := &mutableHolds{holds: []HoldStatus{{ID: "h1", State: StateEnded}}}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{ConfigDir: dir, IdleExit: time.Minute, Clock: fake, Services: Services{Holds: stub}})
	}()
	waitForSocket(t, dir)
	fake.Advance(time.Minute)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("an ended hold kept the host alive")
	}
}

func TestSignalRunsOnShutdownBeforeTheServerStops(t *testing.T) {
	dir := shortDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	type seen struct {
		err      error
		deadline time.Time
		hasDL    bool
	}
	shutdownCtx := make(chan seen, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{ConfigDir: dir, IdleExit: time.Hour, Clock: clock.NewFake(time.Unix(1000, 0)), OnShutdown: func(c context.Context) {
			// The socket is still being served while the holds end.
			_, err := os.Stat(SocketPath(dir))
			require.NoError(t, err)
			dl, ok := c.Deadline()
			shutdownCtx <- seen{c.Err(), dl, ok}
		}})
	}()
	waitForSocket(t, dir)
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("host did not stop")
	}
	select {
	case c := <-shutdownCtx:
		require.NoError(t, c.err, "the shutdown context ignores the cancelled one")
		require.True(t, c.hasDL)
		require.LessOrEqual(t, time.Until(c.deadline), 60*time.Second)
	default:
		t.Fatal("OnShutdown did not run")
	}
}

func TestIdleExitDoesNotRunOnShutdown(t *testing.T) {
	dir := shortDir(t)
	fake := clock.NewFake(time.Unix(1000, 0))
	ran := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{ConfigDir: dir, IdleExit: time.Minute, Clock: fake, OnShutdown: func(context.Context) { ran <- struct{}{} }})
	}()
	waitForSocket(t, dir)
	fake.Advance(time.Minute)
	require.NoError(t, <-done)
	require.Empty(t, ran)
}
