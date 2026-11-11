package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// shortDir returns a short config directory: unix socket paths must stay under 108 bytes.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ph")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func decodeJSON(r io.Reader, v any) error { return json.NewDecoder(r).Decode(v) }

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

type mutableHolds struct {
	mu    sync.Mutex
	holds []HoldStatus
}

func (m *mutableHolds) List() []HoldStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]HoldStatus(nil), m.holds...)
}

func (m *mutableHolds) Open(context.Context, OpenHoldRequest) (OpenedHold, error) {
	return OpenedHold{}, errors.New("not used")
}
func (m *mutableHolds) Get(string) (HoldStatus, error) { return HoldStatus{}, errors.New("not used") }
func (m *mutableHolds) Release(context.Context, string) error {
	return errors.New("not used")
}

func (m *mutableHolds) set(h []HoldStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.holds = h
}
