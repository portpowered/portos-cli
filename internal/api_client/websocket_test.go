package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var wsTestUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// newWSTestServer creates an httptest server that upgrades to WebSocket and calls handler.
func newWSTestServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsTestUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
}

func newWSTestClientImpl(t *testing.T, serverURL string) *WebSocketClientImpl {
	t.Helper()
	client, _ := newTestClient(t, serverURL)
	return &WebSocketClientImpl{client: client}
}

func TestWebSocketClient_ConnectAndClose(t *testing.T) {
	server := newWSTestServer(t, func(conn *websocket.Conn) {
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
	defer server.Close()

	ws := newWSTestClientImpl(t, server.URL)

	ctx := context.Background()
	if err := ws.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := ws.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestWebSocketClient_ReadWriteMessage(t *testing.T) {
	server := newWSTestServer(t, func(conn *websocket.Conn) {
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.WriteMessage(mt, msg)
		}
	})
	defer server.Close()

	ws := newWSTestClientImpl(t, server.URL)

	ctx := context.Background()
	if err := ws.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer ws.Close()

	testMsg := []byte("hello websocket")
	if err := ws.WriteMessage(testMsg); err != nil {
		t.Fatalf("WriteMessage failed: %v", err)
	}

	msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if string(msg) != "hello websocket" {
		t.Errorf("expected 'hello websocket', got %q", string(msg))
	}
}

func TestWebSocketClient_ConnectFailsWithEmptyToken(t *testing.T) {
	client := &ClientImpl{
		baseURL:     "http://localhost:1",
		authService: &stubAuthService{token: ""},
	}
	client.initOnce.Do(func() {})

	ws := &WebSocketClientImpl{client: client}
	err := ws.Connect(context.Background())
	if err == nil {
		t.Fatal("expected error when token is empty")
	}
}

func TestWebSocketClient_ReadMessageWhenNotConnected(t *testing.T) {
	ws := &WebSocketClientImpl{client: &ClientImpl{}}

	_, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("expected error when reading from nil connection")
	}
}

func TestWebSocketClient_WriteMessageWhenNotConnected(t *testing.T) {
	ws := &WebSocketClientImpl{client: &ClientImpl{}}

	err := ws.WriteMessage([]byte("test"))
	if err == nil {
		t.Fatal("expected error when writing to nil connection")
	}
}

func TestWebSocketClient_CloseWhenNotConnected(t *testing.T) {
	ws := &WebSocketClientImpl{client: &ClientImpl{}}

	if err := ws.Close(); err != nil {
		t.Fatalf("Close on nil connection should not error, got: %v", err)
	}
}

func TestWebSocketClient_GracefulClose(t *testing.T) {
	closeReceived := make(chan struct{}, 1)

	server := newWSTestServer(t, func(conn *websocket.Conn) {
		conn.SetCloseHandler(func(code int, text string) error {
			select {
			case closeReceived <- struct{}{}:
			default:
			}
			return nil
		})
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	})
	defer server.Close()

	ws := newWSTestClientImpl(t, server.URL)

	if err := ws.Connect(context.Background()); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if err := ws.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	select {
	case <-closeReceived:
		// Good — close frame was sent
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for close frame")
	}
}

func TestIsUnexpectedCloseError(t *testing.T) {
	// Normal close should not be unexpected
	normalErr := &websocket.CloseError{Code: websocket.CloseNormalClosure}
	if IsUnexpectedCloseError(normalErr) {
		t.Error("normal closure should not be unexpected")
	}

	// Abnormal close should be unexpected
	abnormalErr := &websocket.CloseError{Code: websocket.CloseAbnormalClosure}
	if !IsUnexpectedCloseError(abnormalErr) {
		t.Error("abnormal closure should be unexpected")
	}
}
