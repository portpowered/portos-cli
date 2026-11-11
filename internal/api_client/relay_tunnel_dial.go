package api

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxUpgradeHeaderBytes bounds the upgrade response header that upgradeResponseConn reads itself.
const maxUpgradeHeaderBytes = 16 << 10

// proxyFunc names the HTTP proxy for a request, like http.ProxyFromEnvironment.
type proxyFunc func(*http.Request) (*url.URL, error)

func (r *RelayClientImpl) proxyFor(target *url.URL) (*url.URL, error) {
	proxy := r.proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	return proxy(&http.Request{Method: http.MethodGet, URL: target, Header: http.Header{}})
}

// dialTunnel opens the connection the tunnel WebSocket runs over: through the API's HTTP proxy when
// there is one, with TLS when the API is https. The WebSocket library never sees the proxy, so
// it cannot mistake the proxy's rewrite of the upgrade response for a refusal (upgradeResponseConn).
func (r *RelayClientImpl) dialTunnel(ctx context.Context, target *url.URL, addr string) (net.Conn, error) {
	proxyURL, err := r.proxyFor(target)
	if err != nil {
		return nil, fmt.Errorf("relay tunnel proxy: %w", err)
	}
	var conn net.Conn
	if proxyURL == nil {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialConnect(ctx, proxyURL, addr)
	}
	if err != nil {
		return nil, err
	}
	if target.Scheme == "https" {
		config := &tls.Config{MinVersion: tls.VersionTLS12}
		if r.tlsConfig != nil {
			config = r.tlsConfig.Clone()
		}
		if config.ServerName == "" {
			config.ServerName = target.Hostname()
		}
		config.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, config)
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("relay tunnel TLS: %w", err)
		}
		conn = secure
	}
	return &upgradeResponseConn{Conn: conn, reader: bufio.NewReader(conn)}, nil
}

// dialConnect opens a CONNECT tunnel to addr through an http or https proxy, sending the proxy
// URL's credentials as basic Proxy-Authorization.
func dialConnect(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
	proxyAddr := proxyURL.Host
	if proxyURL.Port() == "" {
		port := "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
		proxyAddr = net.JoinHostPort(proxyURL.Hostname(), port)
	}
	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		return nil, fmt.Errorf("relay tunnel: unsupported proxy scheme %q", proxyURL.Scheme)
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("relay tunnel proxy: %w", err)
	}
	if proxyURL.Scheme == "https" {
		secure := tls.Client(conn, &tls.Config{ServerName: proxyURL.Hostname(), MinVersion: tls.VersionTLS12})
		if err := secure.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("relay tunnel proxy TLS: %w", err)
		}
		conn = secure
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: addr}, Host: addr, Header: http.Header{}}
	if user := proxyURL.User; user != nil {
		password, _ := user.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(user.Username() + ":" + password))
		request.Header.Set("Proxy-Authorization", "Basic "+credentials)
	}
	if err := request.Write(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("relay tunnel proxy: %w", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("relay tunnel proxy: %w", err)
	}
	// A CONNECT 200 has no body; the bytes after it are the tunnel's, so the body is never drained.
	if response.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("relay tunnel proxy refused CONNECT: %s", response.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// bufferedConn reads what a bufio.Reader already holds before the rest of the connection.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

// upgradeResponseConn drops the "close" token from the Connection header of the first response it
// reads, the WebSocket upgrade. A TLS-intercepting egress proxy (Muse's) relays the API's 101 as
// "Connection: upgrade, close"; net/http then deletes the whole Connection header, and the
// WebSocket library refuses the handshake although the proxy keeps the stream open.
type upgradeResponseConn struct {
	net.Conn
	reader     *bufio.Reader
	headerRead bool
	pending    []byte
}

func (c *upgradeResponseConn) Read(p []byte) (int, error) {
	if !c.headerRead {
		header, err := readHeaderBlock(c.reader)
		if err != nil {
			return 0, err
		}
		c.pending = dropCloseToken(header)
		c.headerRead = true
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		return n, nil
	}
	return c.reader.Read(p)
}

// readHeaderBlock reads a response's status line and headers, through the blank line.
func readHeaderBlock(reader *bufio.Reader) ([]byte, error) {
	var header []byte
	for !bytes.HasSuffix(header, []byte("\r\n\r\n")) && !bytes.HasSuffix(header, []byte("\n\n")) {
		if len(header) >= maxUpgradeHeaderBytes {
			return nil, errors.New("relay tunnel: upgrade response header too large")
		}
		b, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		header = append(header, b)
	}
	return header, nil
}

// dropCloseToken removes "close" from every Connection header line, dropping a line left empty.
func dropCloseToken(header []byte) []byte {
	lines := strings.SplitAfter(string(header), "\n")
	var out strings.Builder
	for _, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		name, value, found := strings.Cut(body, ":")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "connection") {
			out.WriteString(line)
			continue
		}
		var kept []string
		for _, token := range strings.Split(value, ",") {
			if token = strings.TrimSpace(token); token != "" && !strings.EqualFold(token, "close") {
				kept = append(kept, token)
			}
		}
		if len(kept) > 0 {
			out.WriteString(name + ": " + strings.Join(kept, ", ") + line[len(body):])
		}
	}
	return []byte(out.String())
}
