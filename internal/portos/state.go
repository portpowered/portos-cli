package portos

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
)

// State is what the session host reads of a robot between messages: the
// operational state and the pose. A part the device does not report is its
// zero value.
type State struct {
	// Operational is the robotic-vacuum-cleaner operational state, or "" when
	// the endpoint reports none (a robot under remote control may not).
	Operational api.OperationalState
	// ErrorReason is the plain-language reason that goes with Operational ERROR.
	ErrorReason string
	// Pose is the spatial pose, or nil when the location is unknown.
	Pose *api.SpatialInterfacePoseAttribute
}

// StateReader reads an endpoint's current state.
type StateReader interface {
	// ReadState asks Port OS for the endpoint's live operational state and pose.
	ReadState(ctx context.Context, endpointID string) (State, error)
}

type stateReader struct {
	endpoints apiclient.EndpointsClient
}

// NewStateReader returns a StateReader over the Port OS endpoints API. It asks
// the server to refresh the attributes from the device, so a poll never reads
// a stored value.
func NewStateReader(endpoints apiclient.EndpointsClient) StateReader {
	return &stateReader{endpoints: endpoints}
}

func (r *stateReader) ReadState(ctx context.Context, endpointID string) (State, error) {
	response, err := r.endpoints.GetByIDWithExpand(ctx, endpointID, "interfaces.attributes", true)
	if err != nil {
		return State{}, fmt.Errorf("read state: %w", err)
	}
	if len(response.Results) == 0 {
		return State{}, fmt.Errorf("read state: endpoint %s not found", endpointID)
	}
	var state State
	if response.Results[0].Attributes == nil {
		return state, nil
	}
	for _, attr := range *response.Results[0].Attributes {
		if attr.Error != nil || attr.Value == nil {
			continue
		}
		switch {
		case isAttribute(attr, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER, api.INTERFACE_PORTOS_ROBOTIC_VACUUM_CLEANER_ATTRIBUTE_OPERATIONAL_STATE):
			var value api.RoboticVacuumCleanerInterfaceOperationalStateAttribute
			if decode(*attr.Value, &value) == nil {
				state.Operational = value.Value
				if value.ErrorReason != nil {
					state.ErrorReason = *value.ErrorReason
				}
			}
		case isAttribute(attr, api.INTERFACE_PORTOS_SPATIAL, api.INTERFACE_PORTOS_SPATIAL_ATTRIBUTE_POSE):
			var pose api.SpatialInterfacePoseAttribute
			if decode(*attr.Value, &pose) == nil && len(pose.Point.Coordinates) >= 2 {
				state.Pose = &pose
			}
		}
	}
	return state, nil
}

// isAttribute matches an attribute by name and interface, whether the
// endpoint spells the interface in full or by its last segment.
func isAttribute(attr api.AttributeState, namespace, name string) bool {
	short := namespace[strings.LastIndex(namespace, "/")+1:]
	return attr.Name == name && (attr.Namespace == namespace || attr.Namespace == short || strings.HasSuffix(attr.Namespace, "/"+short))
}

func decode(value map[string]interface{}, into any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, into)
}
