package clock

import (
	"sync"
	"time"
)

// Fake is a manually advanced Clock for tests.
//
// Advance delivers ticks with a blocking send, so a test that calls Advance
// twice knows the receiver finished handling the first tick before the second
// is delivered. A ticker that was advanced past several periods delivers a
// single tick carrying the current time, as a real ticker does after a stall.
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
	timers  []*fakeTimer
}

// NewFake returns a Fake clock starting at start.
func NewFake(start time.Time) *Fake { return &Fake{now: start} }

// Now returns the fake time.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTicker registers a ticker that fires on Advance.
func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{period: d, next: f.now.Add(d), ch: make(chan time.Time), done: make(chan struct{})}
	f.tickers = append(f.tickers, t)
	return t
}

// AfterFunc registers f to run once, on the goroutine calling Advance.
func (f *Fake) AfterFunc(d time.Duration, fn func()) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{owner: f, due: f.now.Add(d), fn: fn}
	f.timers = append(f.timers, t)
	return t
}

// Advance moves the clock forward by d, then fires everything that is due.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now
	var due []*fakeTimer
	var rest []*fakeTimer
	for _, t := range f.timers {
		switch {
		case t.stopped:
		case !t.due.After(now):
			t.stopped = true
			due = append(due, t)
		default:
			rest = append(rest, t)
		}
	}
	f.timers = rest
	var ticking []*fakeTicker
	for _, t := range f.tickers {
		if !t.next.After(now) {
			for !t.next.After(now) {
				t.next = t.next.Add(t.period)
			}
			ticking = append(ticking, t)
		}
	}
	f.mu.Unlock()

	for _, t := range due {
		t.fn()
	}
	for _, t := range ticking {
		select {
		case t.ch <- now:
		case <-t.done:
		}
	}
}

type fakeTicker struct {
	period time.Duration
	next   time.Time
	ch     chan time.Time
	done   chan struct{}
	once   sync.Once
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }
func (t *fakeTicker) Stop()               { t.once.Do(func() { close(t.done) }) }

type fakeTimer struct {
	owner   *Fake
	due     time.Time
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	was := !t.stopped
	t.stopped = true
	return was
}
