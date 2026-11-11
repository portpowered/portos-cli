package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// longDir returns a config directory whose run/host.sock is too long for a unix socket.
func longDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(shortDir(t), strings.Repeat("d", 120))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	return dir
}

// shortHome points the user's home directory at a short temporary directory.
func shortHome(t *testing.T) string {
	t.Helper()
	home := shortDir(t)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestSocketPathStaysInsideAShortConfigDir(t *testing.T) {
	dir := shortDir(t)
	require.Equal(t, filepath.Join(dir, "run", "host.sock"), SocketPath(dir))
}

func TestSocketPathOfALongConfigDirIsShortAndItsOwn(t *testing.T) {
	home := shortHome(t)
	first, second := longDir(t), longDir(t)

	path := SocketPath(first)
	require.Equal(t, filepath.Join(home, ".portos", "run"), filepath.Dir(path))
	require.LessOrEqual(t, len(path), maxSocketPathBytes)
	require.Equal(t, path, SocketPath(first), "the host and its clients must agree on the path")
	require.NotEqual(t, path, SocketPath(second), "two config dirs must not share a host")
}

func TestRunServesALongConfigDir(t *testing.T) {
	shortHome(t)
	dir := longDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{ConfigDir: dir}) }()
	waitForSocket(t, dir)

	cancel()
	require.NoError(t, <-done)
	_, err := os.Stat(SocketPath(dir))
	require.True(t, os.IsNotExist(err), "the socket outlived the host: %v", err)
}
