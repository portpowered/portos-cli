package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
)

func TestWrapError_Nil(t *testing.T) {
	if WrapError(nil, "") != nil {
		t.Error("expected nil for nil input")
	}
}

func TestWrapError_404PreservesMessageAndHint(t *testing.T) {
	msg := "endpoint not found"
	err := &apiclient.APIError{
		StatusCode: 404,
		Message:    &msg,
		Err:        fmt.Errorf("%s (status: 404)", msg),
	}
	wrapped := WrapError(err, "http://localhost:3000")
	// Original message preserved
	if !strings.Contains(wrapped.Error(), "endpoint not found") {
		t.Errorf("expected original message preserved, got: %s", wrapped.Error())
	}
	// Generic fallback hint shown
	if !strings.Contains(wrapped.Error(), "The requested resource was not found") {
		t.Errorf("expected generic fallback hint, got: %s", wrapped.Error())
	}

}

func TestWrapError_404NilMessage(t *testing.T) {
	result := WrapError(&apiclient.APIError{StatusCode: 404}, "http://localhost:3000")
	if !strings.Contains(result.Error(), "requested resource not found") || !strings.Contains(result.Error(), defaultNotFoundHint) {
		t.Fatalf("missing fallback message or hint: %s", result)
	}
}

func TestWrapError_422UnsupportedNamespace(t *testing.T) {
	msg := "unsupported message namespace: port1/systems/zero/capability-interfaces/brightness. Supported: power, speech-synthesizer"
	err := &apiclient.APIError{
		StatusCode: 422,
		Message:    &msg,
		Err:        fmt.Errorf("%s (status: 422)", msg),
	}

	wrapped := WrapError(err, "http://localhost:3000")
	want := "command not supported: port1/systems/zero/capability-interfaces/brightness. This endpoint supports: power, speech-synthesizer"
	if wrapped.Error() != want {
		t.Errorf("got:  %s\nwant: %s", wrapped.Error(), want)
	}
}

func TestWrapError_422UnsupportedNamespaceWrapped(t *testing.T) {
	msg := "unsupported message namespace: port1/systems/zero/capability-interfaces/lock. Supported: power"
	apiErr := &apiclient.APIError{
		StatusCode: 422,
		Message:    &msg,
		Err:        fmt.Errorf("%s (status: 422)", msg),
	}
	// Simulate the wrapping done by MessageCommand.Run()
	err := fmt.Errorf("failed to send message: %w", apiErr)

	wrapped := WrapError(err, "http://localhost:3000")
	want := "command not supported: port1/systems/zero/capability-interfaces/lock. This endpoint supports: power"
	if wrapped.Error() != want {
		t.Errorf("got:  %s\nwant: %s", wrapped.Error(), want)
	}
}

func TestWrapError_422OtherMessage(t *testing.T) {
	msg := "validation failed: invalid payload"
	err := &apiclient.APIError{
		StatusCode: 422,
		Message:    &msg,
		Err:        fmt.Errorf("%s (status: 422)", msg),
	}

	wrapped := WrapError(err, "http://localhost:3000")
	// Should pass through without rewriting — not an unsupported namespace error
	if wrapped.Error() != err.Error() {
		t.Errorf("expected original error to pass through, got: %s", wrapped.Error())
	}
}

func TestWrapError_422NilMessage(t *testing.T) {
	err := &apiclient.APIError{
		StatusCode: 422,
		Message:    nil,
		Err:        errors.New("unprocessable entity (status: 422)"),
	}

	wrapped := WrapError(err, "http://localhost:3000")
	// Should pass through — no Message field to inspect
	if wrapped.Error() != err.Error() {
		t.Errorf("expected original error to pass through, got: %s", wrapped.Error())
	}
}

