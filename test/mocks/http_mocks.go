package mocks

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MockRoundTripper provides a mock HTTP round tripper for testing
type MockRoundTripper struct {
	RoundTripFunc func(*http.Request) (*http.Response, error)
	LastRequest   *http.Request
	CallCount     int
}

// RoundTrip implements http.RoundTripper interface
func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Track the request
	m.LastRequest = req
	m.CallCount++
	return m.RoundTripFunc(req)
}

// GetLastHTTPRequest returns the last HTTP request made
func (m *MockRoundTripper) GetLastHTTPRequest() *http.Request {
	return m.LastRequest
}

// GetHTTPCallCount returns the number of HTTP requests made
func (m *MockRoundTripper) GetHTTPCallCount() int {
	return m.CallCount
}

// RoutingRoundTripper routes HTTP requests to different mock handlers based on URL
type RoutingRoundTripper struct {
	Routes      map[string]http.RoundTripper
	Default     http.RoundTripper
	LastRequest *http.Request
	CallCount   int
}

// RoundTrip implements http.RoundTripper interface
func (r *RoutingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Track the request
	r.LastRequest = req
	r.CallCount++

	if rt, ok := r.Routes[req.URL.String()]; ok {
		return rt.RoundTrip(req)
	}
	if rt, ok := r.Routes[req.URL.Path]; ok {
		return rt.RoundTrip(req)
	}
	if r.Default != nil {
		return r.Default.RoundTrip(req)
	}
	return nil, fmt.Errorf("no mock for URL: %s", req.URL.String())
}

// GetLastHTTPRequest returns the last HTTP request made
func (r *RoutingRoundTripper) GetLastHTTPRequest() *http.Request {
	return r.LastRequest
}

// GetHTTPCallCount returns the number of HTTP requests made
func (r *RoutingRoundTripper) GetHTTPCallCount() int {
	return r.CallCount
}

// NewMockHTTPClient creates a new HTTP client with the specified mock round tripper
func NewMockHTTPClient(rt http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: rt,
	}
}

// NewRoutingMockClient creates a new HTTP client with URL-based routing
func NewRoutingMockClient(routes map[string]http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: &RoutingRoundTripper{
			Routes: routes,
		},
	}
}

// NewMockRoundTripper creates a mock round tripper that returns the specified response
func NewMockRoundTripper(statusCode int, body string, headers map[string]string) http.RoundTripper {
	return &MockRoundTripper{
		RoundTripFunc: func(req *http.Request) (*http.Response, error) {
			header := make(http.Header)
			for k, v := range headers {
				header.Set(k, v)
			}
			if header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/json")
			}
			return &http.Response{
				StatusCode: statusCode,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     header,
			}, nil
		},
	}
}

// NewJSONMockRoundTripper creates a mock round tripper that returns JSON responses
func NewJSONMockRoundTripper(statusCode int, jsonBody string) http.RoundTripper {
	return NewMockRoundTripper(statusCode, jsonBody, map[string]string{
		"Content-Type": "application/json",
	})
}
