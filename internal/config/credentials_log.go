package config

import "go.uber.org/zap/zapcore"

// tokenPrefix returns the first 10 characters of s followed by "..." for
// identification in logs. If s is shorter than 10 characters it returns
// "[redacted]" to avoid exposing very short secrets.
func tokenPrefix(s string) string {
	if len(s) > 10 {
		return s[:10] + "..."
	}
	if s == "" {
		return ""
	}
	return "[redacted]"
}

// MarshalLogObject implements zap.ObjectMarshaler so that logging a
// Credentials value with zap.Object or zap.Any never emits raw token
// strings. Only boolean presence flags, truncated prefixes, and safe
// metadata (token type, expiry) are logged.
func (c Credentials) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	enc.AddBool("has_access_token", c.AccessToken != "")
	enc.AddBool("has_refresh_token", c.RefreshToken != "")
	enc.AddBool("has_dpop_key", c.DPoPPrivateKey != "")
	if c.TokenType != "" {
		enc.AddString("token_type", c.TokenType)
	}
	if c.ExpiresAt != 0 {
		enc.AddInt64("expires_at", c.ExpiresAt)
	}
	if c.AccessToken != "" {
		enc.AddString("access_token_prefix", tokenPrefix(c.AccessToken))
	}
	if c.RefreshToken != "" {
		enc.AddString("refresh_token_prefix", tokenPrefix(c.RefreshToken))
	}
	return nil
}
