package commands

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

// sharedCamera is the in-process pion camera answering as a Ring shared session: its answer carries
// sessionExpiresAt, and its session hold is scripted by holdFn (by default it stays open until the
// caller gives up, like the real hold while the subscriber is attached).
type sharedCamera struct {
	*pionCamera
	expires    time.Time
	omitExpiry bool
	holdFn     func(ctx context.Context, call int) (*api.MessageRequestHoldEnd, error)

	mu            sync.Mutex
	requests      []*api.CreateMessageRequest
	offerHasAudio bool
	holdIDs       []string
	holdCalls     atomic.Int32
}

func (c *sharedCamera) Send(ctx context.Context, request *api.CreateMessageRequest, keys ...string) (*api.MessageRequest, error) {
	c.mu.Lock()
	c.requests = append(c.requests, request)
	c.mu.Unlock()
	reply, err := c.pionCamera.Send(ctx, request, keys...)
	if err != nil || request.Message.Header.Name != rtcSessionInitiate {
		return reply, err
	}
	offer := request.Message.Body["offer"].(map[string]interface{})["value"].(string)
	c.mu.Lock()
	c.offerHasAudio = strings.Contains(offer, "m=audio")
	c.mu.Unlock()
	if !c.omitExpiry {
		reply.Results[0].Message.Body["sessionExpiresAt"] = c.expires.UTC().Format(time.RFC3339)
	}
	return reply, nil
}

func (c *sharedCamera) Hold(ctx context.Context, id string) (*api.MessageRequestHoldEnd, error) {
	call := int(c.holdCalls.Add(1))
	c.mu.Lock()
	c.holdIDs = append(c.holdIDs, id)
	c.mu.Unlock()
	if c.holdFn != nil {
		return c.holdFn(ctx, call)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *sharedCamera) sent(name string) []*api.CreateMessageRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*api.CreateMessageRequest
	for _, request := range c.requests {
		if request.Message.Header.Name == name {
			out = append(out, request)
		}
	}
	return out
}

type sharedClient struct {
	apiclient.Client
	camera *sharedCamera
}

func (c sharedClient) Messages() apiclient.MessagesClient { return c.camera }
func (c sharedClient) Relay() apiclient.RelayClient       { return cameraClient{}.Relay() }

func holdSharedCamera(t *testing.T, camera *sharedCamera, shared bool) (*HeldCamera, error) {
	t.Helper()
	camera.pionCamera = &pionCamera{t: t, src: syntheticSource(6, 25)}
	if camera.expires.IsZero() {
		camera.expires = time.Now().Add(time.Hour)
	}
	recorder := NewRecorder(sharedClient{camera: camera}, logging.NewLoggerRef())
	recorder.iceServers = nil
	held, err := recorder.HoldCamera(t.Context(), HeldCameraParams{
		EndpointID: "camera", SessionDomain: "cli", SessionID: "held", Lifetime: 30 * time.Minute, Shared: shared,
	})
	if err == nil {
		t.Cleanup(func() { _ = held.Close(context.Background()) })
	}
	return held, err
}

// fastHold shortens the session hold's reopen timing for the test.
func fastHold(t *testing.T, window time.Duration) {
	t.Helper()
	oldWindow, oldDelay := holdReopenWindow, holdRetryDelay
	holdReopenWindow, holdRetryDelay = window, 5*time.Millisecond
	t.Cleanup(func() { holdReopenWindow, holdRetryDelay = oldWindow, oldDelay })
}

func waitDone(t *testing.T, held *HeldCamera) {
	t.Helper()
	select {
	case <-held.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the held camera did not stop")
	}
}

func TestSharedHeldCameraAttachesReceiveOnlyWithNoTalkbackAndReadsItsExpiry(t *testing.T) {
	camera := &sharedCamera{expires: time.Now().Add(55 * time.Minute).Truncate(time.Second)}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatalf("HoldCamera: %v", err)
	}
	initiate := camera.sent(rtcSessionInitiate)
	if len(initiate) != 1 {
		t.Fatalf("sent %d offers, want 1", len(initiate))
	}
	body := initiate[0].Message.Body
	if body["sessionMode"] != "SHARED" || body["sessionDuration"] != 1800 {
		t.Fatalf("offer body = %v, want sessionMode SHARED for 1800 s", body)
	}
	if camera.offerHasAudio {
		t.Fatal("a shared subscriber offered an audio track; the call owns the speaker")
	}
	if !held.Shared() || !held.ExpiresAt().Equal(camera.expires) {
		t.Fatalf("shared = %v, expires = %v, want %v", held.Shared(), held.ExpiresAt(), camera.expires)
	}
	if _, err := held.Play(t.Context(), []byte("OggS")); err == nil {
		t.Fatal("a shared camera played a clip")
	}
}

func TestSinglePeerHeldCameraStillOffersTalkbackAndNoSharedMode(t *testing.T) {
	camera := &sharedCamera{}
	held, err := holdSharedCamera(t, camera, false)
	if err != nil {
		t.Fatalf("HoldCamera: %v", err)
	}
	body := camera.sent(rtcSessionInitiate)[0].Message.Body
	if _, present := body["sessionMode"]; present {
		t.Fatalf("a single-peer offer carried sessionMode: %v", body)
	}
	if !camera.offerHasAudio || held.Shared() || !held.ExpiresAt().IsZero() {
		t.Fatalf("offerHasAudio = %v, shared = %v, expires = %v", camera.offerHasAudio, held.Shared(), held.ExpiresAt())
	}
	if camera.holdCalls.Load() != 0 {
		t.Fatal("a single-peer camera opened a session hold")
	}
}

