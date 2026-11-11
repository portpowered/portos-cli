// Package messages answers the capability messages a caller sends to the
// session host under a hold's lease. Only the hold's own capabilities are
// served: moves go to the motion session, snapshots to the held camera, and
// session messages belong to the hold.
package messages

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/services/holds"
)

// Authorizer proves a lease owns a live hold on an endpoint that covers a kind.
type Authorizer interface {
	Authorize(lease, endpointID string, kind holds.Kind) (holds.Hold, error)
}

// Mover runs a move through a hold's motion session.
type Mover interface {
	Move(ctx context.Context, holdID string, body map[string]any) (*api.MessageRequest, error)
}

// Snapshotter returns the newest frame of a hold's camera as JPEG.
type Snapshotter interface {
	Snapshot(ctx context.Context, holdID string) (jpeg []byte, capturedAt time.Time, err error)
}

// Service dispatches one message sent to the host.
type Service interface {
	// Dispatch routes the message. A refusal by the host (a missing lease, a
	// session message the hold owns, a capability the hold does not serve) is a
	// FAILED reply, never an error; an error means the dispatch itself broke.
	Dispatch(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error)
}

type service struct {
	auth      Authorizer
	mover     Mover
	snapshots Snapshotter
	clock     clock.Clock
}

// New returns a messages Service.
func New(auth Authorizer, mover Mover, snapshots Snapshotter, c clock.Clock) Service {
	return &service{auth: auth, mover: mover, snapshots: snapshots, clock: c}
}

const (
	nameMove         = "move"
	nameStartSession = "start-session"
	nameEndSession   = "end-session"
)

func (s *service) Dispatch(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error) {
	if req == nil || req.Message.Header == nil {
		return nil, errors.New("messages: the request has no message header")
	}
	endpoint := req.Target.Id
	header := req.Message.Header
	if header.Namespace == api.INTERFACE_PORTOS_SNAPSHOT {
		return s.snapshot(ctx, lease, endpoint, header.Name)
	}
	if header.Namespace != api.INTERFACE_PORTOS_REMOTE_CONTROL {
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the session host serves only remote-control and snapshot for a held endpoint; send "+header.Namespace+"/"+header.Name+" without the lease"), nil
	}
	switch header.Name {
	case nameStartSession, nameEndSession:
		return s.refuse(endpoint, holds.CodeHoldOwnsSession, "the hold owns the remote-control session; "+header.Name+" is not sent while it is held"), nil
	case nameMove:
		hold, err := s.auth.Authorize(lease, endpoint, holds.KindMotion)
		if err != nil {
			var holdErr *holds.Error
			if errors.As(err, &holdErr) {
				return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
			}
			return nil, err
		}
		reply, err := s.mover.Move(ctx, hold.ID, req.Message.Body)
		if err == nil {
			return reply, nil
		}
		// The session may have ended between authorize and move. Ask the holds
		// again: if the hold is gone it answers with a typed refusal, which
		// reads better than the motion session's plain error.
		var holdErr *holds.Error
		if errors.As(err, &holdErr) {
			return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
		}
		if _, authErr := s.auth.Authorize(lease, endpoint, holds.KindMotion); errors.As(authErr, &holdErr) {
			return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
		}
		return reply, err
	default:
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the hold serves only move, not "+header.Name), nil
	}
}

// snapshot serves get-snapshot from the held camera. The image is always
// inline: the host has no URL to offer.
func (s *service) snapshot(ctx context.Context, lease, endpoint, name string) (*api.MessageRequest, error) {
	if name != api.INTERFACE_PORTOS_SNAPSHOT_GET_SNAPSHOT {
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the hold serves only get-snapshot, not "+name), nil
	}
	hold, err := s.auth.Authorize(lease, endpoint, holds.KindCamera)
	if err == nil {
		var jpeg []byte
		var capturedAt time.Time
		if jpeg, capturedAt, err = s.snapshots.Snapshot(ctx, hold.ID); err == nil {
			return s.snapshotReply(endpoint, jpeg, capturedAt), nil
		}
	}
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	return nil, fmt.Errorf("messages: snapshot: %w", err)
}

func (s *service) snapshotReply(endpoint string, jpeg []byte, capturedAt time.Time) *api.MessageRequest {
	size := len(jpeg)
	message := api.NewSnapshotInterfaceGetSnapshotResponseMessage(api.SnapshotInterfaceGetSnapshotResponseMessage{
		Image:        api.MediaContent{MimeType: api.Imagejpeg, Data: &jpeg, ByteLength: &size},
		TimeOfSample: capturedAt.UTC(),
	})
	return &api.MessageRequest{
		Id:                  "host-" + uuid.NewString(),
		Status:              api.SUCCEEDED,
		Deadline:            s.clock.Now(),
		SyncDeadlineSeconds: 1,
		Results:             []api.MessageRequestResult{{TargetId: endpoint, Message: &message}},
		Errors:              []api.MessageRequestError{},
	}
}

// refuse builds the Port OS reply for a host-side refusal, so callers and
// exitcode.Code treat it like any rejected message.
func (s *service) refuse(endpointID, code, message string) *api.MessageRequest {
	return Refusal(s.clock.Now(), endpointID, code, message)
}

// Refusal is the typed FAILED reply for a message the session host (or the
// CLI on its behalf) refuses because of a hold. The provider code is the hold
// code, which the CLI maps to its exit code.
func Refusal(now time.Time, endpointID, code, message string) *api.MessageRequest {
	errCode, family := api.ErrorCodeCONFLICT, api.ErrorFamilyCONFLICT
	switch code {
	case holds.CodeHoldLeaseRequired:
		errCode, family = api.ErrorCodePRECONDITIONREQUIRED, api.ErrorFamilyPRECONDITIONREQUIRED
	case holds.CodeNotServedByHold:
		errCode, family = api.ErrorCodeUNSUPPORTEDOPERATION, api.ErrorFamilyREQUESTINVALID
	case holds.CodeCameraUnavailable:
		errCode, family = api.ErrorCodeSERVICEUNAVAILABLE, api.ErrorFamilySERVICEUNAVAILABLE
	}
	return &api.MessageRequest{
		Id:                  "host-" + uuid.NewString(),
		Status:              api.FAILED,
		Deadline:            now,
		SyncDeadlineSeconds: 1,
		Results:             []api.MessageRequestResult{},
		Errors: []api.MessageRequestError{{
			Code:     api.REJECTED,
			TargetId: endpointID,
			Error: &api.Error{
				Code:    errCode,
				Family:  family,
				Message: message,
				Details: &api.ErrorDetails{ProviderCode: &code},
			},
		}},
	}
}
