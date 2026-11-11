package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"go.uber.org/zap"
)

const (
	initialBackoff = 1 * time.Second
	maxBackoff     = 30 * time.Second
)

// ServeCommand handles the serve command business logic
type ServeCommand struct {
	apiClient   apiclient.Client
	formatter   output.Formatter
	authService auth.Service
	logger      *logging.LoggerRef
}

// NewServeCommand creates a new serve command
func NewServeCommand(apiClient apiclient.Client, formatter output.Formatter, authService auth.Service, logger *logging.LoggerRef) *ServeCommand {
	return &ServeCommand{
		apiClient:   apiClient,
		formatter:   formatter,
		authService: authService,
		logger:      logger,
	}
}

// ServeParams contains parameters for the serve command
type ServeParams struct {
	EndpointID string
}

// ServeEvent represents a received WebSocket event for output
type ServeEvent struct {
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// Serve connects to the backend WebSocket and reads incoming events.
// It automatically reconnects with exponential backoff on connection drops.
// It blocks until the context is cancelled (SIGINT/SIGTERM).
func (c *ServeCommand) Serve(ctx context.Context, params ServeParams) error {
	backoff := initialBackoff
	attempt := 0

	for {
		connected, err := c.runConnection(ctx, params)

		// Context cancelled — clean shutdown
		if ctx.Err() != nil {
			c.logger.Logger().Info("shutting down WebSocket connection")
			if !c.formatter.IsJSON() && !c.formatter.IsQuiet() {
				c.formatter.Print("Disconnected.\n")
			}
			return nil
		}

		// Reset backoff after a successful connection that ran for a while
		if connected {
			backoff = initialBackoff
			attempt = 0
		}

		attempt++

		// Connection dropped — attempt reconnection
		if err != nil {
			c.logger.Logger().Warn("WebSocket connection lost, will reconnect",
				zap.Error(err),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))
			if !c.formatter.IsJSON() && !c.formatter.IsQuiet() {
				c.formatter.Print(fmt.Sprintf("Connection lost. Reconnecting in %s (attempt %d)...\n", backoff, attempt))
			}
		} else {
			c.logger.Logger().Info("WebSocket connection closed, will reconnect",
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff))
		}

		// Wait for backoff or context cancellation
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}

		// Exponential backoff
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// runConnection establishes a single WebSocket connection and reads events
// until the connection drops or the context is cancelled. Returns whether
// a connection was successfully established and any error.
func (c *ServeCommand) runConnection(ctx context.Context, params ServeParams) (connected bool, err error) {
	wsClient := c.apiClient.WebSocket()

	c.logger.Logger().Info("connecting to Port OS WebSocket")

	if err := wsClient.Connect(ctx); err != nil {
		return false, fmt.Errorf("failed to connect: %w", err)
	}
	defer wsClient.Close()

	if !c.formatter.IsJSON() && !c.formatter.IsQuiet() {
		c.formatter.Print("Connected to Port OS. Listening for events...\n")
	}
	c.logger.Logger().Info("WebSocket connection established")

	// Start ping loop in background
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	pingErrCh := make(chan error, 1)
	go func() {
		pingErrCh <- wsClient.StartPingLoop(pingCtx)
	}()

	// Read loop
	readErrCh := make(chan error, 1)
	go func() {
		for {
			message, err := wsClient.ReadMessage()
			if err != nil {
				readErrCh <- err
				return
			}
			c.handleMessage(message)
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case err := <-pingErrCh:
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, fmt.Errorf("ping failed: %w", err)
	case err := <-readErrCh:
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		return true, err
	}
}

func (c *ServeCommand) handleMessage(message []byte) {
	if c.formatter.IsJSON() {
		event := ServeEvent{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Message:   json.RawMessage(message),
		}
		c.formatter.Print(event)
	} else {
		c.formatter.Print(string(message) + "\n")
	}
}
