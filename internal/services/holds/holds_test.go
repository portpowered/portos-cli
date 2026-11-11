package holds_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, s)
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

type fakeSession struct {
	name    string
	log     *callLog
	openErr error
	mu      sync.Mutex
	endings []holds.Ending

	// When set, the call blocks until the gate is closed / signals as it starts.
	openGate, closeGate       chan struct{}
	openEntered, closeEntered chan struct{}
	closeDeadline             time.Time
	closeHasDeadline          bool
}

func signal(c chan struct{}) {
	if c != nil {
		c <- struct{}{}
	}
}

func (f *fakeSession) Open(_ context.Context, _, _ string) error {
	f.log.add(f.name + ":open")
	signal(f.openEntered)
	if f.openGate != nil {
		<-f.openGate
	}
	return f.openErr
}

func (f *fakeSession) Close(ctx context.Context, _ string, ending holds.Ending) error {
	f.log.add(f.name + ":close")
	f.mu.Lock()
	f.endings = append(f.endings, ending)
	f.closeDeadline, f.closeHasDeadline = ctx.Deadline()
	f.mu.Unlock()
	signal(f.closeEntered)
	if f.closeGate != nil {
		<-f.closeGate
	}
	return nil
}

func (f *fakeSession) closes() []holds.Ending {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]holds.Ending(nil), f.endings...)
}

// recordingClock keeps every AfterFunc callback so a test can fire a stale one.
type recordingClock struct {
	*clock.Fake
	mu  sync.Mutex
	fns []func()
}

func (r *recordingClock) AfterFunc(d time.Duration, fn func()) clock.Timer {
	r.mu.Lock()
	r.fns = append(r.fns, fn)
	r.mu.Unlock()
	return r.Fake.AfterFunc(d, fn)
}

type fixture struct {
	rec    *recordingClock
	clock  *clock.Fake
	svc    holds.Service
	log    *callLog
	motion *fakeSession
	camera *fakeSession
}

func newFixture(login holds.LoginCheck) *fixture {
	log := &callLog{}
	f := &fixture{
		clock:  clock.NewFake(time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)),
		log:    log,
		motion: &fakeSession{name: "motion", log: log},
		camera: &fakeSession{name: "camera", log: log},
	}
	f.rec = &recordingClock{Fake: f.clock}
	f.svc = holds.New(f.rec, map[holds.Kind]holds.Session{
		holds.KindMotion: f.motion,
		holds.KindCamera: f.camera,
	}, login)
	return f
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var he *holds.Error
	if !errors.As(err, &he) || he.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

var both = []holds.Kind{holds.KindMotion, holds.KindCamera}

func TestOpenIssuesLeaseAndAuthorizes(t *testing.T) {
	f := newFixture(nil)
	h, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if h.Lease == "" || h.State != holds.StateLive || h.ID == "" {
		t.Fatalf("hold = %+v", h)
	}
	other, _ := newFixture(nil).svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	if other.Lease == h.Lease {
		t.Fatal("leases must differ")
	}
	f.clock.Advance(time.Minute)
	got, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
	if err != nil {
		t.Fatal(err)
	}
	if got.Lease != "" {
		t.Fatal("lease must be returned only by Open")
	}
	if !got.LastCommandAt.Equal(f.clock.Now()) {
		t.Fatalf("LastCommandAt = %v", got.LastCommandAt)
	}
	if g, _ := f.svc.Get(h.ID); g.Lease != "" {
		t.Fatal("Get leaked the lease")
	}
}

func TestAuthorizeWrongLease(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	_, err := f.svc.Authorize("nope", "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
	wantCode(t, err, holds.CodeHoldOwnedByOther)
	_, err = f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAQ", holds.KindMotion)
	wantCode(t, err, holds.CodeHoldOwnedByOther)
}

func TestAuthorizeMissingLease(t *testing.T) {
	f := newFixture(nil)
	_, _ = f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	_, err := f.svc.Authorize("", "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
	wantCode(t, err, holds.CodeHoldLeaseRequired)
}

func TestAuthorizeKindNotHeld(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", []holds.Kind{holds.KindCamera}, 0)
	_, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
	wantCode(t, err, holds.CodeNotServedByHold)
}

func TestAuthorizeEndedHold(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	if err := f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, ""); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
	wantCode(t, err, holds.CodeHoldEnded)
	got, err := f.svc.Get(h.ID)
	if err != nil || got.State != holds.StateEnded || got.Ending == nil || got.Ending.Reason != holds.ReasonReleased {
		t.Fatalf("got %+v, %v", got, err)
	}
	if len(f.svc.List()) != 1 {
		t.Fatal("ended hold must stay listed")
	}
}

func TestSecondHoldSameEndpoint(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	_, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	wantCode(t, err, holds.CodeHoldOwnedByOther)
	if _, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAQ", both, 0); err != nil {
		t.Fatal(err)
	}
	_ = f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, "")
	if _, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0); err != nil {
		t.Fatalf("endpoint should be free after release: %v", err)
	}
}

