// Package transport chooses how a capability message reaches an endpoint:
// through Port OS directly, or through the local session host when the
// endpoint is held and the hold covers the message's capability.
package transport

import (
	"context"

	"github.com/portpowered/portos-cli/internal/api"
)

// Sender sends one capability message and returns Port OS's (or the host's)
// reply.
type Sender interface {
	Send(ctx context.Context, req *api.CreateMessageRequest, idempotencyKey string) (*api.MessageRequest, error)
}

// Resolver picks the Sender for a message to an endpoint in a capability
// namespace (a full interface path such as the remote-control one).
type Resolver interface {
	For(ctx context.Context, endpointID, namespace string) (Sender, error)
}

// Held reports whether s sends through a session host hold. Under a hold the
// host owns the endpoint's session, so callers send only the messages the hold
// serves (moves) and no start-session or end-session.
func Held(s Sender) bool {
	h, ok := s.(interface{ Held() bool })
	return ok && h.Held()
}
