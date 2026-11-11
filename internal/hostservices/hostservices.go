// Package hostservices composes the services the session host serves (holds,
// motion, camera, messages) and adapts them to the host's own interfaces.
package hostservices

import (
	"context"
	"errors"
	"time"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/portos"
	"github.com/portpowered/portos-cli/internal/services/camera"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/portpowered/portos-cli/internal/services/messages"
	"github.com/portpowered/portos-cli/internal/services/motion"
)

// fatalReleaseTimeout bounds the release a motion failure triggers.
const fatalReleaseTimeout = holds.DeadManCloseTimeout

// pauseReleaseTimeout bounds ending every hold after the machine paused.
const pauseReleaseTimeout = holds.DeadManCloseTimeout

// Options configure New.
type Options struct {
	Clock      clock.Clock
	Portos     portos.Client
	Keepalive  keepalive.Mode
	LoginCheck holds.LoginCheck
	// Camera opens held camera streams; without it holds cannot cover the camera.
	Camera camera.Opener
}

// Built is what the host runs with.
type Built struct {
	Services host.Services
	// OnPause ends every hold; the host's watchdog calls it when the machine
	// slept, because a robot left in remote control has been unattended.
	OnPause func(gap time.Duration)
	// OnShutdown ends every hold when a signal stops the host, so the robot docks.
	OnShutdown func(ctx context.Context)
}

// New composes the holds, motion and messages services.
func New(opts Options) Built {
	var holdSvc holds.Service
	motionSvc := motion.New(opts.Portos, opts.Clock, opts.Keepalive, func(holdID string, reason holds.Reason, detail string) {
		ctx, cancel := context.WithTimeout(context.Background(), fatalReleaseTimeout)
		defer cancel()
		_ = holdSvc.Release(ctx, holdID, reason, detail)
	})
	sessions := map[holds.Kind]holds.Session{holds.KindMotion: motionSvc}
	var cameraSvc camera.Service
	if opts.Camera != nil {
		cameraSvc = camera.New(opts.Camera, opts.Clock, func(holdID string, reason holds.Reason, detail string) {
			endCameraOnlyHold(holdSvc, holdID, reason, detail)
		})
		sessions[holds.KindCamera] = cameraSvc
	}
	holdSvc = holds.New(opts.Clock, sessions, opts.LoginCheck)
	return Built{
		Services: host.Services{
			Holds:      &holdAdapter{holds: holdSvc, motion: motionSvc, camera: cameraSvc},
			Messages:   messages.New(holdSvc, motionSvc, heldCamera{camera: cameraSvc}, opts.Clock),
			Recordings: recordings{holds: holdSvc, camera: cameraSvc},
		},
		OnShutdown: func(ctx context.Context) {
			holdSvc.EndAll(ctx, holds.ReasonHostShutdown)
		},
		OnPause: func(time.Duration) {
			ctx, cancel := context.WithTimeout(context.Background(), pauseReleaseTimeout)
			defer cancel()
			holdSvc.EndAll(ctx, holds.ReasonHostPaused)
		},
	}
}

// endCameraOnlyHold ends a hold whose only session was the camera that just
// stopped. A hold that also drives the robot keeps going; its snapshots answer
// camera_unavailable until the caller holds the camera again.
func endCameraOnlyHold(holdSvc holds.Service, holdID string, reason holds.Reason, detail string) {
	h, err := holdSvc.Get(holdID)
	if err != nil || len(h.Kinds) != 1 || h.Kinds[0] != holds.KindCamera {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), fatalReleaseTimeout)
	defer cancel()
	_ = holdSvc.Release(ctx, holdID, reason, detail)
}

// heldCamera serves the messages service's camera messages from the camera service.
type heldCamera struct{ camera camera.Service }

var errNoCamera = &holds.Error{Code: holds.CodeCameraUnavailable, Message: "this session host has no camera"}

func (c heldCamera) Snapshot(ctx context.Context, holdID string) ([]byte, time.Time, error) {
	if c.camera == nil {
		return nil, time.Time{}, errNoCamera
	}
	frame, err := c.camera.Snapshot(ctx, holdID)
	return frame.JPEG, frame.CapturedAt, err
}

func (c heldCamera) PlayAudio(ctx context.Context, holdID string, ogg []byte) (time.Duration, error) {
	if c.camera == nil {
		return 0, errNoCamera
	}
	return c.camera.PlayAudio(ctx, holdID, ogg)
}

// recordings serves POST /recordings from the camera service under a camera hold's lease.
type recordings struct {
	holds  holds.Service
	camera camera.Service
}

