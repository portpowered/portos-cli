package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"go.uber.org/zap"
)

// Hold kinds `--motion` and `--camera` ask for.
const (
	hostKindMotion = "motion"
	hostKindCamera = "camera"
)

// SessionHost is the part of the session host client the session commands use.
type SessionHost interface {
	OpenHold(ctx context.Context, req hostclient.OpenHoldRequest) (hostclient.OpenedHold, error)
	GetHold(ctx context.Context, id string) (hostclient.Hold, error)
	ListHolds(ctx context.Context) ([]hostclient.Hold, error)
	ReleaseHold(ctx context.Context, id string) (hostclient.Hold, error)
}

// SessionCommand handles `port session hold|status|release`.
type SessionCommand struct {
	formatter output.Formatter
	logger    *logging.LoggerRef
	configDir func() (string, error)
	// ensure returns a host client, starting the host when none runs.
	ensure func(ctx context.Context, configDir string) (SessionHost, error)
	// dial returns a host client only when a host already runs.
	dial func(ctx context.Context, configDir string) (SessionHost, error)
}

// NewSessionCommand creates the session command.
func NewSessionCommand(formatter output.Formatter, logger *logging.LoggerRef, configProvider *config.ManagerProvider) *SessionCommand {
	return &SessionCommand{
		formatter: formatter,
		logger:    logger,
		configDir: func() (string, error) {
			manager, err := configProvider.GetManager()
			if err != nil {
				return "", err
			}
			return manager.ConfigDir(), nil
		},
		ensure: func(ctx context.Context, dir string) (SessionHost, error) { return hostclient.EnsureRunning(ctx, dir) },
		dial:   func(ctx context.Context, dir string) (SessionHost, error) { return hostclient.Dial(ctx, dir) },
	}
}

// HoldParams are the parameters of `port session hold`.
type HoldParams struct {
	EndpointID string
	Motion     bool
	Camera     bool
	// Shared attaches the camera hold to the endpoint's shared session. It needs Camera.
	Shared bool
	// Listen receives the camera's microphone so record and play-audio --listen capture it. It
	// needs Camera; without it the camera never sends audio.
	Listen bool
	// Talk gives a shared camera hold a talkback track, so play-audio works under it. It needs
	// Shared; a single-peer camera hold always talks.
	Talk bool
	// DeadMan is how long the hold lives without a command; zero uses the host default.
	DeadMan time.Duration
}

// HoldResult is what `port session hold` prints. Lease is the secret the
// caller passes (--lease or PORTOS_LEASE) to use the hold. Pose is null until
// the hold has a pose from a move.
type HoldResult struct {
	HoldID     string   `json:"holdId"`
	Lease      string   `json:"lease"`
	EndpointID string   `json:"endpointId"`
	Kinds      []string `json:"kinds"`
	Shared     bool     `json:"shared,omitempty"`
	Listening  bool     `json:"listening,omitempty"`
	Talking    bool     `json:"talking,omitempty"`
	// ExpiresAt is when a shared camera session ends; the hold ends with it.
	ExpiresAt *time.Time                         `json:"expiresAt,omitempty"`
	Pose      *api.SpatialInterfacePoseAttribute `json:"pose"`
}

