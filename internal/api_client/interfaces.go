package api

import (
	"context"
	"net"

	"github.com/portpowered/portos-cli/internal/api"
)

// Client defines the interface for the Port OS API client
type Client interface {
	// Endpoints returns the endpoints API client
	Endpoints() EndpointsClient

	// Groups returns the endpoint groups API client
	Groups() GroupsClient

	// Messages returns the messages API client
	Messages() MessagesClient

	// Rules returns the access control rules API client
	Rules() RulesClient

	// RulesFor returns a rules client that targets baseURL instead of the configured API base URL.
	RulesFor(baseURL string) RulesClient

	// WebSocket returns the WebSocket API client
	WebSocket() WebSocketClient

	// Relay returns the media relay API client
	Relay() RelayClient
}

// RelayClient fetches credentials for the Port OS media relay and tunnels to it.
type RelayClient interface {
	// Credentials returns short-lived TURN credentials for the authenticated principal.
	Credentials(ctx context.Context) (*api.RelayCredentials, error)
	// Proxied reports whether requests to the API go through an HTTP proxy (HTTPS_PROXY and
	// friends). Such networks usually refuse direct TCP to the relay.
	Proxied() bool
	// Tunnel opens a byte stream to the relay through the API's GET /relay-tunnel WebSocket,
	// through the HTTP proxy when one is configured.
	Tunnel(ctx context.Context) (net.Conn, error)
}

// EndpointsClient defines the interface for endpoint operations
type EndpointsClient interface {
	// Enumerate lists all available endpoints for the authenticated user
	// owner: The owner of the endpoints to get. Use '~caller' to get endpoints for the authenticated user
	// nextToken: Optional token for pagination
	// maxResults: Optional maximum number of results to return
	// typeFilter: Optional filter by endpoint primary type
	// serialNumber: Optional filter by serial number
	Enumerate(ctx context.Context, owner string, nextToken *string, maxResults *int, typeFilter *string, serialNumber *string) (*api.EndpointQueryResponse, error)

	// GetByID queries for an endpoint by its full ID
	// id: The full endpoint identifier
	GetByID(ctx context.Context, id string) (*api.EndpointQueryResponse, error)

	// GetByIDWithExpand queries for an endpoint by its full ID and requested expansion set.
	// id: The full endpoint identifier (e.g. port1/principals/abc/endpoints/xyz)
	GetByIDWithExpand(ctx context.Context, id string, expand string, forceDeviceAttributeQuery bool) (*api.EndpointQueryResponse, error)
}

// GroupsClient reads endpoint groups (rooms, floors, zones and customer groups).
type GroupsClient interface {
	// Enumerate lists one page of the caller's enabled endpoint groups. expand passes the API's
	// expansion tokens, such as interfaces.attributes for each group route's stored attributes.
	Enumerate(ctx context.Context, nextToken *string, maxResults *int, expand []string) (*api.EnumerateEndpointGroupsResponse, error)
}

// MessagesClient defines the interface for message operations
type MessagesClient interface {
	// Send sends a message to a recipient
	// request: The message request containing the message, target, and optional response type
	Send(ctx context.Context, request *api.CreateMessageRequest, idempotencyKeys ...string) (*api.MessageRequest, error)

	// Hold keeps GET /message-requests/{id}/hold open for the shared session subscriber the
	// message request attached. It returns when the attachment ends, with the reason; a
	// dropped connection or a 404 (no live shared subscriber) is an error.
	Hold(ctx context.Context, requestID string) (*api.MessageRequestHoldEnd, error)
}

// RulesClient defines the interface for access control rule operations
type RulesClient interface {
	// List lists access control rules with optional filtering and pagination
	List(ctx context.Context, params *api.GetAccessControlRulesParams) (*api.GetAccessControlRulesResponse, error)

	// Create creates a new access control rule
	Create(ctx context.Context, request *api.UpsertAccessControlRuleRequest) (*api.AccessControlRule, error)

	// Delete deletes an access control rule by its full ID
	// ruleID is the full port identifier path (e.g. "port1/principals/abc/access-control-rules/def")
	Delete(ctx context.Context, ruleID string) error

	// DeleteWithReason deletes a rule and records reason in the audit log
	DeleteWithReason(ctx context.Context, ruleID, reason string) error

	// Evaluate evaluates an operation for a requester against the stored rules
	Evaluate(ctx context.Context, request *api.EvaluateAccessControlRuleRequest) (*api.EvaluateAccessControlRuleResponse, error)
}

// WebSocketClient defines the interface for WebSocket operations.
// Used by the serve command to maintain a persistent connection to the backend.
type WebSocketClient interface {
	// Connect establishes a WebSocket connection to the backend's /websockets endpoint
	Connect(ctx context.Context) error

	// ReadMessage reads the next message from the WebSocket connection
	ReadMessage() ([]byte, error)

	// WriteMessage sends a text message over the WebSocket connection
	WriteMessage(message []byte) error

	// StartPingLoop sends periodic ping frames to keep the connection alive.
	// Blocks until the context is cancelled or a ping write fails.
	StartPingLoop(ctx context.Context) error

	// Close performs a graceful WebSocket close handshake
	Close() error
}
