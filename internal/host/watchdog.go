package host

import (
	"time"

	"github.com/portpowered/portos-cli/internal/clock"
)

// StartWatchdog ticks every interval and calls onPause with the wall-clock gap
// whenever two consecutive ticks are more than threshold apart, which means the
// machine slept or the process was suspended. It returns a stop function.
func StartWatchdog(clk clock.Clock, interval, threshold time.Duration, onPause func(gap time.Duration)) (stop func()) {
	ticker := clk.NewTicker(interval)
	done := make(chan struct{})
	last := clk.Now().Round(0)
	go func() {
		for {
			select {
			case <-done:
				return
			case now := <-ticker.C():
				// Round(0) strips the monotonic reading: on Linux and macOS it stops
				// during suspend, so only the wall clock sees a laptop sleep.
				now = now.Round(0)
				if gap := now.Sub(last); gap > threshold {
					onPause(gap)
				}
				last = now
			}
		}
	}()
	return func() {
		ticker.Stop()
		close(done)
	}
}