func (r recordings) Record(ctx context.Context, lease, endpointID string, duration time.Duration) (host.Recording, error) {
	if r.camera == nil {
		return host.Recording{}, coded(errNoCamera)
	}
	hold, err := r.holds.Authorize(lease, endpointID, holds.KindCamera)
	if err != nil {
		return host.Recording{}, coded(err)
	}
	clip, err := r.camera.Record(ctx, hold.ID, duration)
	if err != nil {
		return host.Recording{}, coded(err)
	}
	return host.Recording{Path: clip.Path, Codec: clip.Codec, FrameRate: clip.FrameRate}, nil
}

// LoginCheck returns the check holds run before opening: the stored
// credentials must carry a refresh token, because a hold outlives one access
// token and a bare PORTOS_ACCESS_TOKEN cannot be renewed.
func LoginCheck(storage auth.TokenStorage) holds.LoginCheck {
	return func(context.Context) error {
		creds, err := storage.LoadCredentials()
		if err != nil || creds == nil || creds.RefreshToken == "" {
			return &holds.Error{
				Code:    holds.CodeHoldRequiresRefreshableLogin,
				Message: "a hold needs a login that can refresh itself; run port auth login",
			}
		}
		return nil
	}
}

// holdAdapter serves holds and motion as the host's HoldService.
type holdAdapter struct {
	holds  holds.Service
	motion motion.Service
	camera camera.Service
}

var _ host.HoldService = (*holdAdapter)(nil)

func (a *holdAdapter) status(h holds.Hold) host.HoldStatus {
	kinds := make([]string, len(h.Kinds))
	hasMotion, hasCamera := false, false
	for i, k := range h.Kinds {
		kinds[i] = string(k)
		hasMotion = hasMotion || k == holds.KindMotion
		hasCamera = hasCamera || k == holds.KindCamera
	}
	out := host.HoldStatus{
		ID:             h.ID,
		EndpointID:     h.EndpointID,
		Kinds:          kinds,
		State:          string(h.State),
		OpenedAt:       h.OpenedAt,
		LastCommandAt:  h.LastCommandAt,
		DeadManSeconds: int(h.DeadMan / time.Second),
	}
	if h.Ending != nil {
		out.Ending = &host.EndingStatus{Reason: string(h.Ending.Reason), At: h.Ending.At, Detail: h.Ending.Detail}
	}
	if hasMotion && h.State != holds.StateEnded {
		st := a.motion.Status(h.ID)
		out.Motion = &host.MotionStatus{
			SeqNum: st.SeqNum, LastMoveAt: st.LastMoveAt, Pose: st.LastPose,
			KeepaliveMode: st.KeepaliveMode, Pending: st.Pending,
		}
	}
	if hasCamera && a.camera != nil && h.State != holds.StateEnded {
		if st, ok := a.camera.Status(h.ID); ok {
			out.Camera = &host.CameraStatus{
				Live: st.Live, OpenedAt: st.OpenedAt, RenewsAt: st.RenewsAt, Renewals: st.Renewals,
				LastFrameAt: st.LastFrame, LostReason: st.LostReason,
			}
		}
	}
	return out
}

func (a *holdAdapter) List() []host.HoldStatus {
	all := a.holds.List()
	out := make([]host.HoldStatus, len(all))
	for i, h := range all {
		out[i] = a.status(h)
	}
	return out
}

func (a *holdAdapter) Open(ctx context.Context, req host.OpenHoldRequest) (host.OpenedHold, error) {
	kinds := make([]holds.Kind, len(req.Kinds))
	for i, k := range req.Kinds {
		kinds[i] = holds.Kind(k)
	}
	h, err := a.holds.Open(ctx, req.EndpointID, kinds, time.Duration(req.DeadManSeconds)*time.Second)
	if err != nil {
		return host.OpenedHold{}, coded(err)
	}
	return host.OpenedHold{HoldStatus: a.status(h), Lease: h.Lease}, nil
}

func (a *holdAdapter) Get(id string) (host.HoldStatus, error) {
	h, err := a.holds.Get(id)
	if err != nil {
		return host.HoldStatus{}, &host.CodedError{Code: host.CodeHoldNotFound, Message: err.Error()}
	}
	return a.status(h), nil
}

func (a *holdAdapter) Release(ctx context.Context, id string) error {
	if _, err := a.holds.Get(id); err != nil {
		return &host.CodedError{Code: host.CodeHoldNotFound, Message: err.Error()}
	}
	return coded(a.holds.Release(ctx, id, holds.ReasonReleased, ""))
}

// coded turns a typed hold error into the host's coded error.
func coded(err error) error {
	var holdErr *holds.Error
	if errors.As(err, &holdErr) {
		return &host.CodedError{Code: holdErr.Code, Message: holdErr.Message}
	}
	return err
}
