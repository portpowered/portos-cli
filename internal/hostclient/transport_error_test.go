package hostclient

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/host"
	"github.com/stretchr/testify/require"
)

// listenSilent serves a unix socket for dir that accepts connections and then
// closes them without replying (reset) or holds them open (hang).
func listenSilent(t *testing.T, dir string, hang bool) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir+"/run", 0o700))
	l, err := net.Listen("unix", host.SocketPath(dir))
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			if hang {
				t.Cleanup(func() { _ = conn.Close() })
				continue
			}
			_ = conn.Close()
		}
	}()
}

func TestResetAfterConnectIsNotNotRunning(t *testing.T) {
	dir := shortDir(t)
	listenSilent(t, dir, false)
	c, err := Dial(t.Context(), dir)
	require.NoError(t, err)
	_, err = c.ListHolds(t.Context())
	require.ErrorIs(t, err, ErrTransport)
	require.NotErrorIs(t, err, ErrNotRunning)
}

func TestHangAfterConnectIsNotNotRunning(t *testing.T) {
	dir := shortDir(t)
	listenSilent(t, dir, true)
	c, err := Dial(t.Context(), dir)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = c.ListHolds(ctx)
	require.ErrorIs(t, err, ErrTransport)
	require.NotErrorIs(t, err, ErrNotRunning)
}

func TestNoSocketIsNotRunning(t *testing.T) {
	dir := shortDir(t)
	_, err := Dial(t.Context(), dir)
	require.ErrorIs(t, err, ErrNotRunning)
	c := newClient(dir, host.SocketPath(dir))
	_, err = c.ListHolds(t.Context())
	require.ErrorIs(t, err, ErrNotRunning)
}
