package commands

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

// countingCamera counts the disconnects the held camera sends.
type countingCamera struct {
	*pionCamera
	disconnects atomic.Int32
	lifetime    atomic.Int64
}

func (c *countingCamera) Send(ctx context.Context, request *api.CreateMessageRequest, keys ...string) (*api.MessageRequest, error) {
	switch request.Message.Header.Name {
	case "disconnect-session":
		c.disconnects.Add(1)
	case rtcSessionInitiate:
		c.lifetime.Store(int64(request.Message.Body["sessionDuration"].(int)))
	}
	return c.pionCamera.Send(ctx, request, keys...)
}

type countingClient struct {
	apiclient.Client
	camera *countingCamera
}

func (c countingClient) Messages() apiclient.MessagesClient { return c.camera }
func (c countingClient) Relay() apiclient.RelayClient {
	return cameraClient{}.Relay()
}

func holdTestCamera(t *testing.T, seconds int) (*HeldCamera, *countingCamera) {
	t.Helper()
	camera := &countingCamera{pionCamera: &pionCamera{t: t, src: ffmpegSource(t, seconds, 15)}}
	recorder := NewRecorder(countingClient{camera: camera}, logging.NewLoggerRef())
	recorder.iceServers = nil
	held, err := recorder.HoldCamera(t.Context(), HeldCameraParams{
		EndpointID: "camera", SessionDomain: "cli", SessionID: "held", Lifetime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("HoldCamera: %v", err)
	}
	t.Cleanup(func() { _ = held.Close(context.Background()) })
	return held, camera
}

func isJPEG(data []byte) bool { return bytes.HasPrefix(data, []byte{0xff, 0xd8}) }

func TestHeldCameraServesTheNewestFrameAcrossSegments(t *testing.T) {
	held, camera := holdTestCamera(t, 8)
	if got := camera.lifetime.Load(); got != 1800 {
		t.Fatalf("asked for a %d s camera session, want 1800", got)
	}
	jpeg, at, err := held.Latest(t.Context(), time.Second)
	if err != nil || !isJPEG(jpeg) {
		t.Fatalf("first snapshot: %v (%d bytes)", err, len(jpeg))
	}
	if age := time.Since(at); age > time.Second {
		t.Fatalf("first frame is %s old", age)
	}
	// Past one segment span the camera keeps serving from a rotated segment.
	time.Sleep(heldSegmentSpan + 1500*time.Millisecond)
	jpeg, at, err = held.Latest(t.Context(), time.Second)
	if err != nil || !isJPEG(jpeg) {
		t.Fatalf("snapshot after rotation: %v (%d bytes)", err, len(jpeg))
	}
	if age := time.Since(at); age > time.Second {
		t.Fatalf("frame after rotation is %s old", age)
	}
	held.mu.Lock()
	segments := held.counter
	held.mu.Unlock()
	if segments < 2 {
		t.Fatalf("the camera wrote %d segments, want a rotation", segments)
	}
}

func TestHeldCameraCloseDisconnectsAndEndsTheStream(t *testing.T) {
	held, camera := holdTestCamera(t, 3)
	if err := held.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case <-held.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not close")
	}
	if got := camera.disconnects.Load(); got != 1 {
		t.Fatalf("sent %d disconnects, want 1", got)
	}
	if _, _, err := held.Latest(t.Context(), time.Second); err == nil {
		t.Fatal("a closed camera served a frame")
	}
}

func TestHeldCameraRefusesAShortLifetime(t *testing.T) {
	recorder := NewRecorder(cameraClient{camera: &pionCamera{t: t}}, logging.NewLoggerRef())
	if _, err := recorder.HoldCamera(t.Context(), HeldCameraParams{EndpointID: "camera", SessionID: "held"}); err == nil {
		t.Fatal("a zero lifetime was accepted")
	}
}
