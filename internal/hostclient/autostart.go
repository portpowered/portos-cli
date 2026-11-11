package hostclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	pollInterval = 50 * time.Millisecond
	respawnEvery = 500 * time.Millisecond
	startTimeout = 5 * time.Second
)

// hostExecutable names the binary spawned as the host. Tests replace it.
var hostExecutable = os.Executable

// EnsureRunning returns a client for the host of configDir, starting a
// detached host first when none is listening.
func EnsureRunning(ctx context.Context, configDir string) (*Client, error) {
	if c, err := Dial(ctx, configDir); err == nil {
		return c, nil
	}
	if err := spawnHost(configDir); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	lastSpawn := time.Now()
	for {
		c, err := Dial(ctx, configDir)
		if err == nil {
			return c, nil
		}
		// A host that is shutting down still holds the lock but has dropped its
		// socket, so the first spawn can lose. Retry; losers exit harmlessly.
		if time.Since(lastSpawn) >= respawnEvery {
			lastSpawn = time.Now()
			if spawnErr := spawnHost(configDir); spawnErr != nil {
				return nil, spawnErr
			}
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("session host did not start within %s", startTimeout), err)
		case <-ticker.C:
		}
	}
}

// spawnHost starts `<exe> __host --config-dir <dir>` detached from this process
// with stdio on the null device. Concurrent callers may each spawn one; the
// extra hosts lose the host.pid lock and exit.
func spawnHost(configDir string) error {
	exe, err := hostExecutable()
	if err != nil {
		return fmt.Errorf("find own executable: %w", err)
	}
	cmd := exec.Command(exe, "__host", "--config-dir", configDir)
	cmd.Env = hostEnv(os.Environ())
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = devNull.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start session host: %w", err)
	}
	// Reap the child if it exits while we are still running (a host that loses
	// the lock exits at once).
	go func() { _ = cmd.Wait() }()
	return nil
}

// callerOnlyEnv are the variables that belong to the calling command, not to
// the long-lived host: the env token set overrides stored credentials, so the
// host drops all of it and uses the stored refreshable credentials.
var callerOnlyEnv = []string{
	"PORTOS_ACCESS_TOKEN", "PORTOS_REFRESH_TOKEN", "PORTOS_EXPIRES_AT", "PORTOS_TOKEN_TYPE", "PORTOS_LEASE",
}

// hostEnv returns env without the caller's token set and lease.
func hostEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, banned := range callerOnlyEnv {
			// Windows environment names are case-insensitive.
			if strings.EqualFold(name, banned) {
				drop = true
				break
			}
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}
