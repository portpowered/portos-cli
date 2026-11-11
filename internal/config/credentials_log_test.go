package config

import (
	"fmt"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestConfigCredentials_MarshalLogObject_RedactsTokens(t *testing.T) {
	accessToken := "eyJhbGciOiJFZERTQSIsInR5cCI6ImF0K2p3dCJ9.eyJzdWIiOiJwb3J0MS91c2Vycy90ZXN0In0.signature"
	refreshToken := "dGhpcy1pcy1hLXJlZnJlc2gtdG9rZW4tdGhhdC1pcy12ZXJ5LWxvbmc"
	dpopKey := "MC4CAQAwBQYDK2VwBCIEIHpYk2x3bAEz0PJ0nDGoXm3MqJlqXbuZx3E_examplekey"

	creds := Credentials{
		AccessToken:    accessToken,
		RefreshToken:   refreshToken,
		ExpiresAt:      1710810000000,
		TokenType:      "DPoP",
		DPoPPrivateKey: dpopKey,
	}

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	logger.Debug("credentials", zap.Object("creds", creds))

	if logs.Len() != 1 {
		t.Fatalf("expected 1 log entry, got %d", logs.Len())
	}

	entry := logs.All()[0]
	raw := entry.ContextMap()

	// Serialize the entire log entry to check for leaks.
	serialized := serializeContextMap(entry.Message, raw)

	for _, secret := range []string{accessToken, refreshToken, dpopKey} {
		if strings.Contains(serialized, secret) {
			t.Errorf("full secret leaked in log output: %s", secret[:20]+"...")
		}
	}

	credsMap, ok := raw["creds"].(map[string]any)
	if !ok {
		t.Fatalf("expected creds to be map, got %T", raw["creds"])
	}

	assertBool(t, credsMap, "has_access_token", true)
	assertBool(t, credsMap, "has_refresh_token", true)
	assertBool(t, credsMap, "has_dpop_key", true)
	assertString(t, credsMap, "token_type", "DPoP")

	// ExpiresAt should be present.
	if _, ok := credsMap["expires_at"]; !ok {
		t.Error("expected expires_at in log output")
	}

	// Prefixes should be truncated.
	prefix, _ := credsMap["access_token_prefix"].(string)
	if len(prefix) > 14 {
		t.Errorf("access_token_prefix too long: %q", prefix)
	}
}

func TestConfigCredentials_MarshalLogObject_EmptyCredentials(t *testing.T) {
	creds := Credentials{}

	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	logger.Debug("empty", zap.Object("creds", creds))

	credsMap := logs.All()[0].ContextMap()["creds"].(map[string]any)

	assertBool(t, credsMap, "has_access_token", false)
	assertBool(t, credsMap, "has_refresh_token", false)
	assertBool(t, credsMap, "has_dpop_key", false)

	if _, ok := credsMap["access_token_prefix"]; ok {
		t.Error("access_token_prefix should not be present for empty token")
	}
	if _, ok := credsMap["expires_at"]; ok {
		t.Error("expires_at should not be present when zero")
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
