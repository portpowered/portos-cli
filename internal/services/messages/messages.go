// Package messages answers the capability messages a caller sends to the
// session host under a hold's lease. Only the hold's own capabilities are
// served: moves go to the motion session, snapshots and play-audio to the held
// camera, and session messages belong to the hold.
package messages

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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
	// GoToPoint runs a go-to-point trip through the hold's motion session.
	GoToPoint(ctx context.Context, holdID string, body map[string]any) (*api.MessageRequest, error)
}

// Camera serves a hold's camera: its newest frame as JPEG, Ogg/Opus clips
// played on its talkback track, and PTZ moves in its own session.
type Camera interface {
	Snapshot(ctx context.Context, holdID string) (jpeg []byte, capturedAt time.Time, err error)
	PlayAudio(ctx context.Context, holdID string, ogg []byte) (time.Duration, error)
	// PTZ sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) with the hold's session ID
	// filled in and returns the reply from Port OS.
	PTZ(ctx context.Context, holdID, name string, body map[string]any) (*api.MessageRequest, error)
}

// Service dispatches one message sent to the host.
type Service interface {
	// Dispatch routes the message. A refusal by the host (a missing lease, a
	// session message the hold owns, a capability the hold does not serve) is a
	// FAILED reply, never an error; an error means the dispatch itself broke.
	Dispatch(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error)
}

type service struct {
	auth   Authorizer
	mover  Mover
	camera Camera
	clock  clock.Clock
}

// New returns a messages Service.
func New(auth Authorizer, mover Mover, camera Camera, c clock.Clock) Service {
	return &service{auth: auth, mover: mover, camera: camera, clock: c}
}

const (
	nameMove         = "move"
	nameStartSession = "start-session"
	nameEndSession   = "end-session"
	nameGoToPoint    = api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_GO_TO_POINT
)

