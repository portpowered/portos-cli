// Package transport chooses how a capability message reaches an endpoint:
// through Port OS directly, or through the local session host when the
// endpoint is held and the hold covers the message's capability.
package transport

import (
	"context"
	"errors"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/hostclient"
)

// Sender sends one capability message and returns Port OS's (or the host's)
// reply.
type Sender interface {
	Send(ctx context.Context, req *api.CreateMessageRequest, idempotencyKey string) (*api.MessageRequest, error)
}

// Resolver picks the Sender for a message to an endpoint in a capability
// namespace (a full interface path such as the remote-control one).
type Resolver interface {
	For(ctx context.Context, endpointID, namespace, name string) (Sender, error)
}

// Held reports whether s sends through a session host hold. Under a hold the
// host owns the endpoint's session, so callers send only the messages the hold
// serves (moves) and no start-session or end-session.
func Held(s Sender) bool {
	h, ok := s.(interface{ Held() bool })
	return ok && h.Held()
}

// RecordHeld records a held camera through the sender Resolver.For gave for the rtc-session
// namespace. Only a held sender records; the video comes from the hold's camera session.
func RecordHeld(ctx context.Context, s Sender, endpointID string, duration time.Duration) (hostclient.Recording, error) {
	recorder, ok := s.(interface {
		Record(ctx context.Context, endpointID string, duration time.Duration) (hostclient.Recording, error)
	})
	if !ok {
		return hostclient.Recording{}, errors.New("only a held camera records through the session host")
	}
	return recorder.Record(ctx, endpointID, duration)
}