func TestDeadManEndsHold(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 10*time.Minute)
	f.clock.Advance(5 * time.Minute)
	if _, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(4*time.Minute + 59*time.Second) // 9:59, only 4:59 since the Authorize
	if g, _ := f.svc.Get(h.ID); g.State != holds.StateLive {
		t.Fatalf("state at 9:59 = %s", g.State)
	}
	f.clock.Advance(5*time.Minute + time.Second) // 15:00
	g, _ := f.svc.Get(h.ID)
	if g.State != holds.StateEnded || g.Ending == nil || g.Ending.Reason != holds.ReasonDeadMan {
		t.Fatalf("hold at 15:00 = %+v", g)
	}
}

func TestDeadManDefaultsAndPerHold(t *testing.T) {
	f := newFixture(nil)
	a, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	b, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAQ", both, time.Minute)
	if a.DeadMan != 10*time.Minute || b.DeadMan != time.Minute {
		t.Fatalf("dead-man = %v, %v", a.DeadMan, b.DeadMan)
	}
	f.clock.Advance(2 * time.Minute)
	if g, _ := f.svc.Get(b.ID); g.State != holds.StateEnded {
		t.Fatal("short hold should have ended")
	}
	if g, _ := f.svc.Get(a.ID); g.State != holds.StateLive {
		t.Fatal("default hold should be live")
	}
}

func TestReleaseClosesMotionThenCamera(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", []holds.Kind{holds.KindCamera, holds.KindMotion}, 0)
	if err := f.svc.Release(context.Background(), h.ID, holds.ReasonBatteryLow, "12%"); err != nil {
		t.Fatal(err)
	}
	calls := f.log.get()
	n := len(calls)
	if n < 2 || calls[n-2] != "motion:close" || calls[n-1] != "camera:close" {
		t.Fatalf("calls = %v", calls)
	}
	if e := f.motion.closes()[0]; e.Reason != holds.ReasonBatteryLow || e.Detail != "12%" {
		t.Fatalf("ending = %+v", e)
	}
}

func TestReleaseIdempotent(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	for i := 0; i < 3; i++ {
		if err := f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.motion.closes()) != 1 || len(f.camera.closes()) != 1 {
		t.Fatalf("closes = %d, %d", len(f.motion.closes()), len(f.camera.closes()))
	}
}

func TestOpenRequiresRefreshableLogin(t *testing.T) {
	login := func(context.Context) error {
		return &holds.Error{Code: holds.CodeHoldRequiresRefreshableLogin, Message: "log in"}
	}
	f := newFixture(login)
	_, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	wantCode(t, err, holds.CodeHoldRequiresRefreshableLogin)
	if len(f.svc.List()) != 0 || len(f.log.get()) != 0 {
		t.Fatal("nothing should be opened")
	}
}

func TestSessionOpenFailureEndsHold(t *testing.T) {
	f := newFixture(nil)
	f.camera.openErr = fmt.Errorf("camera boom")
	_, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	if err == nil {
		t.Fatal("want error")
	}
	list := f.svc.List()
	if len(list) != 1 || list[0].State != holds.StateEnded || list[0].Ending.Reason != holds.ReasonEndpointError {
		t.Fatalf("list = %+v", list)
	}
	if !strings.Contains(list[0].Ending.Detail, "camera boom") {
		t.Fatalf("detail = %q", list[0].Ending.Detail)
	}
	if len(f.motion.closes()) != 1 {
		t.Fatal("opened motion session was not closed")
	}
	f.camera.openErr = nil
	if _, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0); err != nil {
		t.Fatalf("endpoint should be free after a failed open: %v", err)
	}
}

func TestEndAllEndsEveryLiveHold(t *testing.T) {
	f := newFixture(nil)
	a, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	b, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAQ", both, 0)
	f.svc.EndAll(context.Background(), holds.ReasonHostPaused)
	for _, id := range []string{a.ID, b.ID} {
		g, _ := f.svc.Get(id)
		if g.State != holds.StateEnded || g.Ending.Reason != holds.ReasonHostPaused {
			t.Fatalf("hold %s = %+v", id, g)
		}
	}
}

func TestConcurrentAuthorizeAndRelease(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion)
				var he *holds.Error
				if err != nil && (!errors.As(err, &he) || he.Code != holds.CodeHoldEnded) {
					t.Errorf("unexpected error %v", err)
				}
				f.svc.List()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, "")
		}()
	}
	wg.Wait()
	if len(f.motion.closes()) != 1 || len(f.camera.closes()) != 1 {
		t.Fatalf("closes = %d, %d", len(f.motion.closes()), len(f.camera.closes()))
	}
}

