package transport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/exitcode"
	"github.com/portpowered/portos-cli/internal/hostclient"
	"github.com/stretchr/testify/require"
)

type recordedSend struct {
	lease string
	req   *api.CreateMessageRequest
}

type fakeHost struct {
	holds   []hostclient.Hold
	listErr error
	sent    []recordedSend
}

func (f *fakeHost) ListHolds(context.Context) ([]hostclient.Hold, error) { return f.holds, f.listErr }
func (f *fakeHost) Send(_ context.Context, lease string, req *api.CreateMessageRequest) (*api.MessageRequest, error) {
	f.sent = append(f.sent, recordedSend{lease, req})
	return &api.MessageRequest{Id: "host"}, nil
}

type fakeMessages struct{ sent int }

func (f *fakeMessages) Send(context.Context, *api.CreateMessageRequest, ...string) (*api.MessageRequest, error) {
	f.sent++
	return &api.MessageRequest{Id: "cloud"}, nil
}

type fakeClient struct {
	apiclient.Client
	messages *fakeMessages
}

func (f *fakeClient) Messages() apiclient.MessagesClient { return f.messages }
func (f *fakeClient) Relay() apiclient.RelayClient       { return nil }

func newResolver(dir string, host hostAPI, dialErr error, lease string) (*resolver, *fakeMessages) {
	messages := &fakeMessages{}
	return &resolver{
		configDir: func() (string, error) { return dir, nil },
		lease:     func() string { return lease },
		cloud:     &cloudSender{client: &fakeClient{messages: messages}},
		dial: func(context.Context, string) (hostAPI, error) {
			if dialErr != nil {
				return nil, dialErr
			}
			return host, nil
		},
	}, messages
}

func req() *api.CreateMessageRequest { return &api.CreateMessageRequest{} }

func TestResolverNoHostUsesCloud(t *testing.T) {
	dir := t.TempDir()
	// The real dial: it must not start a host.
	r := &resolver{
		configDir: func() (string, error) { return dir, nil },
		lease:     func() string { return "" },
		cloud:     &cloudSender{client: &fakeClient{messages: &fakeMessages{}}},
		dial:      dialHost,
	}
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.False(t, Held(sender))
	_, statErr := os.Stat(filepath.Join(dir, "run"))
	require.True(t, os.IsNotExist(statErr), "resolving must not spawn a host")
}

func TestResolverHeldEndpointUsesHost(t *testing.T) {
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "ep/1", Kinds: []string{"motion"}, State: "live"}}}
	r, messages := newResolver("d", host, nil, "the-lease")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.True(t, Held(sender))
	_, err = sender.Send(t.Context(), req(), "key")
	require.NoError(t, err)
	require.Len(t, host.sent, 1)
	require.Equal(t, "the-lease", host.sent[0].lease)
	require.Zero(t, messages.sent)
}

func TestResolverHeldEndpointWithoutLeaseStillUsesHost(t *testing.T) {
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "ep/1", Kinds: []string{"motion"}, State: "live"}}}
	r, _ := newResolver("d", host, nil, "")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.True(t, Held(sender))
}

func TestResolverOtherCapabilityUsesCloud(t *testing.T) {
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "ep/1", Kinds: []string{"motion"}, State: "live"}}}
	r, messages := newResolver("d", host, nil, "the-lease")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_POWER)
	require.NoError(t, err)
	require.False(t, Held(sender))
	_, err = sender.Send(t.Context(), req(), "key")
	require.NoError(t, err)
	require.Equal(t, 1, messages.sent)
}

