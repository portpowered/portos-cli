// Package host is the local long-lived session host: one per config dir,
// listening on a unix socket, keeping all state in memory.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
)

// StateEnded is the HoldStatus.State of a hold that is over. Ended holds stay
// listed so their ending can be read, but they do not keep the host alive.
const StateEnded = "ended"

// releaseTimeout bounds how long DELETE /holds/{id} waits for the sessions to
// close (the robot is sent home within it).
const releaseTimeout = 60 * time.Second

// openTimeout bounds how long POST /holds waits for the sessions to open.
const openTimeout = 60 * time.Second

// LeaseHeader carries the hold lease on POST /message-requests.
const LeaseHeader = "Portos-Lease"

// Headers of a POST /recordings reply, whose body is the raw video.
const (
	VideoCodecHeader = "Portos-Video-Codec"
	FrameRateHeader  = "Portos-Frame-Rate"
)

// MaxRecordingSeconds is the longest recording POST /recordings takes.
const MaxRecordingSeconds = 600

// Host error codes the routes themselves produce.
const (
	CodeInvalidRequest = "invalid_request"
	CodeHostClosing    = "host_closing"
	CodeHoldNotFound   = "hold_not_found"
	CodeHoldFailed     = "hold_failed"
	CodeDispatchFailed = "dispatch_failed"
)

// EndingStatus is why and when a hold ended.
type EndingStatus struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

// MotionStatus is the motion session of a hold.
type MotionStatus struct {
	SeqNum        int                                `json:"seqNum"`
	LastMoveAt    time.Time                          `json:"lastMoveAt"`
	Pose          *api.SpatialInterfacePoseAttribute `json:"pose"`
	KeepaliveMode string                             `json:"keepaliveMode"`
	Pending       int                                `json:"pending"`
}

// CameraStatus is a hold's camera: whether its stream is live, when the host
// renews it, and why it stopped if it did.
type CameraStatus struct {
	Live        bool       `json:"live"`
	OpenedAt    time.Time  `json:"openedAt"`
	RenewsAt    time.Time  `json:"renewsAt"`
	Renewals    int        `json:"renewals"`
	LastFrameAt *time.Time `json:"lastFrameAt,omitempty"`
	LostReason  string     `json:"lostReason,omitempty"`
}

// HoldStatus is the JSON shape of one hold. It never carries the lease.
type HoldStatus struct {
	ID             string        `json:"id"`
	EndpointID     string        `json:"endpointId"`
	Kinds          []string      `json:"kinds"`
	State          string        `json:"state"`
	OpenedAt       time.Time     `json:"openedAt"`
	LastCommandAt  time.Time     `json:"lastCommandAt"`
	DeadManSeconds int           `json:"deadManSeconds"`
	Ending         *EndingStatus `json:"ending,omitempty"`
	Motion         *MotionStatus `json:"motion,omitempty"`
	Camera         *CameraStatus `json:"camera,omitempty"`
}

// OpenedHold is the reply to POST /holds: the hold and the lease that proves
// ownership of it. The lease is returned only here.
type OpenedHold struct {
	HoldStatus
	Lease string `json:"lease"`
}

// OpenHoldRequest is the body of POST /holds.
type OpenHoldRequest struct {
	EndpointID     string   `json:"endpointId"`
	Kinds          []string `json:"kinds"`
	DeadManSeconds int      `json:"deadManSeconds,omitempty"`
}

// RecordRequest is the body of POST /recordings.
type RecordRequest struct {
	EndpointID      string  `json:"endpointId"`
	DurationSeconds float64 `json:"durationSeconds"`
}

// Recording is video recorded from a held camera, at Path on the host's disk: raw H264 or IVF
// to be played at FrameRate. The route sends it as the reply body and removes it.
type Recording struct {
	Path      string
	Codec     string
	FrameRate float64
}

// RecordingService records a held camera under a hold's lease.
type RecordingService interface {
	Record(ctx context.Context, lease, endpointID string, duration time.Duration) (Recording, error)
}

// ListHoldsResponse is the reply to GET /holds.
type ListHoldsResponse struct {
	Holds []HoldStatus `json:"holds"`
}

// StatusResponse is the body of GET /status.
type StatusResponse struct {
	Version   string       `json:"version"`
	Pid       int          `json:"pid"`
	StartedAt time.Time    `json:"startedAt"`
	Holds     []HoldStatus `json:"holds"`
}

// ErrorBody is the JSON error every route except /message-requests replies with.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CodedError is an error with a stable machine code; routes reply with it.
type CodedError struct {
	Code    string
	Message string
}

// Error implements error.
func (e *CodedError) Error() string { return e.Code + ": " + e.Message }

// HoldService is the hold side of the host. The hostservices package adapts the
// holds and motion services to it.
type HoldService interface {
	// List returns every hold, ended ones included.
	List() []HoldStatus
	Open(ctx context.Context, req OpenHoldRequest) (OpenedHold, error)
	Get(id string) (HoldStatus, error)
	Release(ctx context.Context, id string) error
}

// MessageService answers capability messages sent under a hold's lease.
type MessageService interface {
	Dispatch(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error)
}

// Services are the in-memory services the host serves. A nil field means that
// part is not available: no holds are listed and its routes return 404.
type Services struct {
	Holds      HoldService
	Messages   MessageService
	Recordings RecordingService
}

func (s Services) holds() []HoldStatus {
	if s.Holds == nil {
		return []HoldStatus{}
	}
	if holds := s.Holds.List(); holds != nil {
		return holds
	}
	return []HoldStatus{}
}

