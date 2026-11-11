package hostclient

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/stretchr/testify/require"
)

const (
	testHostEnv     = "PORTOS_TEST_HOST"
	testHostPIDsEnv = "PORTOS_TEST_HOST_PIDS"
)

// TestMain lets the test binary stand in for `port __host`: EnsureRunning
// re-executes it with PORTOS_TEST_HOST=1 and `--config-dir <dir>`. Every such
// host records its pid, and the binary stops them all before it exits.
func TestMain(m *testing.M) {
	if os.Getenv(testHostEnv) == "1" {
		if pids := os.Getenv(testHostPIDsEnv); pids != "" {
			_ = os.WriteFile(filepath.Join(pids, strconv.Itoa(os.Getpid())), nil, 0o600)
		}
		dir := ""
		for i, a := range os.Args {
			if a == "--config-dir" && i+1 < len(os.Args) {
				dir = os.Args[i+1]
			}
		}
		// The idle exit is a safety net so a leaked host never lives long.
		if err := host.Run(context.Background(), host.Options{ConfigDir: dir, IdleExit: time.Minute}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	pids, err := os.MkdirTemp("", "phpids")
	if err != nil {
		os.Exit(1)
	}
	if err := os.Setenv(testHostPIDsEnv, pids); err != nil {
		os.Exit(1)
	}
	code := m.Run()
	stopTestHosts(pids)
	os.Exit(code)
}

// stopTestHosts stops every host the tests spawned. EnsureRunning respawns
// while it waits, so a late spawn can win the lock after its test's cleanup
// killed the first host and live on with no socket until its idle exit; on
// Windows a running host also keeps go test from deleting the test binary.
func stopTestHosts(dir string) {
	time.Sleep(time.Second) // a host spawned just before the tests ended registers itself
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
			_, _ = p.Wait()
		}
	}
	_ = os.RemoveAll(dir)
}

// shortDir returns a short config directory (socket paths must stay under 108
// bytes) and stops any host the test left running.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ph")
	require.NoError(t, err)
	t.Cleanup(func() {
		killHost(dir)
		_ = os.RemoveAll(dir)
	})
	return dir
}

func killHost(dir string) {
	c, err := Dial(context.Background(), dir)
	if err != nil {
		return
	}
	st, err := c.Status(context.Background())
	if err != nil {
		return
	}
	if p, err := os.FindProcess(st.Pid); err == nil {
		_ = p.Kill()
	}
	time.Sleep(100 * time.Millisecond)
}

func TestEnsureRunningStartsHostOnce(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)
	ctx := context.Background()

	first, err := EnsureRunning(ctx, dir)
	require.NoError(t, err)
	second, err := EnsureRunning(ctx, dir)
	require.NoError(t, err)

	a, err := first.Status(ctx)
	require.NoError(t, err)
	b, err := second.Status(ctx)
	require.NoError(t, err)
	require.NotZero(t, a.Pid)
	require.Equal(t, a.Pid, b.Pid)
	require.NotEqual(t, os.Getpid(), a.Pid)
}

func TestEnsureRunningConcurrentStartsOneHost(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)
	ctx := context.Background()

	const n = 8
	pids := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := EnsureRunning(ctx, dir)
			if err != nil {
				errs[i] = err
				return
			}
			st, err := c.Status(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			pids[i] = st.Pid
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, pids[0], pids[i])
	}

	lock := flock.New(filepath.Join(dir, "run", "host.pid"))
	ok, err := lock.TryLock()
	if ok {
		_ = lock.Unlock()
	}
	require.NoError(t, err)
	require.False(t, ok, "host.pid should stay locked while the host runs")
}

func TestEnsureRunningReplacesStaleSocket(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "run"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run", "host.sock"), nil, 0o600))

	c, err := EnsureRunning(context.Background(), dir)
	require.NoError(t, err)
	st, err := c.Status(context.Background())
	require.NoError(t, err)
	require.Empty(t, st.Holds)
}

func TestDialDoesNotSpawn(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)

	_, err := Dial(context.Background(), dir)
	require.ErrorIs(t, err, ErrNotRunning)
	_, statErr := os.Stat(filepath.Join(dir, "run", "host.pid"))
	require.True(t, os.IsNotExist(statErr), "Dial must not start a host")
}

func TestUnservedRoutesReturnTypedError(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)
	ctx := context.Background()
	c, err := EnsureRunning(ctx, dir)
	require.NoError(t, err)

	_, err = c.ListHolds(ctx)
	require.ErrorIs(t, err, ErrRouteNotServed)
	_, err = c.GetHold(ctx, "h1")
	require.ErrorIs(t, err, ErrRouteNotServed)
	_, err = c.OpenHold(ctx, OpenHoldRequest{EndpointID: "ep/1"})
	require.ErrorIs(t, err, ErrRouteNotServed)
	_, err = c.ReleaseHold(ctx, "h1")
	require.ErrorIs(t, err, ErrRouteNotServed)
	_, err = c.Send(ctx, "lease", nil)
	require.ErrorIs(t, err, ErrRouteNotServed)
}

// A host that is shutting down still holds the lock but has dropped its
// socket. The first spawn loses the lock; a later spawn must win once it frees.
func TestEnsureRunningSurvivesShutdownWindow(t *testing.T) {
	t.Setenv(testHostEnv, "1")
	dir := shortDir(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "run"), 0o700))
	lock := flock.New(filepath.Join(dir, "run", "host.pid"))
	ok, err := lock.TryLock()
	require.NoError(t, err)
	require.True(t, ok)
	go func() {
		time.Sleep(700 * time.Millisecond)
		_ = lock.Unlock()
	}()

	c, err := EnsureRunning(context.Background(), dir)
	require.NoError(t, err)
	st, err := c.Status(context.Background())
	require.NoError(t, err)
	require.NotZero(t, st.Pid)
}
