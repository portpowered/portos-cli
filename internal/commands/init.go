package commands

import (
	"fmt"
	"path/filepath"

	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"go.uber.org/zap"
)

// InitCommand handles the `port init` command
type InitCommand struct {
	configProvider *config.ManagerProvider
	formatter      output.Formatter
	logger         *logging.LoggerRef
}

// NewInitCommand creates a new init command
func NewInitCommand(configProvider *config.ManagerProvider, formatter output.Formatter, logger *logging.LoggerRef) *InitCommand {
	return &InitCommand{
		configProvider: configProvider,
		formatter:      formatter,
		logger:         logger,
	}
}

// InitResult represents the result of the init command
type InitResult struct {
	ConfigDir   string `json:"config_dir"`
	ConfigFile  string `json:"config_file"`
	Initialized bool   `json:"initialized"`
	Message     string `json:"message,omitempty"`
}

const authNextSteps = "Next: authenticate with 'port auth set-token <token>' or 'port auth login'"

// Run executes the init command. If apiURL is non-empty it is used as the
// configured API base URL; otherwise the default is used. If authURL is
// non-empty it is written as the auth base URL; otherwise it is omitted
// from the config (falling back to the API base URL at load time).
func (c *InitCommand) Run(apiURL, authURL string) error {
	manager, err := c.configProvider.GetManager()
	if err != nil {
		return fmt.Errorf("failed to resolve config: %w", err)
	}
	configDir := manager.ConfigDir()
	configPath := filepath.Join(configDir, config.ConfigFileName)

	// Check if config file already exists
	configStorage := config.NewConfigStorage(configPath)
	if configStorage.Exists() {
		// Config already exists
		result := InitResult{
			ConfigDir:   configDir,
			ConfigFile:  configPath,
			Initialized: false,
			Message:     fmt.Sprintf("Configuration already exists at %s", configPath),
		}

		if c.formatter.IsJSON() {
			return c.formatter.Print(result)
		}

		// Human-readable output
		msg := fmt.Sprintf("Configuration already exists at %s\n%s\n", configPath, authNextSteps)
		return c.formatter.Print(msg)
	}

	// Create config directory
	if err := manager.EnsureConfigDir(); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Determine API base URL
	baseURL := config.DefaultAPIBaseURL
	if apiURL != "" {
		baseURL = apiURL
	}

	// Create default config
	defaultCfg := &config.Config{
		APIBaseURL:  baseURL,
		AuthBaseURL: authURL, // empty string omitted from YAML via omitempty
	}

	// Save default config
	if err := manager.SaveConfig(defaultCfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	// Prepare result
	result := InitResult{
		ConfigDir:   configDir,
		ConfigFile:  configPath,
		Initialized: true,
		Message:     fmt.Sprintf("Initialized Port OS CLI configuration at %s", configPath),
	}

	c.logger.Logger().Info("initialized CLI configuration",
		zap.String("config_dir", configDir),
		zap.String("config_file", configPath),
	)

	// Output result
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}

	// Human-readable output
	msg := fmt.Sprintf("Initialized Port OS CLI configuration at %s\n%s\n", configPath, authNextSteps)
	return c.formatter.Print(msg)
}
