package portos_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/portos"
)

type fakeMessages struct {
	requests []*api.CreateMessageRequest
	keys     [][]string
	reply    *api.MessageRequest
	err      error
}

func (f *fakeMessages) Send(_ context.Context, r *api.CreateMessageRequest, keys ...string) (*api.MessageRequest, error) {
	f.requests = append(f.requests, r)
	f.keys = append(f.keys, keys)
	return f.reply, f.err
}

func TestSendSyncBuildsSyncEndpointMessageWithFreshKey(t *testing.T) {
	reply := &api.MessageRequest{Id: "r"}
	fake := &fakeMessages{reply: reply}
	c := portos.New(fake)
	for i := 0; i < 2; i++ {
		got, err := c.SendSync(context.Background(), "ep", "ns", "move", map[string]any{"seqNum": 1})
		require.NoError(t, err)
		require.Same(t, reply, got)
	}
	r := fake.requests[0]
	require.Equal(t, api.SyncResponseType, *r.ResponseType)
	require.Equal(t, api.EndpointTarget, r.Target.Type)
	require.Equal(t, "ep", r.Target.Id)
	require.Equal(t, "ns", r.Message.Header.Namespace)
	require.Equal(t, "move", r.Message.Header.Name)
	require.Equal(t, "1.0", r.Message.Header.Version)
	require.Len(t, fake.keys[0], 1)
	require.NotEmpty(t, fake.keys[0][0])
	require.NotEqual(t, fake.keys[0][0], fake.keys[1][0])
}

func TestUnauthorizedAndCodes(t *testing.T) {
	fake := &fakeMessages{err: &apiclient.APIError{StatusCode: 401, Err: errors.New("no")}}
	_, err := portos.New(fake).SendSync(context.Background(), "ep", "ns", "move", nil)
	require.True(t, portos.IsUnauthorized(err))
	require.False(t, portos.IsUnauthorized(errors.New("other")))

	code := "remote_control_not_active"
	reply := &api.MessageRequest{Errors: []api.MessageRequestError{{Error: &api.Error{Code: "x", Details: &api.ErrorDetails{ProviderCode: &code}}}}}
	require.True(t, portos.HasCode(reply, code))
	require.False(t, portos.HasCode(reply, "other"))
	require.False(t, portos.HasCode(nil, code))
}
