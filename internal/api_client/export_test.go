package api

import (
	"context"
	"net/http"
	"testing"
)

// NewTestClientWithTransport builds a ClientImpl with a custom HTTP transport
// for use by external test packages (package api_test).
func NewTestClientWithTransport(t *testing.T, baseURL string, transport http.RoundTripper) *ClientImpl {
	t.Helper()
	client := newTestClientWithTransport(t, transport)
	client.baseURL = baseURL
	return client
}

// DoRequest exposes the unexported doRequest method for integration tests.
func (c *ClientImpl) DoRequest(ctx context.Context, method, path string) (*http.Response, error) {
	return c.doRequest(ctx, method, path, nil, nil)
}
