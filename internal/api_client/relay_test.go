package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRelayCredentialsFetchesTheIssuedServers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/relay-credentials" {
			t.Errorf("unexpected relay request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"iceServers":[{"urls":["turn:relay.example.test:443?transport=tcp"],"username":"1794358800:alice","credential":"c3VwZXI="}],"expiresAt":"2026-11-11T12:00:00Z"}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	credentials, err := (&RelayClientImpl{client: client}).Credentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials.IceServers) != 1 || credentials.IceServers[0].Username != "1794358800:alice" || credentials.IceServers[0].Urls[0] != "turn:relay.example.test:443?transport=tcp" {
		t.Fatalf("relay credentials not decoded: %+v", credentials)
	}
}

func TestRelayCredentialsReportsAnUnconfiguredRelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"the media relay is not configured","code":"SERVICE_UNAVAILABLE","family":"SERVICE_UNAVAILABLE"}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	if _, err := (&RelayClientImpl{client: client}).Credentials(context.Background()); err == nil {
		t.Fatal("an unconfigured relay was not an error")
	}
}
