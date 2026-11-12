// Package portos is the Port OS client the host services share: it sends one
// capability message synchronously and returns Port OS's reply.
package portos

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
)

// Client sends capability messages to an endpoint.
type Client interface {
	// SendSync sends the message with responseType SYNC and a fresh
	// Idempotency-Key, and returns Port OS's reply unchanged. A reply whose
	// status is FAILED is a reply, not an error; the error is for transport and
	// HTTP failures.
	SendSync(ctx context.Context, endpointID, namespace, name string, body map[string]any) (*api.MessageRequest, error)
}

const messageVersion = "1.0"

type client struct {
	messages apiclient.MessagesClient
}

// New returns a Client over the Port OS messages API. Token refresh happens in
// the auth service the messages client was built with.
func New(messages apiclient.MessagesClient) Client {
	return &client{messages: messages}
}

var _ Client = (*client)(nil)

func (c *client) SendSync(ctx context.Context, endpointID, namespace, name string, body map[string]any) (*api.MessageRequest, error) {
	messageType := namespace + "/" + name
	sync := api.SyncResponseType
	request := &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: endpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: namespace, Name: name, Version: messageVersion},
			Body:   body,
		},
		ResponseType: &sync,
	}
	reply, err := c.messages.Send(ctx, request, uuid.NewString())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return reply, nil
}

// IsUnauthorized reports whether err is an HTTP 401 from Port OS.
func IsUnauthorized(err error) bool {
	return errors.Is(err, apiclient.ErrUnauthorized)
}

// ProviderCodes returns the provider code and Port OS error code of every
// failure recorded on the reply, in order.
func ProviderCodes(reply *api.MessageRequest) []string {
	if reply == nil {
		return nil
	}
	var codes []string
	for _, failure := range reply.Errors {
		if failure.Error == nil {
			continue
		}
		if failure.Error.Details != nil && failure.Error.Details.ProviderCode != nil {
			codes = append(codes, *failure.Error.Details.ProviderCode)
		}
		codes = append(codes, string(failure.Error.Code))
	}
	return codes
}

// OutcomeUnknown reports whether a dispatch on the reply ended TIMED_OUT: Port
// OS stopped waiting at the request deadline, so the device may still have acted.
// Its error body is then often a generic INTERNAL, which says nothing about the device.
func OutcomeUnknown(reply *api.MessageRequest) bool {
	if reply == nil {
		return false
	}
	for _, failure := range reply.Errors {
		if failure.Code == api.TIMEDOUT {
			return true
		}
	}
	return false
}

// HasCode reports whether the reply recorded a failure with the given provider
// code or Port OS error code.
func HasCode(reply *api.MessageRequest, code string) bool {
	for _, have := range ProviderCodes(reply) {
		if have == code {
			return true
		}
	}
	return false
}
