package auth

import (
	"errors"
	"testing"
)

func TestAuthError_SentinelIdentity(t *testing.T) {
	err := newAuthError("your session has expired, please run port auth login", ErrTokenExpired)

	if !errors.Is(err, ErrTokenExpired) {
		t.Error("errors.Is should match ErrTokenExpired for an authError wrapping it")
	}
	if errors.Is(err, ErrTokenInvalid) {
		t.Error("errors.Is should NOT match ErrTokenInvalid for an authError wrapping ErrTokenExpired")
	}
	if errors.Is(err, ErrNoCredentials) {
		t.Error("errors.Is should NOT match ErrNoCredentials for an authError wrapping ErrTokenExpired")
	}
}

func TestAuthError_ErrorMessageIsUserFacing(t *testing.T) {
	msg := "your session has expired, please run port auth login"
	err := newAuthError(msg, ErrTokenExpired)

	if err.Error() != msg {
		t.Errorf("Error() should return user-facing message %q, got %q", msg, err.Error())
	}
	if err.Error() == ErrTokenExpired.Error() {
		t.Error("Error() should not return sentinel text")
	}
}
