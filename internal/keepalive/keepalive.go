// Package keepalive decides when and what to send to keep a robot's remote
// control session alive between agent moves.
package keepalive

import "time"

// Mode is how a keepalive is made.
type Mode string

// Keepalive modes.
const (
	// ModePing sends a zero-motion move.
	ModePing Mode = "ping"
	// ModeFidget turns a little one way, then back the other way.
	ModeFidget Mode = "fidget"
)

// Default is the mode used when none is chosen; the S8 spike showed a
// zero-motion move keeps the session alive.
const Default = ModePing

// Interval is how long after the last move started a keepalive is due. The
// robot drops remote control after about 30 s without a move.
const Interval = 20 * time.Second

const (
	moveDurationMillis = 1000
	fidgetOmega        = 0.4
)

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool { return m == ModePing || m == ModeFidget }

// Next returns the keepalive body to send and whether it is due. A fidget mirror
// that is pending is always due. An unknown mode behaves as ping. The body has
// no seqNum; the caller assigns it.
func Next(mode Mode, lastMoveStartedAt, now time.Time, pendingMirror bool) (map[string]any, bool) {
	if mode == ModeFidget && pendingMirror {
		return body(-fidgetOmega), true
	}
	if now.Sub(lastMoveStartedAt) < Interval {
		return nil, false
	}
	if mode == ModeFidget {
		return body(fidgetOmega), true
	}
	return body(0), true
}

func body(omega float64) map[string]any {
	return map[string]any{"velocity": 0.0, "omega": omega, "duration": moveDurationMillis}
}