func TestSharedHeldCameraNeedsTheAnswersExpiry(t *testing.T) {
	if _, err := holdSharedCamera(t, &sharedCamera{omitExpiry: true}, true); err == nil || !strings.Contains(err.Error(), "sessionExpiresAt") {
		t.Fatalf("HoldCamera without sessionExpiresAt = %v, want an error naming it", err)
	}
}

func TestSharedHeldCameraKeepsTheSessionHoldOpenUntilItsEnds(t *testing.T) {
	release := make(chan struct{})
	camera := &sharedCamera{holdFn: func(ctx context.Context, _ int) (*api.MessageRequestHoldEnd, error) {
		select {
		case <-release:
			return &api.MessageRequestHoldEnd{Reason: api.SESSIONCLOSED}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-held.Done():
		t.Fatal("the camera stopped while its session hold was open")
	case <-time.After(200 * time.Millisecond):
	}
	if got := camera.holdCalls.Load(); got != 1 || camera.holdIDs[0] != "initiate" {
		t.Fatalf("hold calls = %d for %v, want one hold on the initiate request", got, camera.holdIDs)
	}
	close(release)
	waitDone(t, held)
	if held.EndReason() != string(api.SESSIONCLOSED) {
		t.Fatalf("EndReason = %q, want SESSION_CLOSED", held.EndReason())
	}
}

func TestSharedHeldCameraEndsWithEachReasonThePortOSHoldGives(t *testing.T) {
	for _, reason := range []api.MessageRequestHoldEndReason{api.DETACHED, api.SESSIONCLOSED, api.EXPIRED} {
		camera := &sharedCamera{holdFn: func(context.Context, int) (*api.MessageRequestHoldEnd, error) {
			return &api.MessageRequestHoldEnd{Reason: reason}, nil
		}}
		held, err := holdSharedCamera(t, camera, true)
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, held)
		if held.EndReason() != string(reason) {
			t.Fatalf("EndReason = %q, want %s", held.EndReason(), reason)
		}
		if got := camera.holdCalls.Load(); got != 1 {
			t.Fatalf("%s: hold calls = %d, want the hold not reopened after an answer", reason, got)
		}
	}
}

func TestSharedHeldCameraReopensADroppedSessionHold(t *testing.T) {
	fastHold(t, 5*time.Second)
	camera := &sharedCamera{holdFn: func(_ context.Context, call int) (*api.MessageRequestHoldEnd, error) {
		if call < 3 {
			return nil, apiclient.NewConnectionError("connection reset", apiclient.ErrConnectionFailed)
		}
		return &api.MessageRequestHoldEnd{Reason: api.EXPIRED}, nil
	}}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, held)
	if got := camera.holdCalls.Load(); got != 3 {
		t.Fatalf("hold calls = %d, want the hold reopened after each drop", got)
	}
	if held.EndReason() != string(api.EXPIRED) {
		t.Fatalf("EndReason = %q", held.EndReason())
	}
}

func TestSharedHeldCameraEndsWhenTheSessionHoldStaysDown(t *testing.T) {
	fastHold(t, 60*time.Millisecond)
	camera := &sharedCamera{holdFn: func(context.Context, int) (*api.MessageRequestHoldEnd, error) {
		return nil, apiclient.NewConnectionError("connection refused", apiclient.ErrConnectionFailed)
	}}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, held)
	if held.EndReason() != endHoldLost {
		t.Fatalf("EndReason = %q, want %s", held.EndReason(), endHoldLost)
	}
}

func TestSharedHeldCameraEndsWhenPortOSDoesNotKnowTheSubscriber(t *testing.T) {
	camera := &sharedCamera{holdFn: func(context.Context, int) (*api.MessageRequestHoldEnd, error) {
		return nil, &apiclient.APIError{StatusCode: 404}
	}}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, held)
	if held.EndReason() != endNotAttached || camera.holdCalls.Load() != 1 {
		t.Fatalf("EndReason = %q after %d hold calls, want %s after one", held.EndReason(), camera.holdCalls.Load(), endNotAttached)
	}
}

func TestClosingASharedHeldCameraStopsItsSessionHoldAndDisconnects(t *testing.T) {
	stopped := make(chan struct{})
	camera := &sharedCamera{holdFn: func(ctx context.Context, _ int) (*api.MessageRequestHoldEnd, error) {
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the camera left its session hold open")
	}
	if got := len(camera.sent("disconnect-session")); got != 1 {
		t.Fatalf("sent %d disconnects, want 1", got)
	}
	if held.EndReason() != "" {
		t.Fatalf("EndReason = %q after a local close, want none", held.EndReason())
	}
}

func TestSharedHeldCameraStepsInItsOwnSession(t *testing.T) {
	camera := &sharedCamera{}
	held, err := holdSharedCamera(t, camera, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.PTZ(t.Context(), api.INTERFACE_PORTOS_CAMERA_STEP_PTZ, map[string]any{"direction": "LEFT", "sessionId": "somebody-else"}); err != nil {
		t.Fatal(err)
	}
	steps := camera.sent(api.INTERFACE_PORTOS_CAMERA_STEP_PTZ)
	if len(steps) != 1 {
		t.Fatalf("sent %d steps, want 1", len(steps))
	}
	body := steps[0].Message.Body
	if body["sessionId"] != "held" || body["direction"] != "LEFT" || steps[0].Message.Header.Namespace != api.INTERFACE_PORTOS_CAMERA {
		t.Fatalf("step = %+v %v: want the hold's own session ID", steps[0].Message.Header, body)
	}
	if steps[0].ResponseType == nil || *steps[0].ResponseType != api.SyncResponseType {
		t.Fatal("the step was not sent synchronously")
	}
}
