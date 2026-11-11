// Package endpointid canonicalises endpoint identifiers so every spelling of
// one endpoint compares equal. It mirrors the backend's compact reference
// grammar (backend/internal/platform/data_types/identifiers/compact.go) and
// never calls the API.
package endpointid

import (
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
)

const (
	uuidBytes      = 16
	compactLen     = 22
	fullSegments   = 5
	compactSegment = "ep"
)

// Key returns the endpoint UUID for a full identifier
// (port1/<owner type>/<owner id>/endpoints/<uuid>) or a compact reference
// (ep/<22 chars>, me/ep/<22 chars> or 0/ep/<22 chars>, where the id is the
// unpadded base64url of the 16 UUID bytes). ok is false for any other
// spelling, including aliases that need the API to resolve.
func Key(id string) (key string, ok bool) {
	parts := strings.Split(id, "/")
	switch {
	case len(parts) == fullSegments && parts[0] == "port1" && parts[3] == "endpoints":
		u, err := uuid.Parse(parts[4])
		if err != nil {
			return "", false
		}
		return u.String(), true
	case len(parts) == 2 && parts[0] == compactSegment:
		return compactKey(parts[1])
	case len(parts) == 3 && (parts[0] == "me" || parts[0] == "0") && parts[1] == compactSegment:
		return compactKey(parts[2])
	}
	return "", false
}

func compactKey(segment string) (string, bool) {
	if len(segment) != compactLen {
		return "", false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(segment)
	if err != nil || len(raw) != uuidBytes {
		return "", false
	}
	u, err := uuid.FromBytes(raw)
	if err != nil {
		return "", false
	}
	return u.String(), true
}

// Same reports whether a and b name one endpoint. Identifiers that have no key
// are equal only when spelled identically.
func Same(a, b string) bool {
	ka, okA := Key(a)
	kb, okB := Key(b)
	if okA && okB {
		return ka == kb
	}
	return a == b
}
