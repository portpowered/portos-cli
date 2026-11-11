package cli

import (
	"errors"
	"os"
	"os/signal"
	"syscall"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/commands"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/portpowered/portos-cli/internal/hostservices"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/portos"
	"github.com/spf13/cobra"
)

// HostCommand is the hidden `port __host` entry point that hostclient spawns
// to run the long-lived session host. It is internal and has no contract entry.
type HostCommand struct {
	configProvider *config.ManagerProvider
	apiClient      apiclient.Client
	tokens         auth.TokenStorage
	recorder       *commands.Recorder
}

// NewHostCommand creates the hidden host command.
func NewHostCommand(configProvider *config.ManagerProvider, apiClient apiclient.Client, tokens auth.TokenStorage, recorder *commands.Recorder) *HostCommand {
	return &HostCommand{configProvider: configProvider, apiClient: apiClient, tokens: tokens, recorder: recorder}
}

// Generate returns the cobra command for the session host.
func (c *HostCommand) Generate() *cobra.Command {
	return &cobra.Command{
		Hidden: true,
		Short:  "Run the local session host (internal)",
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			manager, err := c.configProvider.GetManager()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			wallClock := clock.Real{}
			built := hostservices.New(hostservices.Options{
				Clock:      wallClock,
				Portos:     portos.New(c.apiClient.Messages()),
				State:      portos.NewStateReader(c.apiClient.Endpoints()),
				Keepalive:  keepalive.Default,
				LoginCheck: hostservices.LoginCheck(c.tokens),
				Camera:     &heldCameraOpener{recorder: c.recorder, configProvider: c.configProvider},
			})
			err = host.Run(ctx, host.Options{
				ConfigDir:  manager.ConfigDir(),
				Version:    cmd.Root().Version,
				Clock:      wallClock,
				Services:   built.Services,
				OnPause:    built.OnPause,
				OnShutdown: built.OnShutdown,
			})
			if errors.Is(err, host.ErrAlreadyRunning) {
				return nil // another host won the race; nothing to do
			}
			return err
		},
	}
}
