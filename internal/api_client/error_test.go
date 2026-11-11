package api

import (
	"errors"
	"testing"
)

func TestConnectionError_SentinelIdentity_ConnectionFailed(t *testing.T) {
	err := NewConnectionError("dial tcp 127.0.0.1:8080: connection refused", ErrConnectionFailed)

	if !errors.Is(err, ErrConnectionFailed) {
		t.Error("errors.Is should match ErrConnectionFailed for a connectionError wrapping it")
	}
	if errors.Is(err, ErrInvalidURL) {
		t.Error("errors.Is should NOT match ErrInvalidURL for a connectionError wrapping ErrConnectionFailed")
	}
}

func TestConnectionError_SentinelIdentity_InvalidURL(t *testing.T) {
	err := NewConnectionError("unsupported protocol scheme \"ftp\"", ErrInvalidURL)

	if !errors.Is(err, ErrInvalidURL) {
		t.Error("errors.Is should match ErrInvalidURL for a connectionError wrapping it")
	}
	if errors.Is(err, ErrConnectionFailed) {
		t.Error("errors.Is should NOT match ErrConnectionFailed for a connectionError wrapping ErrInvalidURL")
	}
}

func TestConnectionError_PreservesOriginalMessage(t *testing.T) {
	originalMsg := "dial tcp 127.0.0.1:8080: connection refused"
	err := NewConnectionError(originalMsg, ErrConnectionFailed)

	if err.Error() != originalMsg {
		t.Errorf("Error() should return original message %q, got %q", originalMsg, err.Error())
	}
	if err.Error() == ErrConnectionFailed.Error() {
		t.Error("Error() should not return sentinel text")
	}
}
