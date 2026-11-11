package portos_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/portos"
)

type fakeEndpoints struct {
	apiclient.EndpointsClient
	response *api.EndpointQueryResponse
	expand   string
	force    bool
}

func (f *fakeEndpoints) GetByIDWithExpand(_ context.Context, _ string, expand string, force bool) (*api.EndpointQueryResponse, error) {
	f.expand, f.force = expand, force
	return f.response, nil
}

func attribute(namespace, name string, value map[string]interface{}) api.AttributeState {
	return api.AttributeState{Namespace: namespace, Name: name, Value: &value}
}

func TestReadStateParsesOperationalStateAndPose(t *testing.T) {
	fake := &fakeEndpoints{response: &api.EndpointQueryResponse{Results: api.EndpointsList{{Attributes: &[]api.AttributeState{
		attribute(api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, "operational-state", map[string]interface{}{"value": "ERROR", "errorReason": "Robot trapped"}),
		attribute("spatial", "pose", map[string]interface{}{
			"observedAt": "2026-11-11T09:00:00Z", "relativeToGroupRouteId": "sim/floor",
			"point": map[string]interface{}{"type": "Point", "coordinates": []interface{}{1.5, 0.0}},
		}),
	}}}}}
	state, err := portos.NewStateReader(fake).ReadState(t.Context(), "ep/1")
	require.NoError(t, err)
	require.Equal(t, api.OperationalStateError, state.Operational)
	require.Equal(t, "Robot trapped", state.ErrorReason)
	require.NotNil(t, state.Pose)
	require.Equal(t, []float32{1.5, 0}, state.Pose.Point.Coordinates)
	require.Equal(t, "sim/floor", state.Pose.RelativeToGroupRouteId)
	require.Equal(t, "interfaces.attributes", fake.expand)
	require.True(t, fake.force, "a poll reads the device, not stored state")
}

func TestReadStateReportsWhatIsMissingAsZero(t *testing.T) {
	fake := &fakeEndpoints{response: &api.EndpointQueryResponse{Results: api.EndpointsList{{}}}}
	state, err := portos.NewStateReader(fake).ReadState(t.Context(), "ep/1")
	require.NoError(t, err)
	require.Empty(t, state.Operational)
	require.Nil(t, state.Pose)

	_, err = portos.NewStateReader(&fakeEndpoints{response: &api.EndpointQueryResponse{}}).ReadState(t.Context(), "ep/1")
	require.Error(t, err)
}
