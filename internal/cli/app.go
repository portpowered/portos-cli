package cli

import (
	api "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/spf13/cobra"
)

// CLIApp holds the main CLI application dependencies
type CLIApp struct {
	Config    *config.ManagerProvider
	LoggerRef *logging.LoggerRef
	Auth      auth.Service
	APIClient api.Client
	Router    *Router
	Formatter output.Formatter
}

func NewCLIApp(config *config.ManagerProvider, loggerRef *logging.LoggerRef, auth auth.Service, apiClient api.Client, router *Router, formatter output.Formatter) *CLIApp {
	return &CLIApp{
		Config:    config,
		LoggerRef: loggerRef,
		Auth:      auth,
		APIClient: apiClient,
		Router:    router,
		Formatter: formatter,
	}
}

func (c *CLIApp) Generate() *cobra.Command {
	root := c.Router.BuildRoot()

	root.PersistentPreRun = PreRunChain(
		root.PersistentPreRun, // logger setup (from root.go)
	)

	return root
}

// HandleError prints an error using the centralized formatter.
// In JSON mode, the raw error is passed directly so structured fields (status, type)
// are preserved. In human mode, WrapError rewrites known errors into user-facing messages.
func (c *CLIApp) HandleError(err error) {
	if err == nil {
		return
	}
	if c.Formatter.IsJSON() {
		_ = c.Formatter.PrintError(err)
	} else {
		_ = c.Formatter.PrintError(WrapError(err, c.resolveAPIBaseURL()))
	}
}

// resolveAPIBaseURL attempts to read the configured API base URL for use in error messages.
// Returns empty string if config is unavailable (displayURL handles the fallback).
func (c *CLIApp) resolveAPIBaseURL() string {
	mgr, err := c.Config.GetManager()
	if err != nil {
		return ""
	}
	cfg, err := mgr.LoadConfig()
	if err != nil {
		return ""
	}
	return cfg.APIBaseURL
}
