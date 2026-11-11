package api

import (
	"context"
	"github.com/portpowered/portos-cli/internal/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessageCreatePreservesCallerKeyAndReturnsDurableUnknownOutcome(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodPost || r.URL.Path != "/message-requests" || r.Header.Get("Idempotency-Key") != "same-intent" || r.Header.Get("If-Match") != "" {
			t.Errorf("unexpected message request transport: %s %s %v", r.Method, r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"request","status":"FAILED","results":[],"errors":[{"targetId":"endpoint","routeId":"route","code":"TIMED_OUT"}],"deadline":"2026-10-27T00:00:30Z","syncDeadlineSeconds":30}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	messages := &MessagesClientImpl{client: client}
	result, err := messages.Send(context.Background(), &api.CreateMessageRequest{}, "same-intent")
	if err != nil {
		t.Fatal(err)
	}
	if result.Id != "request" || result.Status != "FAILED" || len(result.Errors) != 1 || result.Errors[0].Code != "TIMED_OUT" || calls != 1 {
		t.Fatalf("durable outcome lost or request resent: %+v calls=%d", result, calls)
	}
}

func TestMessageCreateExposesGeneratedKeyAfterAnUncertainResponseWithoutRetry(t *testing.T) {
	calls, key := 0, ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		key = r.Header.Get("Idempotency-Key")
		_, _ = w.Write([]byte("{"))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	_, err := (&MessagesClientImpl{client: client}).Send(t.Context(), &api.CreateMessageRequest{})
	if err == nil || key == "" || !strings.Contains(err.Error(), key) || calls != 1 {
		t.Fatalf("uncertain request lost recovery identity or retried: key=%s calls=%d err=%v", key, calls, err)
	}
}
