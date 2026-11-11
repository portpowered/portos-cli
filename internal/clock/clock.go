// Package clock abstracts time so that timers and tickers can be driven by tests.
package clock

import "time"

// Clock is the time source used by the session host.
type Clock interface {
	Now() time.Time
	NewTicker(d time.Duration) Ticker
	AfterFunc(d time.Duration, f func()) Timer
}

// Ticker delivers ticks on C until Stop is called.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// Timer is a pending AfterFunc call.
type Timer interface {
	Stop() bool
}

// Real is the wall clock.
type Real struct{}

// Now returns the current wall-clock time.
func (Real) Now() time.Time { return time.Now() }

// NewTicker wraps time.NewTicker.
func (Real) NewTicker(d time.Duration) Ticker { return realTicker{t: time.NewTicker(d)} }

// AfterFunc wraps time.AfterFunc.
func (Real) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
