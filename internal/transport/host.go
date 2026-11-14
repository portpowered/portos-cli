package transport

import (
	"context"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/services/messages"
)

// hostAPI is the part of the host client the transport uses.
type hostAPI interface {
	ListHolds(ctx context.Context) ([]hostclient.Hold, error)
	Send(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error)
	Record(ctx context.Context, lease, endpointID string, duration time.Duration) (hostclient.Recording, error)
	Capture(ctx context.Context, lease, endpointID string, from time.Time, duration time.Duration) (hostclient.AudioCapture, error)
}

// hostSender sends through a hold on the session host under a lease.
type hostSender struct {
	host  hostAPI
	lease string
}

func (h *hostSender) Held() bool { return true }

// Send posts to the host. The host does not deduplicate, so the key is unused.
func (h *hostSender) Send(ctx context.Context, req *api.CreateMessageRequest, _ string) (*api.MessageRequest, error) {
	return h.host.Send(ctx, h.lease, req)
}

// Record records the held camera through the host under the lease.
func (h *hostSender) Record(ctx context.Context, endpointID string, duration time.Duration) (hostclient.Recording, error) {
	return h.host.Record(ctx, h.lease, endpointID, duration)
}

// Capture captures the held camera's microphone through the host under the lease.
func (h *hostSender) Capture(ctx context.Context, endpointID string, from time.Time, duration time.Duration) (hostclient.AudioCapture, error) {
	return h.host.Capture(ctx, h.lease, endpointID, from, duration)
}

// refusingSender answers every message with a typed FAILED reply, so a caller
// that holds a lease with no live hold behind it never reaches the cloud.
type refusingSender struct {
	endpointID string
	code       string
	message    string
}

// Held is true: the caller sends only the hold's messages, not its own session.
func (r *refusingSender) Held() bool { return true }

func (r *refusingSender) Send(context.Context, *api.CreateMessageRequest, string) (*api.MessageRequest, error) {
	return messages.Refusal(time.Now(), r.endpointID, r.code, r.message), nil
}

// Record refuses as Send does, so a stale lease exits as a refused message would.
func (r *refusingSender) Record(context.Context, string, time.Duration) (hostclient.Recording, error) {
	return hostclient.Recording{}, &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), r.endpointID, r.code, r.message)}
}

// Capture refuses as Send does.
func (r *refusingSender) Capture(context.Context, string, time.Time, time.Duration) (hostclient.AudioCapture, error) {
	return hostclient.AudioCapture{}, &apiclient.MessageRequestFailed{Request: messages.Refusal(time.Now(), r.endpointID, r.code, r.message)}
}
