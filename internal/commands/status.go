package commands

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"go.uber.org/zap"
)

// StatusCommand handles the `port status` command
type StatusCommand struct {
	configProvider *config.ManagerProvider
	formatter      output.Formatter
	logger         *logging.LoggerRef
	httpClient     *http.Client
}

// NewStatusCommand creates a new status command
func NewStatusCommand(
	configProvider *config.ManagerProvider,
	formatter output.Formatter,
	logger *logging.LoggerRef,
	httpClient *http.Client,
) *StatusCommand {
	return &StatusCommand{
		configProvider: configProvider,
		formatter:      formatter,
		logger:         logger,
		httpClient:     httpClient,
	}
}

// StatusResult represents the result of the status command
type StatusResult struct {
	APIURL    string `json:"api_url"`
	Reachable bool   `json:"reachable"`
	IsPortOS  bool   `json:"is_portos"`
	Message   string `json:"message"`
}

// Run executes the status command, probing the configured server's /ping endpoint.
func (c *StatusCommand) Run(ctx context.Context) error {
	manager, err := c.configProvider.GetManager()
	if err != nil {
		return fmt.Errorf("failed to resolve config: %w", err)
	}

	cfg, err := manager.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	apiURL := strings.TrimSuffix(cfg.APIBaseURL, "/")
	pingURL := apiURL + "/ping"

	c.logger.Logger().Debug("probing server", zap.String("url", pingURL))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pingURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Server unreachable
		result := StatusResult{
			APIURL:    apiURL,
			Reachable: false,
			IsPortOS:  false,
			Message:   fmt.Sprintf("Cannot reach PortOS backend at %s", apiURL),
		}
		if c.formatter.IsJSON() {
			return c.formatter.Print(result)
		}
		return fmt.Errorf("%s", result.Message)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// /ping returned 200 — this is a PortOS backend
		result := StatusResult{
			APIURL:    apiURL,
			Reachable: true,
			IsPortOS:  true,
			Message:   fmt.Sprintf("Connected to PortOS backend at %s", apiURL),
		}
		if c.formatter.IsJSON() {
			return c.formatter.Print(result)
		}
		return c.formatter.Print(result.Message)
	}

	if resp.StatusCode == http.StatusNotFound {
		// Server is reachable but /ping returns 404 — not a PortOS backend
		result := StatusResult{
			APIURL:    apiURL,
			Reachable: true,
			IsPortOS:  false,
			Message:   fmt.Sprintf("Server at %s is not a PortOS backend", apiURL),
		}
		if c.formatter.IsJSON() {
			return c.formatter.Print(result)
		}
		return fmt.Errorf("%s", result.Message)
	}

	// Other non-200 status codes — reachable but unexpected response
	result := StatusResult{
		APIURL:    apiURL,
		Reachable: true,
		IsPortOS:  false,
		Message:   fmt.Sprintf("Server at %s returned unexpected status %d from /ping", apiURL, resp.StatusCode),
	}
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}
	return fmt.Errorf("%s", result.Message)
}
