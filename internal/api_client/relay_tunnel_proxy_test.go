package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// connectProxy is a minimal HTTP CONNECT proxy that counts the tunnels it opens and checks the
// proxy credentials taken from the proxy URL.
func connectProxy(t *testing.T, connects *atomic.Int32) *url.URL {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") == "" {
			http.Error(w, "CONNECT with credentials only", http.StatusProxyAuthRequired)
			return
		}
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		connects.Add(1)
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
	}))
	t.Cleanup(server.Close)
	proxyURL, _ := url.Parse(server.URL)
	proxyURL.User = url.UserPassword("sandbox", "secret")
	return proxyURL
}

// closingEchoTunnel answers the upgrade the way a TLS-intercepting egress proxy (Muse's) relays it,
// with "close" in the Connection header, then echoes binary messages.
func closingEchoTunnel(t *testing.T) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		socket, err := upgrader.Upgrade(w, r, http.Header{"Connection": {"close"}})
		if err != nil {
			return
		}
		defer socket.Close()
		for {
			kind, message, err := socket.ReadMessage()
			if err != nil || socket.WriteMessage(kind, message) != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestTunnelThroughAProxyAcceptsAnUpgradeThatSaysClose(t *testing.T) {
	server := closingEchoTunnel(t)
	var connects atomic.Int32
	proxyURL := connectProxy(t, &connects)
	client, _ := newTestClient(t, server.URL)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	relay := &RelayClientImpl{
		client:    client,
		proxy:     func(*http.Request) (*url.URL, error) { return proxyURL, nil },
		tlsConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
	}
	conn, err := relay.Tunnel(context.Background())
	if err != nil {
		t.Fatalf("tunnel through the proxy: %v", err)
	}
	defer conn.Close()
	if connects.Load() != 1 {
		t.Fatalf("proxy opened %d tunnels, want 1", connects.Load())
	}
	if _, err := conn.Write([]byte("turn-bytes")); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len("turn-bytes"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "turn-bytes" {
		t.Fatalf("echo = %q, %v", got, err)
	}
}

func TestUpgradeResponseKeepsEverythingButTheCloseToken(t *testing.T) {
	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"sec-websocket-accept: abc=\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: upgrade, close\r\n" +
		"connection: close\r\n" +
		"\r\n" +
		"\x82\x02hi"
	client, server := net.Pipe()
	go func() { _, _ = server.Write([]byte(response)); _ = server.Close() }()
	got, err := io.ReadAll(&upgradeResponseConn{Conn: client, reader: bufio.NewReader(client)})
	if err != nil {
		t.Fatal(err)
	}
	want := "HTTP/1.1 101 Switching Protocols\r\n" +
		"sec-websocket-accept: abc=\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: upgrade\r\n" +
		"\r\n" +
		"\x82\x02hi"
	if string(got) != want {
		t.Fatalf("rewritten response:\n%q\nwant\n%q", got, want)
	}
}

func TestUpgradeResponseRefusesAnEndlessHeader(t *testing.T) {
	client, server := net.Pipe()
	go func() {
		_, _ = server.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nX: " + strings.Repeat("a", maxUpgradeHeaderBytes)))
		_ = server.Close()
	}()
	if _, err := io.ReadAll(&upgradeResponseConn{Conn: client, reader: bufio.NewReader(client)}); err == nil {
		t.Fatal("an upgrade response header past the limit must fail")
	}
}
