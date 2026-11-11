package host

import "sync"

// gate makes the idle exit and POST /holds agree. The idle check closes the
// gate only when nothing is being opened and no hold is active; after that
// POST /holds is refused, so a hold can never be opened on a host that is
// about to exit.
type gate struct {
	mu       sync.Mutex
	closing  bool
	inflight int
}

// enter registers an open in flight. It reports false once the host is closing.
func (g *gate) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return false
	}
	g.inflight++
	return true
}

func (g *gate) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inflight--
}

// close closes the gate unconditionally: the host is shutting down on a
// signal, so no new hold may open while the existing ones end.
func (g *gate) close() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closing = true
}

// tryClose closes the gate unless an open is in flight or busy reports an
// active hold. busy runs under the gate's lock, so a hold registered by an open
// that has already left is seen.
func (g *gate) tryClose(busy func() bool) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return true
	}
	if g.inflight > 0 || busy() {
		return false
	}
	g.closing = true
	return true
}
