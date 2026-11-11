package hostclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHostEnvStripsCallerTokenAndLease(t *testing.T) {
	got := hostEnv([]string{
		"PORTOS_ACCESS_TOKEN=secret",
		"PORTOS_LEASE=lease",
		"PORTOS_REFRESH_TOKEN=refresh",
		"PORTOS_EXPIRES_AT=2026-11-10T00:00:00Z",
		"PORTOS_TOKEN_TYPE=Bearer",
		"PORTOS_API_URL=http://x",
		"PATH=/bin",
		"portos_lease=lower",
	})
	require.Equal(t, []string{"PORTOS_API_URL=http://x", "PATH=/bin"}, got)
}
