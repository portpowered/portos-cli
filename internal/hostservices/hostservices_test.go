package hostservices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/clock"
	"github.com/portpowered/portos-cli/internal/host"
	"github.com/portpowered/portos-cli/internal/keepalive"
	"github.com/portpowered/portos-cli/internal/services/holds"
	"github.com/stretchr/testify/require"
)

type okPortos struct{}

func (okPortos) SendSync(_ context.Context, _, _, name string, _ map[string]any) (*api.MessageRequest, error) {
	return &api.MessageRequest{Id: "r-" + name, Status: api.SUCCEEDED}, nil
}

type storage struct {
	creds *auth.Credentials
	err   error
}

func (s storage) SaveCredentials(*auth.Credentials) error     { return nil }
func (s storage) LoadCredentials() (*auth.Credentials, error) { return s.creds, s.err }
func (s storage) DeleteCredentials() error                    { return nil }

func TestLoginCheckNeedsRefreshToken(t *testing.T) {
	for name, st := range map[string]storage{
		"none":       {err: errors.New("missing")},
		"no refresh": {creds: &auth.Credentials{AccessToken: "a"}},
	} {
		err := LoginCheck(st)(t.Context())
		var holdErr *holds.Error
		require.ErrorAs(t, err, &holdErr, name)
		require.Equal(t, holds.CodeHoldRequiresRefreshableLogin, holdErr.Code)
	}
	require.NoError(t, LoginCheck(storage{creds: &auth.Credentials{RefreshToken: "r"}})(t.Context()))
}

func TestHoldAdapterOpenListReleaseAndPause(t *testing.T) {
	fake := clock.NewFake(time.Unix(1000, 0))
	built := New(Options{Clock: fake, Portos: okPortos{}, Keepalive: keepalive.Default})
	svc := built.Services.Holds

	opened, err := svc.Open(t.Context(), host.OpenHoldRequest{EndpointID: "ep/AAAAAAAAAAAAAAAAAAAAAA", Kinds: []string{"motion"}, DeadManSeconds: 120})
	require.NoError(t, err)
	require.NotEmpty(t, opened.Lease)
	require.Equal(t, 120, opened.DeadManSeconds)
	require.NotNil(t, opened.Motion)

	_, err = svc.Open(t.Context(), host.OpenHoldRequest{EndpointID: "ep/AAAAAAAAAAAAAAAAAAAAAA", Kinds: []string{"motion"}})
	var coded *host.CodedError
	require.ErrorAs(t, err, &coded)
	require.Equal(t, holds.CodeHoldOwnedByOther, coded.Code)

	listed := svc.List()
	require.Len(t, listed, 1)
	require.Empty(t, listed[0].Ending)

	built.OnPause(time.Minute)
	got, err := svc.Get(opened.ID)
	require.NoError(t, err)
	require.Equal(t, host.StateEnded, got.State)
	require.Equal(t, string(holds.ReasonHostPaused), got.Ending.Reason)

	_, err = svc.Get("nope")
	require.ErrorAs(t, err, &coded)
	require.Equal(t, host.CodeHoldNotFound, coded.Code)
	require.ErrorAs(t, svc.Release(t.Context(), "nope"), &coded)
}

func TestOnShutdownEndsEveryHoldWithHostShutdown(t *testing.T) {
	fake := clock.NewFake(time.Unix(1000, 0))
	built := New(Options{Clock: fake, Portos: okPortos{}, Keepalive: keepalive.Default})
	svc := built.Services.Holds
	opened, err := svc.Open(t.Context(), host.OpenHoldRequest{EndpointID: "ep/AAAAAAAAAAAAAAAAAAAAAA", Kinds: []string{"motion"}})
	require.NoError(t, err)

	built.OnShutdown(t.Context())

	got, err := svc.Get(opened.ID)
	require.NoError(t, err)
	require.Equal(t, host.StateEnded, got.State)
	require.Equal(t, "host-shutdown", got.Ending.Reason)
}
