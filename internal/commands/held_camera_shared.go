package commands

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
)

// Session hold timing. Variables so a test can shorten them.
var (
	// holdReopenWindow is how long a dropped session hold may stay down before the subscriber
	// counts as lost. Port OS detaches a subscriber 30 s after its hold drops, so reopening
	// has to succeed inside that.
	holdReopenWindow = 25 * time.Second
	// holdRetryDelay is the pause before a dropped session hold is opened again.
	holdRetryDelay = time.Second
)

const (
	// endHoldLost is the end reason when the session hold could not be reopened in time, and
	// endNotAttached when Port OS no longer knows the subscriber.
	endHoldLost    = "HOLD_LOST"
	endNotAttached = "NOT_ATTACHED"
)

// heldSessionRequest is the SYNC initiate-session-with-offer request for a held camera. A
// shared camera attaches as a subscriber of the endpoint's shared session.
func heldSessionRequest(params HeldCameraParams, offerSDP string) *api.CreateMessageRequest {
	request := sessionRequest(params.EndpointID, params.SessionDomain, params.SessionID, params.Lifetime, offerSDP)
	if params.Shared {
		request.Message.Body["sessionMode"] = string(api.RtcSessionInterfaceInitiateSessionWithOfferMessageSessionModeSHARED)
	}
	return request
}

// sessionExpiry reads sessionExpiresAt from the answer of a shared session.
func sessionExpiry(response *api.MessageRequest) (time.Time, error) {
	for _, result := range response.Results {
		if result.Message == nil {
			continue
		}
		text, _ := result.Message.Body["sessionExpiresAt"].(string)
		if text == "" {
			break
		}
		expires, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return time.Time{}, fmt.Errorf("read sessionExpiresAt %q: %w", text, err)
		}
		return expires, nil
	}
	return time.Time{}, errors.New("the shared camera answer carries no sessionExpiresAt")
}

// Shared reports whether the camera is a subscriber of a shared session.
func (h *HeldCamera) Shared() bool { return h.params.Shared }

// ExpiresAt is when a shared camera's session ends; zero for a single-peer camera.
func (h *HeldCamera) ExpiresAt() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.expiresAt
}

// EndReason says why the shared session hold ended: DETACHED, SESSION_CLOSED or EXPIRED from
// Port OS, or HOLD_LOST and NOT_ATTACHED when the hold could not be kept. Empty while the camera
// runs and when it ended some other way.
func (h *HeldCamera) EndReason() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.endReason
}

func (h *HeldCamera) endWith(reason string) {
	h.mu.Lock()
	h.endReason = reason
	h.mu.Unlock()
	h.markDone()
}

// runHold keeps the message request's session hold open for as long as the subscriber is
// attached. A hold that drops is opened again within holdReopenWindow; the camera ends when
// Port OS answers the hold, when the subscriber is unknown, or when the hold stays down.
func (h *HeldCamera) runHold(requestID string) {
	var downSince time.Time
	for {
		openedAt := time.Now()
		end, err := h.recorder.apiClient.Messages().Hold(h.life, requestID)
		if err == nil {
			h.endWith(string(end.Reason))
			return
		}
		if h.life.Err() != nil {
			return
		}
		var apiErr *apiclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			h.endWith(endNotAttached)
			return
		}
		now := time.Now()
		if downSince.IsZero() || now.Sub(openedAt) > holdRetryDelay {
			downSince = now
		}
		h.recorder.logger.Logger().Warn("shared session hold dropped", zap.Error(err))
		if now.Sub(downSince) >= holdReopenWindow {
			h.endWith(endHoldLost)
			return
		}
		timer := time.NewTimer(holdRetryDelay)
		select {
		case <-h.life.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// PTZ sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) for this camera's own session.
// A sessionId in body is replaced.
func (h *HeldCamera) PTZ(ctx context.Context, name string, body map[string]any) (*api.MessageRequest, error) {
	h.mu.Lock()
	closed := h.closed
	h.mu.Unlock()
	if closed {
		return nil, errors.New("the held camera is closed")
	}
	ptzBody := map[string]any{}
	for key, value := range body {
		ptzBody[key] = value
	}
	ptzBody["sessionId"] = h.params.SessionID
	messageType := api.INTERFACE_PORTOS_CAMERA + "/" + name
	sync := api.SyncResponseType
	return h.recorder.apiClient.Messages().Send(ctx, &api.CreateMessageRequest{
		Target: api.CreateMessageTarget{Type: api.EndpointTarget, Id: h.params.EndpointID},
		Message: api.Message{
			Type:   &messageType,
			Header: &api.MessageHeader{Namespace: api.INTERFACE_PORTOS_CAMERA, Name: name, Version: "1.0"},
			Body:   ptzBody,
		},
		ResponseType: &sync,
	}, uuid.NewString())
}
