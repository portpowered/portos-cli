package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gofrs/flock"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/config"
)

// ErrAlreadyRunning means another host holds run/host.pid for this config dir.
var ErrAlreadyRunning = errors.New("session host already running")

const (
	runDirName      = "run"
	socketFileName  = "host.sock"
	pidFileName     = "host.pid"
	defaultIdleExit = 5 * time.Minute
	shutdownGrace   = 60 * time.Second
	// shutdownEndBound bounds ending every hold when a signal stops the host.
	shutdownEndBound   = 60 * time.Second
	readHeaderTimeout  = 5 * time.Second
	idlePollInterval   = time.Second
	watchdogInterval   = time.Second
	watchdogThreshold  = 20 * time.Second
	runDirMode         = 0o700
	socketMode         = 0o600
	pidFileMode        = 0o600
	networkUnix        = "unix"
	defaultVersionText = "dev"
)

// Options configure Run.
type Options struct {
	ConfigDir string
	IdleExit  time.Duration // default 5m
	Clock     clock.Clock   // default the wall clock
	Services  Services
	Version   string
	// OnPause, when set, is called when the watchdog sees the machine pause.
	OnPause func(gap time.Duration)
	// OnShutdown, when set, is called when the host's context is cancelled
	// (a SIGTERM or SIGINT), before the server shuts down, so every hold can
	// end and the robot docks. Its context ignores the cancellation and is
	// bounded at 60 s. The idle exit does not call it: it has no holds.
	OnShutdown func(ctx context.Context)
}

// maxSocketPathBytes keeps a socket path inside the smallest platform limit
// (104 bytes with the terminator on macOS, 108 on Linux and Windows).
const maxSocketPathBytes = 100

// socketHashBytes is how much of the config dir's hash names a fallback socket.
const socketHashBytes = 8

// SocketPath is where the host listens for the given config dir: run/host.sock
// inside it, or, when that path is too long for a unix socket, a socket named
// by the config dir's hash in the run dir of ~/.portos.
func SocketPath(configDir string) string {
	inside := filepath.Join(configDir, runDirName, socketFileName)
	if len(inside) <= maxSocketPathBytes {
		return inside
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return inside
	}
	if abs, err := filepath.Abs(configDir); err == nil {
		configDir = abs
	}
	sum := sha256.Sum256([]byte(configDir))
	return filepath.Join(home, config.ConfigDirName, runDirName, hex.EncodeToString(sum[:socketHashBytes])+".sock")
}

// Run takes the host lock, listens on run/host.sock and serves until ctx ends
// or the host has had no holds for IdleExit.
func Run(ctx context.Context, opts Options) error {
	if opts.IdleExit <= 0 {
		opts.IdleExit = defaultIdleExit
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if opts.Version == "" {
		opts.Version = defaultVersionText
	}

	runDir := filepath.Join(opts.ConfigDir, runDirName)
	if err := ensureRunDir(runDir); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(runDir, pidFileName))
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock host.pid: %w", err)
	}
	if !locked {
		return ErrAlreadyRunning
	}
	defer func() { _ = lock.Unlock() }()
	// Advisory only: on Windows the locked byte range blocks other handles.
	_ = os.WriteFile(lock.Path(), []byte(strconv.Itoa(os.Getpid())), pidFileMode)

	signalCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g := &gate{}
	stopIdle := watchIdle(ctx, opts, g, cancel)
	defer stopIdle()

	// Holding the lock means any socket file left here is stale.
	sockPath := SocketPath(opts.ConfigDir)
	if err := ensureRunDir(filepath.Dir(sockPath)); err != nil {
		return err
	}
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	listener, err := net.Listen(networkUnix, sockPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", sockPath, err)
	}
	defer func() { _ = os.Remove(sockPath) }()
	if err := os.Chmod(sockPath, socketMode); err != nil {
		_ = listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	if opts.OnPause != nil {
		defer StartWatchdog(opts.Clock, watchdogInterval, watchdogThreshold, opts.OnPause)()
	}

	srv := &http.Server{ReadHeaderTimeout: readHeaderTimeout, Handler: newHandler(opts.Version, opts.Clock.Now(), opts.Services, g)}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	if signalCtx.Err() != nil && opts.OnShutdown != nil {
		g.close()
		endCtx, endCancel := context.WithTimeout(context.WithoutCancel(signalCtx), shutdownEndBound)
		opts.OnShutdown(endCtx)
		endCancel()
	}
	shutdownCtx, stop := context.WithTimeout(context.Background(), shutdownGrace)
	defer stop()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return nil
}

// watchIdle cancels the host once it has had no holds for IdleExit. The idle
// window restarts every time a hold is seen, so it measures time since the
// last hold went away (or since start when there never was one).
func watchIdle(ctx context.Context, opts Options, g *gate, cancel context.CancelFunc) (stop func()) {
	ticker := opts.Clock.NewTicker(idlePollInterval)
	done := make(chan struct{})
	busySince := opts.Clock.Now()
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case now := <-ticker.C():
				if opts.Services.activeHolds() > 0 {
					busySince = now
				} else if now.Sub(busySince) >= opts.IdleExit {
					if g.tryClose(func() bool { return opts.Services.activeHolds() > 0 }) {
						cancel()
						return
					}
					busySince = now // a hold opened in the meantime: measure from now
				}
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
	}
}

// ensureRunDir creates dir readable by its owner only.
func ensureRunDir(dir string) error {
	if err := os.MkdirAll(dir, runDirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Chmod(dir, runDirMode); err != nil {
		return fmt.Errorf("chmod %s: %w", dir, err)
	}
	return nil
}
