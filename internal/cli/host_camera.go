package cli

import (
	"context"
	"fmt"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/services/camera"
)

// heldCameraOpener opens the session host's camera streams with the recorder's
// held peer.
type heldCameraOpener struct {
	recorder       *commands.Recorder
	configProvider *config.ManagerProvider
	opened         atomic.Int64
}

var _ camera.Opener = (*heldCameraOpener)(nil)

func (o *heldCameraOpener) Open(ctx context.Context, endpointID string, lifetime time.Duration) (camera.Stream, error) {
	n := o.opened.Add(1)
	return o.recorder.HoldCamera(ctx, commands.HeldCameraParams{
		EndpointID:    endpointID,
		SessionDomain: o.sessionDomain(),
		SessionID:     fmt.Sprintf("hold-camera-%d-%d", time.Now().UnixNano(), n),
		Lifetime:      lifetime,
	})
}

// sessionDomain is the API host, as the snapshot and record commands use.
func (o *heldCameraOpener) sessionDomain() string {
	manager, err := o.configProvider.GetManager()
	if err != nil {
		return "cli"
	}
	cfg, err := manager.LoadConfig()
	if err != nil || cfg.APIBaseURL == "" {
		return "cli"
	}
	if u, err := url.Parse(cfg.APIBaseURL); err == nil && u.Host != "" {
		return u.Hostname()
	}
	return "cli"
}