func TestWrapError_ConnectionErrors(t *testing.T) {
	const testURL = "http://localhost:9999"

	tests := []struct {
		name       string
		inputErr   error
		apiBaseURL string
		wantSubstr []string
	}{
		{
			name:       "ErrConnectionFailed sentinel includes URL and config hints",
			inputErr:   apiclient.NewConnectionError(`Get "http://localhost:9999/endpoints": dial tcp [::1]:9999: connectex: No connection could be made`, apiclient.ErrConnectionFailed),
			apiBaseURL: testURL,
			wantSubstr: []string{"connection failed", testURL, "PORTOS_API_BASE_URL", "port init"},
		},
		{
			name:       "wrapped ErrConnectionFailed includes URL and config hints",
			inputErr:   fmt.Errorf("request failed: %w", apiclient.NewConnectionError(`connection refused`, apiclient.ErrConnectionFailed)),
			apiBaseURL: testURL,
			wantSubstr: []string{"connection failed", testURL, "PORTOS_API_BASE_URL", "port init"},
		},
		{
			name:       "ErrConnectionFailed with different base URL",
			inputErr:   apiclient.NewConnectionError(`dial tcp: lookup badhost: no such host`, apiclient.ErrConnectionFailed),
			apiBaseURL: "http://badhost:3000",
			wantSubstr: []string{"connection failed", "http://badhost:3000", "PORTOS_API_BASE_URL", "port init"},
		},
		{
			name:       "empty URL shows not configured placeholder",
			inputErr:   apiclient.NewConnectionError(`dial tcp: connection refused`, apiclient.ErrConnectionFailed),
			apiBaseURL: "",
			wantSubstr: []string{"connection failed", "(not configured)", "PORTOS_API_BASE_URL", "port init"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapError(tt.inputErr, tt.apiBaseURL)
			if result == nil {
				t.Fatal("WrapError() = nil, want non-nil")
			}
			for _, substr := range tt.wantSubstr {
				if !strings.Contains(result.Error(), substr) {
					t.Errorf("WrapError() = %q, want substring %q", result.Error(), substr)
				}
			}
		})
	}
}

func TestWrapError_ProtocolSchemeErrors(t *testing.T) {
	tests := []struct {
		name       string
		inputErr   error
		apiBaseURL string
		wantSubstr []string
	}{
		{
			name:       "ErrInvalidURL sentinel includes URL and config hints",
			inputErr:   apiclient.NewConnectionError(`unsupported protocol scheme "localhost"`, apiclient.ErrInvalidURL),
			apiBaseURL: "localhost:8080",
			wantSubstr: []string{"invalid API base URL", `"localhost:8080"`, "PORTOS_API_BASE_URL", "port init"},
		},
		{
			name:       "wrapped ErrInvalidURL with empty URL shows placeholder",
			inputErr:   fmt.Errorf("request failed: %w", apiclient.NewConnectionError(`unsupported protocol scheme "ftp"`, apiclient.ErrInvalidURL)),
			apiBaseURL: "",
			wantSubstr: []string{"invalid API base URL", `"(not configured)"`, "PORTOS_API_BASE_URL", "port init"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapError(tt.inputErr, tt.apiBaseURL)
			if result == nil {
				t.Fatal("WrapError() = nil, want non-nil")
			}
			for _, substr := range tt.wantSubstr {
				if !strings.Contains(result.Error(), substr) {
					t.Errorf("WrapError() = %q, want substring %q", result.Error(), substr)
				}
			}
		})
	}
}

func TestWrapError_AuthErrorsContainRemediation(t *testing.T) {
	tests := []struct {
		name     string
		inputErr error
	}{
		{
			name:     "token expired sentinel",
			inputErr: fmt.Errorf("access token expired: %w", auth.ErrTokenExpired),
		},
		{
			name:     "token invalid sentinel",
			inputErr: fmt.Errorf("invalid access token: %w", auth.ErrTokenInvalid),
		},
		{
			name:     "no credentials sentinel",
			inputErr: fmt.Errorf("no access token: %w", auth.ErrNoCredentials),
		},
		{
			name:     "401 API error via sentinel",
			inputErr: &apiclient.APIError{StatusCode: 401, Err: errors.New("unauthorized")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapError(tt.inputErr, "http://localhost:3000")
			msg := result.Error()
			if !strings.Contains(msg, "authentication failed") {
				t.Errorf("expected 'authentication failed', got: %s", msg)
			}
			if !strings.Contains(msg, "port auth login") {
				t.Errorf("expected 'port auth login', got: %s", msg)
			}
			if !strings.Contains(msg, "port auth set-token") {
				t.Errorf("expected 'port auth set-token', got: %s", msg)
			}
		})
	}
}

