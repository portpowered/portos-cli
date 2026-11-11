package logging

import (
	"net/http"
	"testing"
)

func TestRedactString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "long string is truncated",
			input: "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.longtoken",
			want:  "Bearer eyJhbGci...",
		},
		{
			name:  "exactly 16 chars is truncated",
			input: "1234567890123456",
			want:  "123456789012345...",
		},
		{
			name:  "exactly 15 chars is redacted",
			input: "123456789012345",
			want:  "[redacted]",
		},
		{
			name:  "short string is fully redacted",
			input: "secret",
			want:  "[redacted]",
		},
		{
			name:  "empty string is redacted",
			input: "",
			want:  "[redacted]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactString(tt.input)
			if got != tt.want {
				t.Errorf("RedactString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactSensitiveHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.verylongtoken")
	h.Set("Set-Cookie", "session=abc123def456ghi789; Path=/; HttpOnly")
	h.Set("Content-Type", "application/json")
	h.Set("X-Request-Id", "req-12345")

	result := RedactSensitiveHeaders(h)

	// Authorization should be redacted (truncated)
	if got := result["Authorization"]; got != "Bearer eyJhbGci..." {
		t.Errorf("Authorization = %q, want truncated form", got)
	}

	// Set-Cookie should be redacted (truncated)
	if got := result["Set-Cookie"]; got != "session=abc123d..." {
		t.Errorf("Set-Cookie = %q, want truncated form", got)
	}

	// Non-sensitive headers pass through unchanged
	if got := result["Content-Type"]; got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
	if got := result["X-Request-Id"]; got != "req-12345" {
		t.Errorf("X-Request-Id = %q, want %q", got, "req-12345")
	}
}

func TestRedactSensitiveHeaders_CaseInsensitive(t *testing.T) {
	h := http.Header{}
	// net/http canonicalizes header names, so "authorization" becomes "Authorization"
	h.Set("authorization", "Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.token")

	result := RedactSensitiveHeaders(h)

	if got := result["Authorization"]; got != "Bearer eyJhbGci..." {
		t.Errorf("Authorization = %q, want truncated form", got)
	}
}

func TestRedactSensitiveHeaders_Empty(t *testing.T) {
	result := RedactSensitiveHeaders(http.Header{})
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestRedactSensitiveHeaders_Cookie(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "session=verylongsessionvalue12345")

	result := RedactSensitiveHeaders(h)

	if got := result["Cookie"]; got != "session=verylon..." {
		t.Errorf("Cookie = %q, want truncated form", got)
	}
}

func TestIsSensitiveHeader(t *testing.T) {
	if !IsSensitiveHeader("Authorization") {
		t.Error("expected Authorization to be sensitive")
	}
	if !IsSensitiveHeader("set-cookie") {
		t.Error("expected set-cookie to be sensitive (case-insensitive)")
	}
	if IsSensitiveHeader("Content-Type") {
		t.Error("expected Content-Type to NOT be sensitive")
	}
}

func TestRedactMapValues(t *testing.T) {
	m := map[string]string{
		"AccessToken":    "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.longtoken",
		"RefreshToken":   "refresh-token-value-that-is-long",
		"DPoPPrivateKey": "private-key-material-here-long",
		"Username":       "testuser",
	}

	result := RedactMapValues(m, []string{"AccessToken", "RefreshToken", "DPoPPrivateKey"})

	if got := result["AccessToken"]; got != "eyJhbGciOiJSUzI..." {
		t.Errorf("AccessToken = %q, want truncated form", got)
	}
	if got := result["RefreshToken"]; got != "refresh-token-v..." {
		t.Errorf("RefreshToken = %q, want truncated form", got)
	}
	if got := result["DPoPPrivateKey"]; got != "private-key-mat..." {
		t.Errorf("DPoPPrivateKey = %q, want truncated form", got)
	}
	if got := result["Username"]; got != "testuser" {
		t.Errorf("Username = %q, want %q", got, "testuser")
	}
}
