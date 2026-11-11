//go:build !windows

package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSocketAndRunDirPermissions(t *testing.T) {
	dir := shortDir(t)
	// run/ already exists with loose permissions.
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "run"), 0o755))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, Options{ConfigDir: dir}) }()
	waitForSocket(t, dir)

	sock, err := os.Stat(SocketPath(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), sock.Mode().Perm())
	run, err := os.Stat(filepath.Join(dir, "run"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), run.Mode().Perm())

	cancel()
	require.NoError(t, <-done)
}
