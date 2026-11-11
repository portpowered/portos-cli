package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// errorTransport is an http.RoundTripper that always returns the configured error.
type errorTransport struct {
	err error
}

func (t *errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// newTestClientWithTransport builds a ClientImpl using a custom transport,
// reusing the shared stubAuthService and logger helpers from the redaction tests.
func newTestClientWithTransport(t *testing.T, transport http.RoundTripper) *ClientImpl {
	t.Helper()

	client, _ := newTestClient(t, "http://localhost:9999")
	client.httpClient = &http.Client{Transport: transport}
	return client
}

func TestDoRequest_WrapsConnectionRefused(t *testing.T) {
	transport := &errorTransport{
		err: fmt.Errorf("dial tcp 127.0.0.1:8080: connection refused"),
	}
	client := newTestClientWithTransport(t, transport)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("expected errors.Is(err, ErrConnectionFailed) to be true, got false; err = %v", err)
	}
	if errors.Is(err, ErrInvalidURL) {
		t.Error("errors.Is(err, ErrInvalidURL) should be false for a connection refused error")
	}
}

func TestDoRequest_WrapsNoSuchHost(t *testing.T) {
	transport := &errorTransport{
		err: fmt.Errorf("dial tcp: lookup nonexistent.example.com: no such host"),
	}
	client := newTestClientWithTransport(t, transport)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrConnectionFailed) {
		t.Errorf("expected errors.Is(err, ErrConnectionFailed) to be true; err = %v", err)
	}
}

func TestDoRequest_WrapsUnsupportedProtocolScheme(t *testing.T) {
	transport := &errorTransport{
		err: fmt.Errorf("Get \"ftp://localhost/test\": unsupported protocol scheme \"ftp\""),
	}
	client := newTestClientWithTransport(t, transport)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrInvalidURL) {
		t.Errorf("expected errors.Is(err, ErrInvalidURL) to be true; err = %v", err)
	}
	if errors.Is(err, ErrConnectionFailed) {
		t.Error("errors.Is(err, ErrConnectionFailed) should be false for a protocol scheme error")
	}
}

func TestDoRequest_GenericErrorNotWrappedAsSentinel(t *testing.T) {
	transport := &errorTransport{
		err: fmt.Errorf("some unexpected transport error"),
	}
	client := newTestClientWithTransport(t, transport)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, ErrConnectionFailed) {
		t.Error("generic error should not match ErrConnectionFailed")
	}
	if errors.Is(err, ErrInvalidURL) {
		t.Error("generic error should not match ErrInvalidURL")
	}
	// Should still be wrapped with "request failed:"
	if err.Error() == "" {
		t.Error("error message should not be empty")
	}
}

func TestDoRequest_PreservesOriginalErrorMessage(t *testing.T) {
	originalSubstring := "no route to host"
	transport := &errorTransport{
		err: errors.New("dial tcp 10.0.0.1:443: " + originalSubstring),
	}
	client := newTestClientWithTransport(t, transport)

	_, err := client.doRequest(context.Background(), "GET", "/test", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// http.Client.Do() wraps the transport error with the request URL,
	// and our wrapConnectionError preserves that full message.
	if got := err.Error(); !strings.Contains(got, originalSubstring) {
		t.Errorf("expected error message to contain %q, got %q", originalSubstring, got)
	}
}
