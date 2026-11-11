// Package hostclient talks to the local session host over its unix socket.
package hostclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/host"
)

var (
	// ErrNotRunning means no session host is listening for the config dir.
	ErrNotRunning = errors.New("session host is not running")
	// ErrTransport means the host was reached but the exchange failed (a
	// timeout, a reset, a hang-up). Its state is unknown, so callers must not
	// treat it as "no host".
	ErrTransport = errors.New("session host exchange failed")
	// ErrRouteNotServed means the running host does not serve the route yet.
	ErrRouteNotServed = errors.New("session host does not serve this route")
)

// Routes of the host.
const (
	pathStatus   = "/status"
	pathHolds    = "/holds"
	pathMessages = "/message-requests"
	pathRecord   = "/recordings"

	// LeaseHeader carries the hold lease on POST /message-requests.
	LeaseHeader = host.LeaseHeader

	hostName = "http://host"
	network  = "unix"
)

// Status is the body of GET /status.
type Status = host.StatusResponse

// Hold is one hold.
type Hold = host.HoldStatus

// OpenedHold is a freshly opened hold with its lease.
type OpenedHold = host.OpenedHold

// OpenHoldRequest is the body of POST /holds.
type OpenHoldRequest = host.OpenHoldRequest

// Client is an HTTP client bound to the host socket of one config dir.
type Client struct {
	http      *http.Client
	configDir string
}

func newClient(configDir, socketPath string) *Client {
	return &Client{configDir: configDir, http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, socketPath)
		},
	}}}
}

// Dial connects to a running host without starting one.
func Dial(ctx context.Context, configDir string) (*Client, error) {
	socketPath := host.SocketPath(configDir)
	conn, err := (&net.Dialer{}).DialContext(ctx, network, socketPath)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
	}
	_ = conn.Close()
	return newClient(configDir, socketPath), nil
}

// Status returns the host's status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var out Status
	err := c.do(ctx, http.MethodGet, pathStatus, nil, nil, &out)
	return out, err
}

// OpenHold opens a hold. A host that is shutting down refuses with 503; the
// open then waits for that host to go and retries once on a fresh one.
func (c *Client) OpenHold(ctx context.Context, req OpenHoldRequest) (OpenedHold, error) {
	out, err := c.openHold(ctx, req)
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusServiceUnavailable {
		return out, err
	}
	fresh, err := c.restart(ctx)
	if err != nil {
		return OpenedHold{}, err
	}
	return fresh.openHold(ctx, req)
}

func (c *Client) openHold(ctx context.Context, req OpenHoldRequest) (OpenedHold, error) {
	var out OpenedHold
	err := c.do(ctx, http.MethodPost, pathHolds, nil, req, &out)
	return out, err
}

// restart waits for the host this client talks to to drop its socket, then
// returns a client for a running host.
func (c *Client) restart(ctx context.Context) (*Client, error) {
	waitCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if _, err := Dial(waitCtx, c.configDir); err != nil {
			break
		}
		select {
		case <-waitCtx.Done():
			return nil, errors.New("session host is shutting down and did not exit in time")
		case <-ticker.C:
		}
	}
	return EnsureRunning(ctx, c.configDir)
}

// GetHold returns one hold.
func (c *Client) GetHold(ctx context.Context, id string) (Hold, error) {
	var out Hold
	err := c.do(ctx, http.MethodGet, pathHolds+"/"+url.PathEscape(id), nil, nil, &out)
	return out, err
}

// ListHolds lists the holds, ended ones included.
func (c *Client) ListHolds(ctx context.Context) ([]Hold, error) {
	var out host.ListHoldsResponse
	err := c.do(ctx, http.MethodGet, pathHolds, nil, nil, &out)
	return out.Holds, err
}

// ReleaseHold releases a hold and returns it as it ended.
func (c *Client) ReleaseHold(ctx context.Context, id string) (Hold, error) {
	var out Hold
	err := c.do(ctx, http.MethodDelete, pathHolds+"/"+url.PathEscape(id), nil, nil, &out)
	return out, err
}

// Send posts a message request through the host under the given hold lease.
func (c *Client) Send(ctx context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error) {
	var out api.MessageRequest
	header := http.Header{LeaseHeader: []string{lease}}
	if err := c.do(ctx, http.MethodPost, pathMessages, header, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Recording is video the host recorded from a held camera, saved at Path: raw H264 or IVF to
// be played at FrameRate. The caller removes Path.
type Recording = host.Recording

// Record asks the host for the held camera's video for the duration, under the given hold
// lease, and saves it to a temporary file. It returns once the recording ends.
func (c *Client) Record(ctx context.Context, lease, endpointID string, duration time.Duration) (Recording, error) {
	header := http.Header{LeaseHeader: []string{lease}}
	resp, err := c.send(ctx, http.MethodPost, pathRecord, header, host.RecordRequest{EndpointID: endpointID, DurationSeconds: duration.Seconds()})
	if err != nil {
		return Recording{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	rate, err := strconv.ParseFloat(resp.Header.Get(host.FrameRateHeader), 64)
	if err != nil || rate <= 0 {
		return Recording{}, fmt.Errorf("%w: the recording has no frame rate", ErrTransport)
	}
	codec := resp.Header.Get(host.VideoCodecHeader)
	ext := ".h264"
	if codec == "VP8" {
		ext = ".ivf"
	}
	file, err := os.CreateTemp("", "portos-recording-*"+ext)
	if err != nil {
		return Recording{}, err
	}
	_, copyErr := io.Copy(file, resp.Body)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		_ = os.Remove(file.Name())
		return Recording{}, fmt.Errorf("%w: read the recording: %v", ErrTransport, err)
	}
	return Recording{Path: file.Name(), Codec: codec, FrameRate: rate}, nil
}

// StatusError is a non-2xx reply from the host. Code and Message are set when
// the host replied with its JSON error body.
type StatusError struct {
	StatusCode int
	Body       string
	Code       string
	Message    string
}

func (e *StatusError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("session host: %s: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("session host replied %d: %s", e.StatusCode, e.Body)
}

// HoldCode is the host error code; exitcode.Code maps hold_* codes to exit 13.
func (e *StatusError) HoldCode() string { return e.Code }

// Is makes a 404 from a host with no such route match ErrRouteNotServed.
func (e *StatusError) Is(target error) bool {
	return target == ErrRouteNotServed && e.StatusCode == http.StatusNotFound && e.Code == ""
}

func (c *Client) do(ctx context.Context, method, path string, header http.Header, body, out any) error {
	resp, err := c.send(ctx, method, path, header, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// send makes one request and returns a 2xx reply for the caller to read and close; any other
// status is a StatusError.
func (c *Client) send(ctx context.Context, method, path string, header http.Header, body any) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, hostName+path, reader)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if isDialRefusal(err) {
			return nil, fmt.Errorf("%w: %v", ErrNotRunning, err)
		}
		return nil, fmt.Errorf("%w: %v", ErrTransport, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		statusErr := &StatusError{StatusCode: resp.StatusCode, Body: string(bytes.TrimSpace(msg))}
		var body host.ErrorBody
		if json.Unmarshal(msg, &body) == nil {
			statusErr.Code, statusErr.Message = body.Code, body.Message
		}
		return nil, statusErr
	}
	return resp, nil
}

// isDialRefusal reports whether err is a failure to connect at all (no socket
// file, connection refused), as opposed to a failure after connecting.
func isDialRefusal(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) || opErr.Op != "dial" {
		return false
	}
	return !opErr.Timeout() && !errors.Is(err, context.Canceled)
}
