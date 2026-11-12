package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
)

// MessagesClientImpl implements the MessagesClient interface
type MessagesClientImpl struct {
	client *ClientImpl
}

// Send sends a message to a recipient
func (m *MessagesClientImpl) Send(ctx context.Context, request *api.CreateMessageRequest, idempotencyKeys ...string) (*api.MessageRequest, error) {
	if request == nil {
		return nil, NewSetupError("request cannot be nil")
	}

	// Serialize request body
	body, err := json.Marshal(request)
	if err != nil {
		return nil, &SetupError{Message: "failed to marshal request: " + err.Error(), Err: err}
	}

	// Perform request
	key := uuid.NewString()
	if len(idempotencyKeys) > 0 && idempotencyKeys[0] != "" {
		key = idempotencyKeys[0]
	}
	resp, err := m.client.doRequest(ctx, http.MethodPost, "/message-requests", strings.NewReader(string(body)), map[string]string{"Idempotency-Key": key})
	if err != nil {
		var setup *SetupError
		if errors.As(err, &setup) {
			return nil, err
		}
		return nil, fmt.Errorf("message request response unavailable; recover identical input with --idempotency-key %q: %w", key, err)
	}

	// Parse response
	var result api.MessageRequest
	if err := m.client.handleResponse(resp, &result); err != nil {
		var rejected *APIError
		if errors.As(err, &rejected) {
			return nil, err
		}
		return nil, fmt.Errorf("message request response unavailable; recover identical input with --idempotency-key %q: %w", key, err)
	}
	if !validMessageRequest(result) {
		return nil, fmt.Errorf("invalid message request response; recover identical input with --idempotency-key %q", key)
	}
	return &result, nil
}

// Hold waits on the message request's session hold until the subscriber's attachment ends.
func (m *MessagesClientImpl) Hold(ctx context.Context, requestID string) (*api.MessageRequestHoldEnd, error) {
	if requestID == "" {
		return nil, NewSetupError("message request ID cannot be empty")
	}
	resp, err := m.client.doRequest(ctx, http.MethodGet, "/message-requests/"+url.PathEscape(requestID)+"/hold", nil, nil)
	if err != nil {
		return nil, err
	}
	var result api.MessageRequestHoldEnd
	if err := m.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	switch result.Reason {
	case api.DETACHED, api.SESSIONCLOSED, api.EXPIRED:
		return &result, nil
	default:
		return nil, fmt.Errorf("session hold ended with unknown reason %q", result.Reason)
	}
}

func validMessageRequest(request api.MessageRequest) bool {
	if request.Id == "" || request.Deadline.IsZero() || request.SyncDeadlineSeconds < 1 || request.Results == nil || request.Errors == nil {
		return false
	}
	for _, result := range request.Results {
		if result.TargetId == "" {
			return false
		}
		if message := result.Message; message != nil && (message.Header == nil || message.Header.Namespace == "" || message.Header.Name == "" || message.Header.Version == "" || message.Body == nil) {
			return false
		}
	}
	for _, failure := range request.Errors {
		if failure.TargetId == "" {
			return false
		}
		switch failure.Code {
		case api.REJECTED, api.UNREACHABLE, api.NOTACCEPTED, api.TIMEDOUT:
		default:
			return false
		}
		if failure.Error != nil && (failure.Error.Code == "" || failure.Error.Family == "" || failure.Error.Message == "") {
			return false
		}
	}
	switch request.Status {
	case api.QUEUED, api.INPROGRESS:
		return true
	case api.SUCCEEDED:
		return len(request.Results) > 0 && len(request.Errors) == 0
	case api.FAILED:
		return len(request.Results) == 0 && len(request.Errors) > 0
	case api.PARTIALLYSUCCEEDED:
		return len(request.Results) > 0 && len(request.Errors) > 0
	default:
		return false
	}
}
