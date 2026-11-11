package api

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// echoTunnel upgrades GET /relay-tunnel and echoes each binary message, after checking the token.
func echoTunnel(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/relay-tunnel" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, `{"message":"no"}`, http.StatusUnauthorized)
			return
		}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer socket.Close()
		for {
			kind, message, err := socket.ReadMessage()
			if err != nil {
				return
			}
			if socket.WriteMessage(kind, message) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTunnelIsAByteStreamThatOutlivesItsDialContext(t *testing.T) {
	client, _ := newTestClient(t, echoTunnel(t).URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	conn, err := (&RelayClientImpl{client: client}).Tunnel(ctx)
	cancel() // the ICE agent's dial context ends once the conn is returned
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, ok := conn.LocalAddr().(*net.TCPAddr); !ok {
		t.Fatalf("LocalAddr is %T; the ICE proxy dialer requires *net.TCPAddr", conn.LocalAddr())
	}
	want := "\x00\x10stun-frame"
	for _, part := range []string{"\x00\x10", "stun", "-frame"} {
		if _, err := conn.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 3) // smaller than a message, so one message spans several reads
	var got []byte
	for len(got) < len(want) {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	if string(got) != want {
		t.Fatalf("read %q, want %q", got, want)
	}
}

func TestTunnelEndsWithEOFWhenTheServerCloses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		_ = socket.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = socket.Close()
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	conn, err := (&RelayClientImpl{client: client}).Tunnel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Read(make([]byte, 8)); err != io.EOF {
		t.Fatalf("read after the server closed: %v, want io.EOF", err)
	}
}

func TestTunnelReportsARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"the media relay is not configured"}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	_, err := (&RelayClientImpl{client: client}).Tunnel(context.Background())
	if err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want the refusal status and message, got %v", err)
	}
}

func TestLoopbackAPIIsNotProxied(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://proxy.example.test:3128")
	client, _ := newTestClient(t, "http://127.0.0.1:1")
	if (&RelayClientImpl{client: client}).Proxied() {
		t.Fatal("a loopback API is never reached through the proxy")
	}
}
