package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/portos-cli/internal/auth"
	"go.uber.org/zap"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// pongWait is the time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// pingPeriod is how often to send pings. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// maxMessageSize is the maximum message size allowed from the peer (8 MB).
	maxMessageSize = 8000000
)

// WebSocketClientImpl provides a WebSocket connection to the Port OS backend.
type WebSocketClientImpl struct {
	client *ClientImpl

	mu   sync.Mutex
	conn *websocket.Conn
}

// Connect establishes a WebSocket connection to the backend's /websockets endpoint.
// The auth token is passed as a query parameter as required by the backend.
func (ws *WebSocketClientImpl) Connect(ctx context.Context) error {
	if err := ws.client.ensureInited(); err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	token, err := ws.client.authService.GetAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get access token: %w", err)
	}
	if token == "" {
		return &SetupError{Message: "no access token — authenticate with \"port auth login\", \"port auth set-token <token>\", or PORTOS_ACCESS_TOKEN", Err: auth.ErrNoCredentials}
	}

	// Convert http(s) URL to ws(s) URL
	wsURL := "ws" + strings.TrimPrefix(ws.client.baseURL, "http") + "/websockets?token=" + token

	ws.client.logger.Logger().Debug("WebSocket connecting", zap.String("url", ws.client.baseURL+"/websockets"))

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("websocket connection failed: %w", err)
	}

	ws.mu.Lock()
	ws.conn = conn
	ws.mu.Unlock()

	// Configure the connection
	conn.SetReadLimit(maxMessageSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	ws.client.logger.Logger().Debug("WebSocket connection established")
	return nil
}

// ReadMessage reads the next message from the WebSocket connection.
// It blocks until a message is received, an error occurs, or the connection is closed.
func (ws *WebSocketClientImpl) ReadMessage() ([]byte, error) {
	ws.mu.Lock()
	conn := ws.conn
	ws.mu.Unlock()

	if conn == nil {
		return nil, fmt.Errorf("websocket not connected")
	}

	_, message, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	return message, nil
}

// WriteMessage sends a text message over the WebSocket connection.
func (ws *WebSocketClientImpl) WriteMessage(message []byte) error {
	ws.mu.Lock()
	conn := ws.conn
	ws.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("websocket not connected")
	}

	conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteMessage(websocket.TextMessage, message)
}

// StartPingLoop sends periodic ping frames to keep the connection alive.
// It blocks until the context is cancelled or a ping write fails.
// Should be called in a separate goroutine.
func (ws *WebSocketClientImpl) StartPingLoop(ctx context.Context) error {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			ws.mu.Lock()
			conn := ws.conn
			ws.mu.Unlock()

			if conn == nil {
				return fmt.Errorf("websocket not connected")
			}

			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return fmt.Errorf("ping failed: %w", err)
			}
		}
	}
}

// Close performs a graceful WebSocket close handshake.
func (ws *WebSocketClientImpl) Close() error {
	ws.mu.Lock()
	conn := ws.conn
	ws.conn = nil
	ws.mu.Unlock()

	if conn == nil {
		return nil
	}

	// Send close frame and wait briefly for acknowledgment
	err := conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(writeWait),
	)
	if err != nil {
		conn.Close()
		return err
	}
	return conn.Close()
}

// IsUnexpectedCloseError returns true if the error is an unexpected WebSocket close
// (not a normal closure or going-away).
func IsUnexpectedCloseError(err error) bool {
	return websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure)
}
