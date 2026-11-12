package camera

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

type fakeStream struct {
	id     int
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	closed bool
	frame  []byte
	err    error
	play   error
	played [][]byte
	record error
	// shared streams report an expiry and why they ended.
	expires time.Time
	ended   string
	steps   []map[string]any
}

func (f *fakeStream) StepPTZ(_ context.Context, body map[string]any) (*api.MessageRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, body)
	return &api.MessageRequest{Id: fmt.Sprintf("step-%d", f.id), Status: api.SUCCEEDED}, nil
}
func (f *fakeStream) ExpiresAt() time.Time { return f.expires }
func (f *fakeStream) EndReason() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

func (f *fakeStream) Latest(context.Context, time.Duration) ([]byte, time.Time, error) {
	return f.frame, time.Unix(int64(f.id), 0), f.err
}
func (f *fakeStream) Play(_ context.Context, ogg []byte) (time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.play != nil {
		return 0, f.play
	}
	f.played = append(f.played, ogg)
	return time.Second, nil
}
func (f *fakeStream) Record(_ context.Context, d time.Duration) (Clip, error) {
	if f.record != nil {
		return Clip{}, f.record
	}
	return Clip{Path: fmt.Sprintf("clip-%d-%s", f.id, d), Codec: "H264", FrameRate: 15}, nil
}
func (f *fakeStream) Done() <-chan struct{} { return f.done }
func (f *fakeStream) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	f.end()
	return nil
}
func (f *fakeStream) end() { f.once.Do(func() { close(f.done) }) }
func (f *fakeStream) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

type fakeOpener struct {
	mu        sync.Mutex
	streams   []*fakeStream
	lifetimes []time.Duration
	fail      error
	// sharedFor is how long a shared stream's session lasts; zero means the lifetime asked for.
	sharedFor time.Duration
	clock     clock.Clock
	shared    int
}

func (o *fakeOpener) Open(_ context.Context, _ string, lifetime time.Duration) (Stream, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.fail != nil {
		return nil, o.fail
	}
	s := &fakeStream{id: len(o.streams) + 1, done: make(chan struct{}), frame: []byte{0xff, 0xd8, byte(len(o.streams) + 1)}}
	o.streams = append(o.streams, s)
	o.lifetimes = append(o.lifetimes, lifetime)
	return s, nil
}

// OpenShared opens a stream whose session ends sharedFor after the open, or a full lifetime
// after it when sharedFor is zero.
func (o *fakeOpener) OpenShared(ctx context.Context, endpointID string, lifetime time.Duration) (Stream, error) {
	stream, err := o.Open(ctx, endpointID, lifetime)
	if err != nil {
		return nil, err
	}
	fake := stream.(*fakeStream)
	span := o.sharedFor
	if span == 0 {
		span = lifetime
	}
	fake.expires = o.clock.Now().Add(span)
	o.shared++
	return fake, nil
}

func (o *fakeOpener) stream(i int) *fakeStream {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streams[i]
}

type lostCall struct {
	holdID string
	reason holds.Reason
}

func newTestService(t *testing.T) (*fakeOpener, *clock.Fake, Service, chan lostCall) {
	t.Helper()
	fake := clock.NewFake(time.Unix(1000, 0))
	opener := &fakeOpener{clock: fake}
	lost := make(chan lostCall, 4)
	svc := New(opener, fake, func(holdID string, reason holds.Reason, _ string) { lost <- lostCall{holdID, reason} })
	return opener, fake, svc, lost
}

func TestSnapshotServesTheHeldStream(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	if opener.lifetimes[0] != Lifetime {
		t.Fatalf("asked for %s, want %s", opener.lifetimes[0], Lifetime)
	}
	frame, err := svc.Snapshot(t.Context(), "hold-1")
	if err != nil || frame.JPEG[2] != 1 {
		t.Fatalf("Snapshot = %v, %v", frame.JPEG, err)
	}
	status, ok := svc.Status("hold-1")
	if !ok || !status.Live || status.LastFrame == nil {
		t.Fatalf("status = %+v, %v", status, ok)
	}
}

