package logging

import (
	"net/http"
	"strings"
)

// sensitiveHeaders lists HTTP header names whose values must be redacted in
// debug output. Comparison is case-insensitive (header names are canonicalized
// by net/http).
var sensitiveHeaders = map[string]bool{
	"Authorization": true,
	"Set-Cookie":    true,
	"Cookie":        true,
}

// RedactString truncates s to the first 15 characters followed by "..." when
// len(s) > 15. For strings of 15 characters or fewer it returns "[redacted]"
// so that very short secrets are never shown at all.
func RedactString(s string) string {
	if len(s) > 15 {
		return s[:15] + "..."
	}
	return "[redacted]"
}

// RedactSensitiveHeaders returns a sanitized copy of headers suitable for
// debug logging. Sensitive header values (Authorization, Set-Cookie, Cookie)
// are replaced with a truncated/redacted form via RedactString. All other
// header values pass through unchanged.
func RedactSensitiveHeaders(h http.Header) map[string]string {
	safe := make(map[string]string, len(h))
	for k := range h {
		if sensitiveHeaders[http.CanonicalHeaderKey(k)] {
			safe[k] = RedactString(h.Get(k))
		} else {
			safe[k] = h.Get(k)
		}
	}
	return safe
}

// IsSensitiveHeader reports whether the given header name (case-insensitive)
// is considered sensitive for logging purposes.
func IsSensitiveHeader(name string) bool {
	return sensitiveHeaders[http.CanonicalHeaderKey(name)]
}

// RedactMapValues returns a copy of m with values for any key in
// sensitiveKeys redacted via RedactString. Key comparison is case-insensitive
// using strings.EqualFold.
func RedactMapValues(m map[string]string, sensitiveKeys []string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		redact := false
		for _, sk := range sensitiveKeys {
			if strings.EqualFold(k, sk) {
				redact = true
				break
			}
		}
		if redact {
			out[k] = RedactString(v)
		} else {
			out[k] = v
		}
	}
	return out
}