func TestWrapError_403ContainsAuthRemediation(t *testing.T) {
	err := &apiclient.APIError{StatusCode: 403, Err: errors.New("forbidden")}
	result := WrapError(err, "http://localhost:3000")
	msg := result.Error()
	if !strings.Contains(msg, "access denied") {
		t.Errorf("expected 'access denied', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("expected 'port auth login' in 403 message, got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("expected 'port auth set-token' in 403 message, got: %s", msg)
	}
}

func TestWrapError_NonSentinelErrorNotMisclassified(t *testing.T) {
	// Errors that mention auth/connection keywords in their message but do NOT
	// wrap sentinels must NOT be classified — the error handler is now type-safe.
	tests := []struct {
		name       string
		inputErr   error
		wantAbsent []string
	}{
		{
			name:       "raw string mentioning token is not auth error",
			inputErr:   errors.New("jwt: token is expired"),
			wantAbsent: []string{"authentication failed"},
		},
		{
			name:       "raw string mentioning 401 is not auth error",
			inputErr:   errors.New("server returned 401 Unauthorized"),
			wantAbsent: []string{"authentication failed"},
		},
		{
			name:       "raw string mentioning connection refused is not connection error",
			inputErr:   errors.New(`Get "http://localhost:9999": connection refused`),
			wantAbsent: []string{"connection failed", "unable to reach"},
		},
		{
			name:       "raw string mentioning unsupported protocol is not invalid URL error",
			inputErr:   errors.New(`unsupported protocol scheme "localhost"`),
			wantAbsent: []string{"invalid API base URL"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := WrapError(tt.inputErr, "http://localhost:3000")
			for _, absent := range tt.wantAbsent {
				if strings.Contains(result.Error(), absent) {
					t.Errorf("non-sentinel error should NOT be classified, got: %s", result.Error())
				}
			}
		})
	}
}

func TestWrapError_SetupErrorPassesThrough(t *testing.T) {
	setupErr := apiclient.NewSetupError("no access token — authenticate with \"port auth login\", \"port auth set-token <token>\", or PORTOS_ACCESS_TOKEN")
	result := WrapError(setupErr, "http://localhost:3000")
	// SetupError should pass through without being rewritten
	if result.Error() != setupErr.Error() {
		t.Errorf("SetupError should pass through unchanged.\ngot:  %s\nwant: %s", result.Error(), setupErr.Error())
	}
}

func TestWrapError_401APIErrorContainsRemediation(t *testing.T) {
	err := &apiclient.APIError{
		StatusCode: 401,
		Err:        errors.New("unauthorized"),
	}
	result := WrapError(err, "http://localhost:3000")
	msg := result.Error()
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("401 error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("401 error should mention 'port auth set-token', got: %s", msg)
	}
}

func TestWrapError_403APIErrorContainsRemediation(t *testing.T) {
	err := &apiclient.APIError{
		StatusCode: 403,
		Err:        errors.New("forbidden"),
	}
	result := WrapError(err, "http://localhost:3000")
	msg := result.Error()
	if !strings.Contains(msg, "access denied") {
		t.Errorf("expected 'access denied', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth login") {
		t.Errorf("403 error should mention 'port auth login', got: %s", msg)
	}
	if !strings.Contains(msg, "port auth set-token") {
		t.Errorf("403 error should mention 'port auth set-token', got: %s", msg)
	}
}

func TestWrapError_UnknownErrorPassesThrough(t *testing.T) {
	err := errors.New("some other error")
	result := WrapError(err, "http://localhost:3000")
	if result.Error() != err.Error() {
		t.Errorf("WrapError() = %q, want passthrough %q", result.Error(), err.Error())
	}
}

func TestFormatUnsupportedCapabilityError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "standard format",
			msg:  "unsupported message namespace: brightness. Supported: power, speech-synthesizer",
			want: "command not supported: brightness. This endpoint supports: power, speech-synthesizer",
		},
		{
			name: "full namespace path",
			msg:  "unsupported message namespace: port1/systems/zero/capability-interfaces/brightness. Supported: power",
			want: "command not supported: port1/systems/zero/capability-interfaces/brightness. This endpoint supports: power",
		},
		{
			name: "single supported capability",
			msg:  "unsupported message namespace: lock. Supported: power",
			want: "command not supported: lock. This endpoint supports: power",
		},
		{
			name: "no prefix match",
			msg:  "some other error message",
			want: "some other error message",
		},
		{
			name: "missing supported section",
			msg:  "unsupported message namespace: brightness",
			want: "unsupported message namespace: brightness",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUnsupportedCapabilityError(tt.msg)
			if got.Error() != tt.want {
				t.Errorf("got:  %s\nwant: %s", got.Error(), tt.want)
			}
		})
	}
}