func TestStaleDeadManCallbackLosesToAuthorize(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, time.Minute)
	f.rec.mu.Lock()
	stale := f.rec.fns[0]
	f.rec.mu.Unlock()
	if _, err := f.svc.Authorize(h.Lease, "ep/AAAAAAAAAAAAAAAAAAAAAA", holds.KindMotion); err != nil {
		t.Fatal(err)
	}
	stale() // the first timer fires after Authorize re-armed it
	if g, _ := f.svc.Get(h.ID); g.State != holds.StateLive {
		t.Fatalf("state = %s, want live", g.State)
	}
	if len(f.motion.closes()) != 0 {
		t.Fatal("stale callback closed a session")
	}
}

func TestDeadManCloseIsBounded(t *testing.T) {
	f := newFixture(nil)
	_, _ = f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, time.Minute)
	f.clock.Advance(2 * time.Minute)
	f.motion.mu.Lock()
	defer f.motion.mu.Unlock()
	if !f.motion.closeHasDeadline || time.Until(f.motion.closeDeadline) > holds.DeadManCloseTimeout {
		t.Fatalf("dead-man close deadline = %v (set %v)", f.motion.closeDeadline, f.motion.closeHasDeadline)
	}
}

func TestReleaseDuringOpeningLeaksNoSession(t *testing.T) {
	f := newFixture(nil)
	f.motion.openGate = make(chan struct{})
	f.motion.openEntered = make(chan struct{})
	errc := make(chan error, 1)
	go func() {
		_, err := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
		errc <- err
	}()
	<-f.motion.openEntered
	id := f.svc.List()[0].ID
	if err := f.svc.Release(context.Background(), id, holds.ReasonReleased, ""); err != nil {
		t.Fatal(err)
	}
	close(f.motion.openGate)
	wantCode(t, <-errc, holds.CodeHoldEnded)
	if len(f.motion.closes()) != 1 {
		t.Fatalf("motion closes = %d, want 1 (session opened after the hold ended)", len(f.motion.closes()))
	}
	for _, c := range f.log.get() {
		if c == "camera:open" {
			t.Fatal("camera opened after the hold left opening")
		}
	}
}

func TestSecondReleaseWaitsForCloses(t *testing.T) {
	f := newFixture(nil)
	h, _ := f.svc.Open(context.Background(), "ep/AAAAAAAAAAAAAAAAAAAAAA", both, 0)
	f.motion.closeGate = make(chan struct{})
	f.motion.closeEntered = make(chan struct{})
	first := make(chan error, 1)
	go func() { first <- f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, "") }()
	<-f.motion.closeEntered

	second := make(chan error, 1)
	go func() { second <- f.svc.Release(context.Background(), h.ID, holds.ReasonReleased, "") }()
	select {
	case err := <-second:
		t.Fatalf("second Release returned %v before the closes finished", err)
	case <-time.After(50 * time.Millisecond):
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.svc.Release(ctx, h.ID, holds.ReasonReleased, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Release = %v", err)
	}

	close(f.motion.closeGate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if g, _ := f.svc.Get(h.ID); g.State != holds.StateEnded {
		t.Fatalf("state = %s", g.State)
	}
}

func TestSecondHoldUnderAnotherSpellingIsRefused(t *testing.T) {
	const compact = "me/ep/I-r_4j-cTIaVkx7o8yKJHg"
	const full = "port1/principals/p1/endpoints/23eaffe2-3f9c-4c86-9593-1ee8f322891e"
	f := newFixture(nil)
	h, err := f.svc.Open(context.Background(), compact, both[:1], 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Open(context.Background(), full, both[:1], 0)
	wantCode(t, err, holds.CodeHoldOwnedByOther)
	// The lease works under either spelling, and the hold keeps the id it was opened with.
	got, err := f.svc.Authorize(h.Lease, full, holds.KindMotion)
	if err != nil {
		t.Fatal(err)
	}
	if got.EndpointID != compact {
		t.Fatalf("EndpointID = %q, want the original spelling", got.EndpointID)
	}
}

func TestOpenRefusesEndpointIDWithoutCanonicalKey(t *testing.T) {
	f := newFixture(nil)
	_, err := f.svc.Open(context.Background(), "foo/bar", both, 0)
	var holdErr *holds.Error
	if !errors.As(err, &holdErr) || holdErr.Code != holds.CodeInvalidRequest {
		t.Fatalf("error = %v, want code %s", err, holds.CodeInvalidRequest)
	}
	if holdErr.Message != "use the endpoint's full id or its ep/... form" {
		t.Fatalf("message = %q", holdErr.Message)
	}
	if got := f.svc.List(); len(got) != 0 {
		t.Fatalf("list = %v, want none", got)
	}
}
