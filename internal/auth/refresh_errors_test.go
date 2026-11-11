package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A refresh that fails for a reason other than invalid_grant must not read as an expired session,
// so callers (long-lived session holds) can retry it.
func TestGetAccessTokenTransientRefreshFailureIsNotExpired(t *testing.T) {
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))

	t.Run("503 from the token endpoint", func(t *testing.T) {
		server := &fakeTokenEndpoint{status: 503, body: `upstream down`}
		svc, _ := newRefreshService(t, server, &Credentials{AccessToken: expired, RefreshToken: "r"})

		_, err := svc.GetAccessToken(context.Background())
		assertTransientRefreshError(t, err)
		if !strings.Contains(err.Error(), "503") {
			t.Fatalf("error should carry the cause, got %v", err)
		}
	})

	t.Run("network failure", func(t *testing.T) {
		down := httptest.NewServer(http.NotFoundHandler())
		base := down.URL
		down.Close()
		svc, _ := newRefreshServiceAt(t, &http.Client{Timeout: 5 * time.Second}, base, &Credentials{AccessToken: expired, RefreshToken: "r"})

		_, err := svc.GetAccessToken(context.Background())
		assertTransientRefreshError(t, err)
	})
}

func assertTransientRefreshError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, ErrTokenExpired) || errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a transient refresh failure must not wrap ErrTokenExpired/ErrTokenInvalid, got %v", err)
	}
}

func TestGetAccessTokenInvalidGrantIsExpired(t *testing.T) {
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))
	server := &fakeTokenEndpoint{status: 400, body: `{"error":"invalid_grant"}`}
	svc, _ := newRefreshService(t, server, &Credentials{AccessToken: expired, RefreshToken: "r"})

	_, err := svc.GetAccessToken(context.Background())
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("invalid_grant must map to ErrTokenExpired, got %v", err)
	}
	if !strings.Contains(err.Error(), "port auth login") {
		t.Fatalf("error should tell the user to log in again: %v", err)
	}
}

// A refresh that can never succeed locally is a "log in again" condition, not a transient one.
func TestGetAccessTokenWithoutRefreshClientIsInvalid(t *testing.T) {
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))
	svc := newTestService(&mockTokenStorage{creds: &Credentials{AccessToken: expired, RefreshToken: "r"}})

	_, err := svc.GetAccessToken(context.Background())
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("a service with no refresh client must report ErrTokenInvalid, got %v", err)
	}
}

func TestGetAccessTokenWithInvalidStoredDPoPKeyIsInvalid(t *testing.T) {
	expired := generateTestJWTWithExpiry(t, time.Now().Add(-time.Hour))
	server := &fakeTokenEndpoint{status: 200, body: `{}`}
	svc, _ := newRefreshService(t, server, &Credentials{AccessToken: expired, RefreshToken: "r", DPoPPrivateKey: "not-a-key"})

	_, err := svc.GetAccessToken(context.Background())
	if !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("an unusable stored DPoP key must report ErrTokenInvalid, got %v", err)
	}
	if len(server.requests) != 0 {
		t.Fatal("no token request may be sent with an unusable key")
	}
}
