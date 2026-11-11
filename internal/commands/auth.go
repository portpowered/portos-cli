package commands

import (
	"context"
	"fmt"

	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

// AuthCommand handles authentication commands (set-token and login)
type AuthCommand struct {
	authService    auth.Service
	configProvider *config.ManagerProvider
	formatter      output.Formatter
	logger         *logging.LoggerRef
}

// NewAuthCommand creates a new auth command
func NewAuthCommand(authService auth.Service, configProvider *config.ManagerProvider, formatter output.Formatter, logger *logging.LoggerRef) *AuthCommand {
	return &AuthCommand{
		authService:    authService,
		configProvider: configProvider,
		formatter:      formatter,
		logger:         logger,
	}
}

// SetTokenResult represents the result of the set-token command
type SetTokenResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SetTokenParams contains parameters for the SetToken command
type SetTokenParams struct {
	Token string
}

// LoginParams contains parameters for the Login command
type LoginParams struct {
	// ClientID is the OAuth client for the device flow; empty means the CLI client.
	ClientID string
	// Device selects the device authorization flow (RFC 8628): the CLI prints a
	// code to approve on any signed-in browser, so it works where no browser can
	// open on this machine, such as a remote agent sandbox.
	Device bool
}

const postAuthNextSteps = "\nNext: try 'port endpoint list' to see your devices\n"

// SetToken sets an access token directly without going through device auth
func (c *AuthCommand) SetToken(ctx context.Context, params SetTokenParams) error {
	if params.Token == "" {
		return fmt.Errorf("token cannot be empty")
	}

	// Set the token using the auth service
	if err := c.authService.SetToken(ctx, params.Token); err != nil {
		return fmt.Errorf("failed to set token: %w", err)
	}

	result := SetTokenResult{
		Success: true,
		Message: "Token set successfully",
	}

	c.logger.Logger().Info("token set successfully")

	// Output result
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}

	// Human-readable output
	return c.formatter.Print("Token set successfully\n" + postAuthNextSteps)
}

// Login runs the browser-based OAuth 2.0 Authorization Code + PKCE flow, or the
// device authorization flow when params.Device is set.
func (c *AuthCommand) Login(ctx context.Context, params LoginParams) error {
	messageHandler := func(message string) {
		// Suppress intermediate progress messages in JSON mode, except the device
		// flow's code, without which nobody can approve the login.
		if params.Device || !c.formatter.IsJSON() {
			c.formatter.Print(message)
		}
	}

	var err error
	if params.Device {
		err = c.authService.Login(ctx, params.ClientID, messageHandler)
	} else {
		err = c.authService.BrowserLogin(ctx, messageHandler)
	}
	if err != nil {
		return fmt.Errorf("login failed: %w\n\nIf login is not working, you can also use:\n  port auth set-token <token>", err)
	}

	// If we reach here, login was successful
	result := SetTokenResult{
		Success: true,
		Message: "Login successful",
	}

	c.logger.Logger().Info("login successful")

	// Output result
	if c.formatter.IsJSON() {
		return c.formatter.Print(result)
	}

	// Human-readable output - messages were already printed via messageHandler
	return c.formatter.Print(postAuthNextSteps)
}

// Convenience functions for running auth commands