func TestResolverEndedOrOtherEndpointWithoutLeaseUsesCloud(t *testing.T) {
	const ep1 = "me/ep/I-r_4j-cTIaVkx7o8yKJHg"
	const ep2 = "me/ep/AAAAAAAAAAAAAAAAAAAAAA"
	host := &fakeHost{holds: []hostclient.Hold{
		{ID: "hold-1", EndpointID: ep1, Kinds: []string{"motion"}, State: "ended"},
		{ID: "hold-2", EndpointID: ep2, Kinds: []string{"motion"}, State: "live"},
	}}
	r, _ := newResolver("d", host, nil, "")
	sender, err := r.For(t.Context(), ep1, api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.False(t, Held(sender))
}

func TestResolverHostErrorIsNotCloud(t *testing.T) {
	r, _ := newResolver("d", nil, errors.New("boom"), "")
	_, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.Error(t, err)
}

func TestResolverAliasEndpoint(t *testing.T) {
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "me/ep/I-r_4j-cTIaVkx7o8yKJHg", Kinds: []string{"motion"}, State: "live"}}}
	r, _ := newResolver("d", host, nil, "x")
	sender, err := r.For(t.Context(), "port1/principals/p1/endpoints/23eaffe2-3f9c-4c86-9593-1ee8f322891e", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.True(t, Held(sender), "another spelling of the held endpoint must still go to the host")

	other, err := r.For(t.Context(), "port1/principals/p1/endpoints/00000000-0000-4000-8000-000000000001", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	requireRefusal(t, other, "hold_not_found")
}

func TestResolverUnkeyedIDFailsSafeTowardLiveHold(t *testing.T) {
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: "me/ep/I-r_4j-cTIaVkx7o8yKJHg", Kinds: []string{"motion"}, State: "live"}}}
	r, _ := newResolver("d", host, nil, "x")
	sender, err := r.For(t.Context(), "my-vacuum", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	require.True(t, Held(sender))

	other, err := r.For(t.Context(), "my-vacuum", api.INTERFACE_PORTOS_POWER)
	require.NoError(t, err)
	require.False(t, Held(other), "a capability the hold does not cover still goes to the cloud")
}

func TestResolverTransportErrorIsNotCloud(t *testing.T) {
	host := &fakeHost{listErr: hostclient.ErrTransport}
	r, messages := newResolver("d", host, nil, "")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.ErrorIs(t, err, hostclient.ErrTransport)
	require.Nil(t, sender)
	require.Zero(t, messages.sent)
}

func requireRefusal(t *testing.T, sender Sender, code string) {
	t.Helper()
	reply, err := sender.Send(t.Context(), req(), "key")
	require.NoError(t, err)
	require.Equal(t, api.FAILED, reply.Status)
	require.Len(t, reply.Errors, 1)
	require.Equal(t, code, *reply.Errors[0].Error.Details.ProviderCode)
}

func TestResolverStaleLeaseOnEndedHoldRefusesHoldEnded(t *testing.T) {
	const ep = "me/ep/I-r_4j-cTIaVkx7o8yKJHg"
	host := &fakeHost{holds: []hostclient.Hold{{ID: "hold-1", EndpointID: ep, Kinds: []string{"motion"}, State: "ended"}}}
	r, messages := newResolver("d", host, nil, "old-lease")
	sender, err := r.For(t.Context(), ep, api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	requireRefusal(t, sender, "hold_ended")
	require.Zero(t, messages.sent)
	require.Empty(t, host.sent)
}

func TestResolverLeaseWithNoHostRefusesHoldNotFound(t *testing.T) {
	r, messages := newResolver("d", nil, hostclient.ErrNotRunning, "old-lease")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	requireRefusal(t, sender, "hold_not_found")
	require.Zero(t, messages.sent)
}

func TestResolverLeaseWithHostRouteNotServedRefusesHoldNotFound(t *testing.T) {
	host := &fakeHost{listErr: hostclient.ErrRouteNotServed}
	r, messages := newResolver("d", host, nil, "old-lease")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_REMOTE_CONTROL)
	require.NoError(t, err)
	requireRefusal(t, sender, "hold_not_found")
	require.Zero(t, messages.sent)
}

func TestResolverLeaseOnUncoveredNamespaceStillUsesCloud(t *testing.T) {
	r, messages := newResolver("d", nil, hostclient.ErrNotRunning, "old-lease")
	sender, err := r.For(t.Context(), "ep/1", api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER)
	require.NoError(t, err)
	require.False(t, Held(sender))
	_, err = sender.Send(t.Context(), req(), "key")
	require.NoError(t, err)
	require.Equal(t, 1, messages.sent)
}

func TestResolverRefusalsExitWithHoldCode(t *testing.T) {
	for _, code := range []string{"hold_ended", "hold_not_found"} {
		reply, err := (&refusingSender{endpointID: "ep/1", code: code}).Send(t.Context(), req(), "key")
		require.NoError(t, err)
		require.Equal(t, exitcode.ExitHold, exitcode.Code(&apiclient.MessageRequestFailed{Request: reply}), code)
	}
}