// Hold opens a hold on an endpoint and prints its lease.
func (c *SessionCommand) Hold(ctx context.Context, params HoldParams) error {
	if params.EndpointID == "" {
		return exitcode.NewInvalidArgument(errors.New("endpoint ID is required"))
	}
	var kinds []string
	if params.Motion {
		kinds = append(kinds, hostKindMotion)
	}
	if params.Camera {
		kinds = append(kinds, hostKindCamera)
	}
	if len(kinds) == 0 {
		return exitcode.NewInvalidArgument(errors.New("name what to hold: --motion, --camera or both"))
	}
	if params.Shared && !params.Camera {
		return exitcode.NewInvalidArgument(errors.New("--shared holds the camera in the shared session; add --camera"))
	}
	if params.Shared && params.Motion {
		return exitcode.NewInvalidArgument(errors.New("--shared holds only the camera; open the motion hold without --shared"))
	}
	if params.Listen && !params.Camera {
		return exitcode.NewInvalidArgument(errors.New("--listen receives the camera's microphone; add --camera"))
	}
	if params.Talk && !params.Shared {
		return exitcode.NewInvalidArgument(errors.New("--talk is for a shared camera hold (--camera --shared); a camera hold without --shared always talks"))
	}
	if params.DeadMan < 0 {
		return exitcode.NewInvalidArgument(errors.New("--dead-man must not be negative"))
	}
	dir, err := c.configDir()
	if err != nil {
		return err
	}
	host, err := c.ensure(ctx, dir)
	if err != nil {
		return err
	}
	c.logger.Logger().Info("opening hold", zap.String("endpoint_id", params.EndpointID))
	opened, err := host.OpenHold(ctx, hostclient.OpenHoldRequest{
		EndpointID:     params.EndpointID,
		Kinds:          kinds,
		Shared:         params.Shared,
		Listen:         params.Listen,
		Talk:           params.Talk,
		DeadManSeconds: int(params.DeadMan / time.Second),
	})
	if err != nil {
		return err
	}
	result := HoldResult{HoldID: opened.ID, Lease: opened.Lease, EndpointID: opened.EndpointID, Kinds: opened.Kinds, Shared: params.Shared}
	if opened.Motion != nil {
		result.Pose = opened.Motion.Pose
	}
	if camera := opened.Camera; camera != nil {
		result.Listening, result.Talking, result.ExpiresAt = camera.Listening, camera.Talking, camera.ExpiresAt
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	line := fmt.Sprintf("hold %s on %s (%s)", result.HoldID, result.EndpointID, strings.Join(result.Kinds, ","))
	if result.Listening {
		line += "  listening"
	}
	if result.ExpiresAt != nil {
		line += "  expires " + result.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return c.formatter.Print(fmt.Sprintf("%s\nlease %s\npass it as --lease or PORTOS_LEASE", line, result.Lease))
}

// SessionStatusResult is what `port session status` prints.
type SessionStatusResult struct {
	Holds []hostclient.Hold `json:"holds"`
}

// Status prints the holds, or one hold when id is given. With no host running
// there are no holds.
func (c *SessionCommand) Status(ctx context.Context, id string) error {
	dir, err := c.configDir()
	if err != nil {
		return err
	}
	result := SessionStatusResult{Holds: []hostclient.Hold{}}
	host, err := c.dial(ctx, dir)
	switch {
	case errors.Is(err, hostclient.ErrNotRunning):
		if id != "" {
			return fmt.Errorf("no hold %q: no session host is running", id)
		}
	case err != nil:
		return err
	case id != "":
		hold, err := host.GetHold(ctx, id)
		if err != nil {
			return err
		}
		result.Holds = append(result.Holds, hold)
	default:
		holds, err := host.ListHolds(ctx)
		if err != nil {
			return err
		}
		result.Holds = append(result.Holds, holds...)
	}
	return c.printHolds(result)
}

// Release ends a hold, sending the robot home, and prints how it ended.
func (c *SessionCommand) Release(ctx context.Context, id string) error {
	if id == "" {
		return exitcode.NewInvalidArgument(errors.New("hold ID is required"))
	}
	dir, err := c.configDir()
	if err != nil {
		return err
	}
	host, err := c.dial(ctx, dir)
	if errors.Is(err, hostclient.ErrNotRunning) {
		return fmt.Errorf("no hold %q: no session host is running", id)
	}
	if err != nil {
		return err
	}
	hold, err := host.ReleaseHold(ctx, id)
	if err != nil {
		return err
	}
	return c.printHolds(SessionStatusResult{Holds: []hostclient.Hold{hold}})
}

func (c *SessionCommand) printHolds(result SessionStatusResult) error {
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	if len(result.Holds) == 0 {
		return c.formatter.Print("no holds")
	}
	for _, hold := range result.Holds {
		line := fmt.Sprintf("%s  %s  %s  %s", hold.ID, hold.EndpointID, strings.Join(hold.Kinds, ","), hold.State)
		if hold.Motion != nil {
			line += fmt.Sprintf("  seq %d pending %d keepalive %s", hold.Motion.SeqNum, hold.Motion.Pending, hold.Motion.KeepaliveMode)
		}
		if camera := hold.Camera; camera != nil {
			line += cameraFlags(camera)
		}
		if hold.Ending != nil {
			line += fmt.Sprintf("  ended: %s", hold.Ending.Reason)
			if hold.Ending.Detail != "" {
				line += " (" + hold.Ending.Detail + ")"
			}
		}
		if err := c.formatter.Print(line); err != nil {
			return err
		}
	}
	return nil
}

// cameraFlags is the camera part of a status line: shared, listening, talking and the expiry.
func cameraFlags(camera *hostclient.CameraStatus) string {
	line := ""
	if camera.Shared {
		line += "  shared"
	}
	if camera.Listening {
		line += "  listening"
	}
	if camera.Shared && camera.Talking {
		line += "  talking"
	}
	if camera.ExpiresAt != nil {
		line += "  expires " + camera.ExpiresAt.UTC().Format(time.RFC3339)
	}
	return line
}
