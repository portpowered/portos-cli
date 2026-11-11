package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	api "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/cli"
)

// errorTransport is an http.RoundTripper that always returns the configured error.
type errorTransport struct {
	err error
}

func (t *errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, t.err
}

// TestSentinelConnectionError_EndToEnd verifies the full error flow:
// HTTP client (doRequest) wraps a net/http connection error with ErrConnectionFailed,
// and the error handler (WrapError) produces the correct user-facing message.
func TestSentinelConnectionError_EndToEnd(t *testing.T) {
	const baseURL = "http://localhost:9999"

	transport := &errorTransport{
		err: fmt.Errorf("dial tcp 127.0.0.1:9999: connectex: No connection could be made because the target machine actively refused it"),
	}
	client := api.NewTestClientWithTransport(t, baseURL, transport)

	_, err := client.DoRequest(context.Background(), "GET", "/test")
	if err == nil {
		t.Fatal("expected error from doRequest, got nil")
	}

	// Verify the HTTP client layer wrapped the error with the sentinel
	if !errors.Is(err, api.ErrConnectionFailed) {
		t.Fatalf("expected errors.Is(err, ErrConnectionFailed) to be true after doRequest; err = %v", err)
	}

	// Pass through the error handler — this is the integration point
	userErr := cli.WrapError(err, baseURL)
	if userErr == nil {
		t.Fatal("WrapError returned nil")
	}

	msg := userErr.Error()
	for _, want := range []string{"connection failed", baseURL, "PORTOS_API_BASE_URL", "port init"} {
		if !strings.Contains(msg, want) {
			t.Errorf("WrapError() = %q, want substring %q", msg, want)
		}
	}
}

// TestSentinelInvalidURL_EndToEnd verifies the full error flow for protocol errors:
// HTTP client wraps an "unsupported protocol scheme" error with ErrInvalidURL,
// and WrapError produces the correct user-facing invalid URL message.
func TestSentinelInvalidURL_EndToEnd(t *testing.T) {
	const baseURL = "localhost:8080"

	transport := &errorTransport{
		err: fmt.Errorf(`unsupported protocol scheme "localhost"`),
	}
	client := api.NewTestClientWithTransport(t, baseURL, transport)

	_, err := client.DoRequest(context.Background(), "GET", "/test")
	if err == nil {
		t.Fatal("expected error from doRequest, got nil")
	}

	// Verify the HTTP client layer wrapped the error with the sentinel
	if !errors.Is(err, api.ErrInvalidURL) {
		t.Fatalf("expected errors.Is(err, ErrInvalidURL) to be true after doRequest; err = %v", err)
	}

	// Pass through the error handler
	userErr := cli.WrapError(err, baseURL)
	if userErr == nil {
		t.Fatal("WrapError returned nil")
	}

	msg := userErr.Error()
	for _, want := range []string{"invalid API base URL", fmt.Sprintf("%q", baseURL), "PORTOS_API_BASE_URL", "port init"} {
		if !strings.Contains(msg, want) {
			t.Errorf("WrapError() = %q, want substring %q", msg, want)
		}
	}
}