func TestRenewalSwapsStreamsBeforeTheSessionEnds(t *testing.T) {
	opener, fake, svc, lost := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	fake.Advance(Lifetime - renewBefore)
	if len(opener.streams) != 2 {
		t.Fatalf("opened %d streams, want a renewal", len(opener.streams))
	}
	if !opener.stream(0).isClosed() {
		t.Fatal("the old stream stayed open after renewal")
	}
	frame, err := svc.Snapshot(t.Context(), "hold-1")
	if err != nil || frame.JPEG[2] != 2 {
		t.Fatalf("Snapshot after renewal = %v, %v", frame.JPEG, err)
	}
	status, _ := svc.Status("hold-1")
	if status.Renewals != 1 || !status.Live {
		t.Fatalf("status = %+v", status)
	}
	select {
	case call := <-lost:
		t.Fatalf("renewal reported a lost camera: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAStreamClosedByTheFarSideIsPreempted(t *testing.T) {
	opener, _, svc, lost := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	opener.stream(0).end()
	select {
	case call := <-lost:
		if call.holdID != "hold-1" || call.reason != holds.ReasonPreempted {
			t.Fatalf("lost = %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("a preempted stream was not reported")
	}
	_, err := svc.Snapshot(t.Context(), "hold-1")
	if !IsUnavailable(err) {
		t.Fatalf("Snapshot after preemption = %v, want camera_unavailable", err)
	}
}

func TestAFailedRenewalLosesTheCamera(t *testing.T) {
	opener, fake, svc, lost := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	opener.mu.Lock()
	opener.fail = errors.New("camera refused")
	opener.mu.Unlock()
	fake.Advance(Lifetime - renewBefore)
	select {
	case call := <-lost:
		if call.reason != holds.ReasonEndpointError {
			t.Fatalf("lost = %+v", call)
		}
	case <-time.After(time.Second):
		t.Fatal("a failed renewal was not reported")
	}
	if !opener.stream(0).isClosed() {
		t.Fatal("the expiring stream stayed open")
	}
}

func TestCloseIsNotAPreemption(t *testing.T) {
	opener, fake, svc, lost := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(t.Context(), "hold-1", holds.Ending{Reason: holds.ReasonReleased}); err != nil {
		t.Fatal(err)
	}
	if !opener.stream(0).isClosed() {
		t.Fatal("Close left the stream open")
	}
	fake.Advance(Lifetime)
	if len(opener.streams) != 1 {
		t.Fatal("a closed camera renewed")
	}
	select {
	case call := <-lost:
		t.Fatalf("Close reported a lost camera: %+v", call)
	case <-time.After(50 * time.Millisecond):
	}
	if _, ok := svc.Status("hold-1"); ok {
		t.Fatal("a closed camera still has a status")
	}
}

func TestAFrameErrorIsCameraUnavailable(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	opener.stream(0).err = errors.New("no frame yet")
	if _, err := svc.Snapshot(t.Context(), "hold-1"); !IsUnavailable(err) {
		t.Fatalf("Snapshot = %v, want camera_unavailable", err)
	}
	if _, err := svc.Snapshot(t.Context(), "hold-2"); !IsUnavailable(err) {
		t.Fatalf("Snapshot of an unknown hold = %v, want camera_unavailable", err)
	}
}

func TestPlayAudioUsesTheHeldStream(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	length, err := svc.PlayAudio(t.Context(), "hold-1", []byte("OggS"))
	if err != nil || length != time.Second {
		t.Fatalf("PlayAudio = %s, %v", length, err)
	}
	if got := opener.stream(0).played; len(got) != 1 {
		t.Fatalf("the stream played %d clips, want 1", len(got))
	}
}

func TestPlayAudioRefusals(t *testing.T) {
	cases := []struct {
		name string
		play error
		code string
	}{
		{"busy", ErrTalkbackBusy, holds.CodeTalkbackBusy},
		{"invalid clip", fmt.Errorf("%w: it holds no audio", ErrClipInvalid), holds.CodeInvalidRequest},
		{"broken track", errors.New("write talkback packet: closed"), holds.CodeCameraUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opener, _, svc, _ := newTestService(t)
			if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
				t.Fatal(err)
			}
			opener.stream(0).play = tc.play
			_, err := svc.PlayAudio(t.Context(), "hold-1", []byte("OggS"))
			var holdErr *holds.Error
			if !errors.As(err, &holdErr) || holdErr.Code != tc.code {
				t.Fatalf("PlayAudio = %v, want %s", err, tc.code)
			}
		})
	}
	_, _, svc, _ := newTestService(t)
	if _, err := svc.PlayAudio(t.Context(), "hold-9", nil); !IsUnavailable(err) {
		t.Fatalf("PlayAudio with no camera = %v, want camera_unavailable", err)
	}
}

func TestRecordUsesTheHeldStream(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	clip, err := svc.Record(t.Context(), "hold-1", 3*time.Second)
	if err != nil || clip.Path != "clip-1-3s" || clip.FrameRate != 15 {
		t.Fatalf("Record = %+v, %v", clip, err)
	}
	opener.stream(0).record = errors.New("the held camera stopped during the recording")
	if _, err := svc.Record(t.Context(), "hold-1", time.Second); !IsUnavailable(err) {
		t.Fatalf("Record on a broken stream = %v, want camera_unavailable", err)
	}
	if _, err := svc.Record(t.Context(), "hold-9", time.Second); !IsUnavailable(err) {
		t.Fatalf("Record with no camera = %v, want camera_unavailable", err)
	}
}

func openSharedHold(t *testing.T, opener *fakeOpener, svc Service) {
	t.Helper()
	if err := svc.Open(holds.WithShared(t.Context()), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	if opener.shared != 1 {
		t.Fatalf("opened %d shared streams, want 1", opener.shared)
	}
}

func TestSharedHoldReportsItsExpiryAndRenewsWhileTheSessionOutlastsTheRenewal(t *testing.T) {
	opener, fake, svc, lost := newTestService(t)
	openSharedHold(t, opener, svc)
	status, _ := svc.Status("hold-1")
	if !status.Shared || status.ExpiresAt == nil || !status.ExpiresAt.Equal(fake.Now().Add(Lifetime)) {
		t.Fatalf("status = %+v, want a shared camera that expires a lifetime from now", status)
	}
	fake.Advance(Lifetime - renewBefore)
	if opener.shared != 2 || !opener.stream(0).isClosed() {
		t.Fatalf("shared opens = %d, old closed = %v: want the renewal to attach the new subscriber and drop the old", opener.shared, opener.stream(0).isClosed())
	}
	select {
	case call := <-lost:
		t.Fatalf("renewal reported a lost camera: %+v", call)
	default:
	}
}

func TestSharedHoldEndsWithSessionExpiredInsteadOfRenewingPastTheSessionEnd(t *testing.T) {
	opener, fake, svc, lost := newTestService(t)
	opener.sharedFor = 10 * time.Minute // the shared upstream has ten minutes left
	openSharedHold(t, opener, svc)
	status, _ := svc.Status("hold-1")
	if !status.RenewsAt.IsZero() {
		t.Fatalf("a session at the upstream's end has nothing to renew into, RenewsAt = %v", status.RenewsAt)
	}
	fake.Advance(9 * time.Minute)
	if opener.shared != 1 {
		t.Fatalf("opened %d shared streams before the expiry, want no renewal", opener.shared)
	}
	fake.Advance(time.Minute)
	select {
	case call := <-lost:
		if call.reason != holds.ReasonSessionExpired {
			t.Fatalf("lost = %+v, want %s", call, holds.ReasonSessionExpired)
		}
	case <-time.After(time.Second):
		t.Fatal("the hold did not end at the session's expiry")
	}
	if opener.shared != 1 || !opener.stream(0).isClosed() {
		t.Fatalf("shared opens = %d, closed = %v: want the old stream closed and no replacement", opener.shared, opener.stream(0).isClosed())
	}
	if _, err := svc.Snapshot(t.Context(), "hold-1"); !IsUnavailable(err) {
		t.Fatalf("Snapshot after the expiry = %v, want camera_unavailable", err)
	}
	if status, _ = svc.Status("hold-1"); status.Live || status.LostReason != string(holds.ReasonSessionExpired) {
		t.Fatalf("status = %+v", status)
	}
}

func TestSharedStreamEndedByItsSessionHoldIsReportedWithThatReason(t *testing.T) {
	for _, tc := range []struct {
		ended string
		want  holds.Reason
	}{
		{string(api.EXPIRED), holds.ReasonSessionExpired},
		{string(api.SESSIONCLOSED), holds.ReasonSessionClosed},
		{string(api.DETACHED), holds.ReasonPreempted},
	} {
		opener, _, svc, lost := newTestService(t)
		openSharedHold(t, opener, svc)
		stream := opener.stream(0)
		stream.mu.Lock()
		stream.ended = tc.ended
		stream.mu.Unlock()
		stream.end()
		select {
		case call := <-lost:
			if call.reason != tc.want {
				t.Fatalf("%s: lost = %+v, want %s", tc.ended, call, tc.want)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: the hold was not told its camera stopped", tc.ended)
		}
	}
}

func TestSharedHoldRefusesPlayAudioButStillSteps(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	openSharedHold(t, opener, svc)
	_, err := svc.PlayAudio(t.Context(), "hold-1", []byte("OggS"))
	var holdErr *holds.Error
	if !errors.As(err, &holdErr) || holdErr.Code != holds.CodeSharedHoldNoTalkback {
		t.Fatalf("PlayAudio under a shared hold = %v, want %s", err, holds.CodeSharedHoldNoTalkback)
	}
	if len(opener.stream(0).played) != 0 {
		t.Fatal("a clip reached a shared stream")
	}
	reply, err := svc.StepPTZ(t.Context(), "hold-1", map[string]any{"direction": "LEFT"})
	if err != nil || reply.Id != "step-1" {
		t.Fatalf("StepPTZ = %+v, %v", reply, err)
	}
	if steps := opener.stream(0).steps; len(steps) != 1 || steps[0]["direction"] != "LEFT" {
		t.Fatalf("steps = %v", steps)
	}
}

func TestSinglePeerHoldStillPlaysAudioAndOpensWithoutTheSharedMode(t *testing.T) {
	opener, _, svc, _ := newTestService(t)
	if err := svc.Open(t.Context(), "hold-1", "ep"); err != nil {
		t.Fatal(err)
	}
	if opener.shared != 0 {
		t.Fatal("a plain camera hold attached as a shared subscriber")
	}
	if _, err := svc.PlayAudio(t.Context(), "hold-1", []byte("OggS")); err != nil {
		t.Fatalf("PlayAudio = %v", err)
	}
}
