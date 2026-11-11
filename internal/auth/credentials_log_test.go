package auth

import (
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestCredentials_MarshalLogObject_RedactsTokens(t *testing.T) {
	// Realistic token values that must never appear in log output.
	accessToken := "eyJhbGciOiJFZERTQSIsInR5cCI6ImF0K2p3dCJ9.eyJzdWIiOiJwb3J0MS91c2Vycy90ZXN0In0.signature"
	refreshToken := "dGhpcy1pcy1hLXJlZnJlc2gtdG9rZW4tdGhhdC1pcy12ZXJ5LWxvbmc"
	dpopKey := "MC4CAQAwBQYDK2VwBCIEIHpYk2x3bAEz0PJ0nDGoXm3MqJlqXbuZx3E_examplekey"

	creds := Credentials{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		TokenType:      "DPoP",
		DPoPPrivateKey: dpopKey,
	}

	// Capture log output via zap observer.
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	logger.Debug("credentials", zap.Object("creds", creds))

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]
	raw := entry.ContextMap()

	// Serialize the entire log entry to a string for substring checks.
	serialized := serializeContextMap(entry.Message, raw)

	// Full token values must NOT appear anywhere in the serialized output.
	for _, secret := range []string{accessToken, refreshToken, dpopKey} {
		if strings.Contains(serialized, secret) {
			t.Errorf("full secret leaked in log output: %s", secret[:20]+"...")
		}
	}

	// Verify the structured fields exist with expected values.
	credsMap, ok := raw["creds"]
	if !ok {
		t.Fatal("expected 'creds' field in log output")
	}

	m, ok := credsMap.(map[string]any)
	if !ok {
		t.Fatalf("expected creds to be map, got %T", credsMap)
	}

	assertBool(t, m, "has_access_token", true)
	assertBool(t, m, "has_refresh_token", true)
	assertBool(t, m, "has_dpop_key", true)
	assertString(t, m, "token_type", "DPoP")

	// Prefixes should be 10 chars + "..."
	prefix, ok := m["access_token_prefix"].(string)
	if !ok || len(prefix) > 14 {
		t.Errorf("access_token_prefix should be truncated, got %q", prefix)
	}
	if strings.Contains(prefix, ".") && len(prefix) > 14 {
		t.Error("access_token_prefix contains too much of the original token")
	}
}

func TestCredentials_MarshalLogObject_EmptyCredentials(t *testing.T) {
	creds := Credentials{}

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	logger.Debug("empty", zap.Object("creds", creds))

	entry := logs.All()[0]
	raw := entry.ContextMap()
	credsMap := raw["creds"].(map[string]any)

	assertBool(t, credsMap, "has_access_token", false)
	assertBool(t, credsMap, "has_refresh_token", false)
	assertBool(t, credsMap, "has_dpop_key", false)

	// No prefix fields should be present for empty tokens.
	if _, ok := credsMap["access_token_prefix"]; ok {
		t.Error("access_token_prefix should not be present for empty token")
	}
	if _, ok := credsMap["refresh_token_prefix"]; ok {
		t.Error("refresh_token_prefix should not be present for empty token")
	}
}

func TestTokenPrefix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"short", "[redacted]"},
		{"exactly10!", "[redacted]"},
		{"longertoken123", "longertoke..."},
		{"eyJhbGciOiJFZERTQSI", "eyJhbGciOi..."},
	}
	for _, tc := range tests {
		got := tokenPrefix(tc.input)
		if got != tc.want {
			t.Errorf("tokenPrefix(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// helpers

func serializeContextMap(message string, raw map[string]any) string {
	var b strings.Builder
	b.WriteString(message)
	for k, v := range raw {
		fmt.Fprintf(&b, " %s=%s", k, formatValue(v))
	}
	return b.String()
}

func formatValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]any:
		var b strings.Builder
		b.WriteString("{")
		for k, v := range val {
			fmt.Fprintf(&b, "%s:%s ", k, formatValue(v))
		}
		b.WriteString("}")
		return b.String()
	default:
		return ""
	}
}

func assertBool(t *testing.T, m map[string]any, key string, want bool) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing key %q", key)
		return
	}
	got, ok := v.(bool)
	if !ok {
		t.Errorf("key %q: expected bool, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("key %q = %v, want %v", key, got, want)
	}
}

func assertString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	v, ok := m[key]
	if !ok {
		t.Errorf("missing key %q", key)
		return
	}
	got, ok := v.(string)
	if !ok {
		t.Errorf("key %q: expected string, got %T", key, v)
		return
	}
	if got != want {
		t.Errorf("key %q = %q, want %q", key, got, want)
	}
}