// activeHolds counts the holds that are not over.
func (s Services) activeHolds() int {
	n := 0
	for _, h := range s.holds() {
		if h.State != StateEnded {
			n++
		}
	}
	return n
}

func newHandler(version string, started time.Time, services Services, g *gate) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, StatusResponse{
			Version:   version,
			Pid:       os.Getpid(),
			StartedAt: started,
			Holds:     services.holds(),
		})
	})
	if services.Holds != nil {
		mountHolds(mux, services.Holds, g)
	}
	if services.Messages != nil {
		mountMessages(mux, services.Messages)
	}
	if services.Recordings != nil {
		mountRecordings(mux, services.Recordings)
	}
	return mux
}

func mountHolds(mux *http.ServeMux, holds HoldService, g *gate) {
	mux.HandleFunc("POST /holds", func(w http.ResponseWriter, r *http.Request) {
		// The idle exit and this route must not race: once the host starts
		// closing it refuses, and the client starts a fresh host.
		if !g.enter() {
			writeError(w, http.StatusServiceUnavailable, CodeHostClosing, "the session host is shutting down; retry")
			return
		}
		defer g.leave()
		var req OpenHoldRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EndpointID == "" || len(req.Kinds) == 0 || req.DeadManSeconds < 0 {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "a hold needs endpointId, kinds and a non-negative deadManSeconds")
			return
		}
		// Opening a session outlives the caller: once start-session is sent the
		// robot is in remote control, so the open must run to its end on a
		// context the caller's interrupt cannot cancel.
		openCtx, cancelOpen := context.WithTimeout(context.WithoutCancel(r.Context()), openTimeout)
		defer cancelOpen()
		opened, err := holds.Open(openCtx, req)
		if err != nil {
			writeHoldError(w, err)
			return
		}
		if r.Context().Err() != nil {
			// The caller is gone and never saw the lease: end the hold now so
			// the session is closed and the robot docked rather than left open.
			releaseCtx, cancelRelease := context.WithTimeout(context.WithoutCancel(r.Context()), releaseTimeout)
			defer cancelRelease()
			_ = holds.Release(releaseCtx, opened.ID)
			return
		}
		writeJSON(w, http.StatusCreated, opened)
	})
	mux.HandleFunc("GET /holds", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, ListHoldsResponse{Holds: holds.List()})
	})
	mux.HandleFunc("GET /holds/{id}", func(w http.ResponseWriter, r *http.Request) {
		hold, err := holds.Get(r.PathValue("id"))
		if err != nil {
			writeHoldError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, hold)
	})
	mux.HandleFunc("DELETE /holds/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// Sending the robot home outlives the caller, so detach from its
		// connection and bound the wait.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), releaseTimeout)
		defer cancel()
		releaseErr := holds.Release(ctx, id)
		var coded *CodedError
		if errors.As(releaseErr, &coded) {
			writeHoldError(w, releaseErr)
			return
		}
		hold, err := holds.Get(id)
		if err != nil {
			writeHoldError(w, err)
			return
		}
		if releaseErr != nil {
			// The hold is over, but a session did not close cleanly: say so.
			writeError(w, http.StatusBadGateway, CodeHoldFailed, releaseErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, hold)
	})
}

func mountMessages(mux *http.ServeMux, messages MessageService) {
	mux.HandleFunc("POST /message-requests", func(w http.ResponseWriter, r *http.Request) {
		var req api.CreateMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "the body must be a message request")
			return
		}
		reply, err := messages.Dispatch(r.Context(), r.Header.Get(LeaseHeader), &req)
		if err != nil {
			writeError(w, http.StatusBadGateway, CodeDispatchFailed, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, reply)
	})
}

// mountRecordings serves POST /recordings: the held camera's video for the requested duration,
// as raw H264 or IVF with its codec and frame rate in headers. It replies once the recording ends.
func mountRecordings(mux *http.ServeMux, recordings RecordingService) {
	mux.HandleFunc("POST /recordings", func(w http.ResponseWriter, r *http.Request) {
		var req RecordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.EndpointID == "" ||
			req.DurationSeconds <= 0 || req.DurationSeconds > MaxRecordingSeconds {
			writeError(w, http.StatusBadRequest, CodeInvalidRequest, "a recording needs endpointId and durationSeconds between 0 and 600")
			return
		}
		duration := time.Duration(req.DurationSeconds * float64(time.Second))
		clip, err := recordings.Record(r.Context(), r.Header.Get(LeaseHeader), req.EndpointID, duration)
		if err != nil {
			writeHoldError(w, err)
			return
		}
		defer func() { _ = os.Remove(clip.Path) }()
		file, err := os.Open(clip.Path)
		if err != nil {
			writeError(w, http.StatusInternalServerError, CodeHoldFailed, "read the recording: "+err.Error())
			return
		}
		defer func() { _ = file.Close() }()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set(VideoCodecHeader, clip.Codec)
		w.Header().Set(FrameRateHeader, strconv.FormatFloat(clip.FrameRate, 'f', -1, 64))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, file)
	})
}

func writeHoldError(w http.ResponseWriter, err error) {
	var coded *CodedError
	if errors.As(err, &coded) {
		status := http.StatusConflict
		switch coded.Code {
		case CodeHoldNotFound:
			status = http.StatusNotFound
		case CodeInvalidRequest:
			status = http.StatusBadRequest
		}
		writeError(w, status, coded.Code, coded.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, CodeHoldFailed, err.Error())
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorBody{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