func (s *service) Dispatch(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error) {
	if req == nil || req.Message.Header == nil {
		return nil, errors.New("messages: the request has no message header")
	}
	endpoint := req.Target.Id
	header := req.Message.Header
	switch header.Namespace {
	case api.INTERFACE_PORTOS_SNAPSHOT:
		return s.snapshot(ctx, lease, endpoint, header.Name)
	case api.INTERFACE_PORTOS_MEDIA_PLAYER:
		return s.playAudio(ctx, lease, endpoint, header.Name, req.Message.Body)
	case api.INTERFACE_PORTOS_CAMERA:
		return s.ptz(ctx, lease, endpoint, header.Name, req.Message.Body)
	case api.INTERFACE_PORTOS_RTC_SESSION:
		return s.refuse(endpoint, holds.CodeHoldOwnsSession, "the hold owns the camera session; record it with port message record and the lease, or release the hold first"), nil
	case api.INTERFACE_PORTOS_REMOTE_CONTROL:
	case api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER:
		if header.Name != nameGoToPoint {
			return s.refuse(endpoint, holds.CodeNotServedByHold, "the hold serves only go-to-point of the robotic-vacuum-cleaner messages; send "+header.Namespace+"/"+header.Name+" without the lease"), nil
		}
		return s.goToPoint(ctx, lease, endpoint, req.Message.Body)
	default:
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the session host serves only remote-control, go-to-point, snapshot and media-player play-audio for a held endpoint; send "+header.Namespace+"/"+header.Name+" without the lease"), nil
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

// goToPoint runs a trip on the hold's motion session. The trip counts as
// activity for the dead-man: the lease is authorized again once it ends, so the
// clock restarts from the arrival. A refusal by Port OS is the reply, unchanged.
func (s *service) goToPoint(ctx context.Context, lease, endpoint string, body map[string]any) (*api.MessageRequest, error) {
	hold, err := s.auth.Authorize(lease, endpoint, holds.KindMotion)
	if err == nil {
		var reply *api.MessageRequest
		reply, err = s.mover.GoToPoint(ctx, hold.ID, body)
		if err == nil {
			_, _ = s.auth.Authorize(lease, endpoint, holds.KindMotion)
			return reply, nil
		}
	}
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		_, _ = s.auth.Authorize(lease, endpoint, holds.KindMotion)
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	if _, authErr := s.auth.Authorize(lease, endpoint, holds.KindMotion); errors.As(authErr, &holdErr) {
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	return nil, fmt.Errorf("messages: go-to-point: %w", err)
}

// ptz sends a camera PTZ message (step-ptz, move-ptz or stop-ptz) in the held camera's own
// session, so the caller never handles the session ID. The reply is Port OS's, unchanged.
func (s *service) ptz(ctx context.Context, lease, endpoint, name string, body map[string]any) (*api.MessageRequest, error) {
	switch name {
	case api.INTERFACE_PORTOS_CAMERA_STEP_PTZ, api.INTERFACE_PORTOS_CAMERA_MOVE_PTZ, api.INTERFACE_PORTOS_CAMERA_STOP_PTZ:
	default:
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the hold serves only step-ptz, move-ptz and stop-ptz of the camera messages; send "+name+" without the lease"), nil
	}
	hold, err := s.auth.Authorize(lease, endpoint, holds.KindCamera)
	if err == nil {
		var reply *api.MessageRequest
		if reply, err = s.camera.PTZ(ctx, hold.ID, name, body); err == nil {
			return reply, nil
		}
	}
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	return nil, fmt.Errorf("messages: %s: %w", name, err)
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
		if jpeg, capturedAt, err = s.camera.Snapshot(ctx, hold.ID); err == nil {
			return s.snapshotReply(endpoint, jpeg, capturedAt), nil
		}
	}
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	return nil, fmt.Errorf("messages: snapshot: %w", err)
}

// playAudio plays an inline Ogg/Opus clip on the held camera's talkback track
// and answers once it has started.
func (s *service) playAudio(ctx context.Context, lease, endpoint, name string, body map[string]any) (*api.MessageRequest, error) {
	if name != api.INTERFACE_PORTOS_MEDIA_PLAYER_PLAY_AUDIO {
		return s.refuse(endpoint, holds.CodeNotServedByHold, "the hold serves only play-audio, not "+name), nil
	}
	hold, err := s.auth.Authorize(lease, endpoint, holds.KindCamera)
	if err == nil {
		var ogg []byte
		if ogg, err = oggClip(body); err == nil {
			if _, err = s.camera.PlayAudio(ctx, hold.ID, ogg); err == nil {
				return s.playingReply(endpoint), nil
			}
		}
	}
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		return s.refuse(endpoint, holdErr.Code, holdErr.Message), nil
	}
	return nil, fmt.Errorf("messages: play-audio: %w", err)
}

// oggClip reads the clip from a play-audio body. Under a camera hold the clip
// travels inline as Ogg/Opus; the host fetches nothing.
func oggClip(body map[string]any) ([]byte, error) {
	audio, _ := body["audio"].(map[string]any)
	data, _ := audio["data"].(string)
	mime, _ := audio["mimeType"].(string)
	invalid := func(message string) error {
		return &holds.Error{Code: holds.CodeInvalidRequest, Message: message}
	}
	if data == "" {
		return nil, invalid("under a camera hold, play-audio takes the clip inline as audio.data; port message play-audio --file sends it")
	}
	if mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])); mime != "audio/ogg" && mime != "audio/opus" {
		return nil, invalid("under a camera hold, play-audio takes Ogg/Opus (audio/ogg), not " + mime + "; port message play-audio --file converts it")
	}
	ogg, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, invalid("audio.data is not base64")
	}
	return ogg, nil
}

func (s *service) playingReply(endpoint string) *api.MessageRequest {
	message := api.NewMediaPlayerInterfacePlaybackResultMessage(api.MediaPlayerInterfacePlaybackResultMessage{PlaybackState: api.PlaybackStatePlaying})
	return &api.MessageRequest{
		Id:                  "host-" + uuid.NewString(),
		Status:              api.SUCCEEDED,
		Deadline:            s.clock.Now(),
		SyncDeadlineSeconds: 1,
		Results:             []api.MessageRequestResult{{TargetId: endpoint, Message: &message}},
		Errors:              []api.MessageRequestError{},
	}
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
	case holds.CodeInvalidRequest:
		errCode, family = api.ErrorCodeBADREQUEST, api.ErrorFamilyREQUESTINVALID
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
