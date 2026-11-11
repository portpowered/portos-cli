package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/portpowered/portos-cli/internal/auth"
)

const tunnelHandshakeTimeout = 15 * time.Second

// Proxied reports whether requests to the API go through an HTTP proxy.
func (r *RelayClientImpl) Proxied() bool {
	if err := r.client.ensureInited(); err != nil {
		return false
	}
	request, err := http.NewRequest(http.MethodGet, r.client.baseURL+"/relay-tunnel", nil)
	if err != nil {
		return false
	}
	proxyURL, err := http.ProxyFromEnvironment(request)
	return err == nil && proxyURL != nil
}

// Tunnel opens GET /relay-tunnel as a WebSocket and returns it as a byte stream to the relay.
func (r *RelayClientImpl) Tunnel(ctx context.Context) (net.Conn, error) {
	if err := r.client.ensureInited(); err != nil {
		return nil, &SetupError{Message: "failed to load config: " + err.Error(), Err: err}
	}
	token, err := r.client.authService.GetAccessToken(ctx)
	if err != nil {
		return nil, NewSetupErrorWrapping(err)
	}
	if token == "" {
		return nil, &SetupError{Message: "no access token — authenticate with \"port auth login\", \"port auth set-token <token>\", or PORTOS_ACCESS_TOKEN", Err: auth.ErrNoCredentials}
	}
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: tunnelHandshakeTimeout}
	url := "ws" + strings.TrimPrefix(r.client.baseURL, "http") + "/relay-tunnel"
	socket, response, err := dialer.DialContext(ctx, url, http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
			_ = response.Body.Close()
			return nil, fmt.Errorf("relay tunnel refused: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("relay tunnel: %w", err)
	}
	return NewTunnelConn(socket), nil
}

// TunnelConn is a net.Conn over a relay tunnel WebSocket: writes go out as binary messages and
// reads return the concatenated binary messages, since TURN over TCP frames itself.
type TunnelConn struct {
	socket *websocket.Conn

	readMu  sync.Mutex
	reader  io.Reader
	writeMu sync.Mutex
	close   sync.Once
}

// NewTunnelConn wraps an open tunnel socket.
func NewTunnelConn(socket *websocket.Conn) *TunnelConn {
	return &TunnelConn{socket: socket}
}

// Read reads the relay's byte stream. The tunnel's close is io.EOF.
func (c *TunnelConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if c.reader == nil {
			kind, reader, err := c.socket.NextReader()
			if err != nil {
				var closeErr *websocket.CloseError
				if errors.As(err, &closeErr) {
					return 0, io.EOF
				}
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, fmt.Errorf("relay tunnel sent a non-binary message")
			}
			c.reader = reader
		}
		n, err := c.reader.Read(p)
		if errors.Is(err, io.EOF) {
			c.reader = nil
			if n == 0 {
				continue
			}
			err = nil
		}
		return n, err
	}
}

// Write sends p to the relay as one binary message.
func (c *TunnelConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.socket.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close ends the tunnel.
func (c *TunnelConn) Close() error {
	var err error
	c.close.Do(func() {
		c.writeMu.Lock()
		_ = c.socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		c.writeMu.Unlock()
		err = c.socket.Close()
	})
	return err
}

// LocalAddr is the TCP address of the socket to the API or proxy. Pion's ICE agent requires a
// *net.TCPAddr from a proxy dialer's connection.
func (c *TunnelConn) LocalAddr() net.Addr {
	if addr, ok := c.socket.LocalAddr().(*net.TCPAddr); ok {
		return addr
	}
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}

// RemoteAddr is the address of the API or proxy the tunnel runs through.
func (c *TunnelConn) RemoteAddr() net.Addr { return c.socket.RemoteAddr() }

// SetDeadline sets the read and write deadlines.
func (c *TunnelConn) SetDeadline(t time.Time) error {
	return errors.Join(c.socket.SetReadDeadline(t), c.socket.SetWriteDeadline(t))
}

// SetReadDeadline sets the read deadline.
func (c *TunnelConn) SetReadDeadline(t time.Time) error { return c.socket.SetReadDeadline(t) }

// SetWriteDeadline sets the write deadline.
func (c *TunnelConn) SetWriteDeadline(t time.Time) error { return c.socket.SetWriteDeadline(t) }
