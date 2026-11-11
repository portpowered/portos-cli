package keepalive_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/portpowered/portos-cli/internal/keepalive"
)

var t0 = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestNextPingDueAt20s(t *testing.T) {
	_, due := keepalive.Next(keepalive.ModePing, t0, t0.Add(19*time.Second), false)
	require.False(t, due)
	body, due := keepalive.Next(keepalive.ModePing, t0, t0.Add(20*time.Second), false)
	require.True(t, due)
	require.Equal(t, map[string]any{"velocity": 0.0, "omega": 0.0, "duration": 1000}, body)
}

func TestNextFidgetThenMirror(t *testing.T) {
	body, due := keepalive.Next(keepalive.ModeFidget, t0, t0.Add(20*time.Second), false)
	require.True(t, due)
	require.Equal(t, 0.4, body["omega"])
	mirror, due := keepalive.Next(keepalive.ModeFidget, t0, t0.Add(time.Second), true)
	require.True(t, due, "a pending mirror is due at once")
	require.Equal(t, -0.4, mirror["omega"])
}

func TestNextPingIgnoresPendingMirror(t *testing.T) {
	_, due := keepalive.Next(keepalive.ModePing, t0, t0.Add(time.Second), true)
	require.False(t, due)
}

func TestDefaultIsPing(t *testing.T) {
	require.Equal(t, keepalive.ModePing, keepalive.Default)
	require.True(t, keepalive.ModeFidget.Valid())
	require.False(t, keepalive.Mode("nap").Valid())
}
