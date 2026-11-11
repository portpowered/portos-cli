package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/logging"
)

type attributeEndpoints struct {
	apiclient.EndpointsClient
	gotID, gotExpand string
	gotForce         bool
	response         *api.EndpointQueryResponse
}

func (a *attributeEndpoints) GetByIDWithExpand(_ context.Context, id, expand string, force bool) (*api.EndpointQueryResponse, error) {
	a.gotID, a.gotExpand, a.gotForce = id, expand, force
	return a.response, nil
}

type attributeClient struct {
	apiclient.Client
	endpoints *attributeEndpoints
}

func (c *attributeClient) Endpoints() apiclient.EndpointsClient { return c.endpoints }

func poseResponse() *api.EndpointQueryResponse {
	value := map[string]interface{}{
		"point":                  map[string]interface{}{"type": "Point", "coordinates": []interface{}{1.5, 2.0}},
		"heading":                90.0,
		"relativeToGroupRouteId": "map-1",
		"observedAt":             "2026-11-10T09:00:00Z",
	}
	attributes := []api.AttributeState{
		{Namespace: rcNamespace, Name: "session", Value: &map[string]interface{}{"value": "ACTIVE"}},
		{Namespace: "port1/systems/zero/capability-interfaces/spatial", Name: "pose", Value: &value},
	}
	return &api.EndpointQueryResponse{Results: []api.Endpoint{{Id: "ep/1", Attributes: &attributes}}}
}

func newTestAttributeCommand(response *api.EndpointQueryResponse) (*EndpointCommand, *attributeEndpoints, *recordingFormatter) {
	endpoints := &attributeEndpoints{response: response}
	formatter := &recordingFormatter{json: true}
	return NewEndpointCommand(&attributeClient{endpoints: endpoints}, formatter, logging.NewLoggerRef(), nil), endpoints, formatter
}

func TestAttributeReadForced(t *testing.T) {
	cmd, endpoints, formatter := newTestAttributeCommand(poseResponse())
	err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/pose", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if endpoints.gotID != "ep/1" || endpoints.gotExpand != "interfaces.attributes" || !endpoints.gotForce {
		t.Fatalf("query = id %q expand %q force %v", endpoints.gotID, endpoints.gotExpand, endpoints.gotForce)
	}
	encoded, _ := json.Marshal(formatter.printed[0])
	var pose api.SpatialInterfacePoseAttribute
	if err := json.Unmarshal(encoded, &pose); err != nil {
		t.Fatal(err)
	}
	if pose.Point.Coordinates[0] != 1.5 || pose.Point.Coordinates[1] != 2.0 || pose.Heading == nil || *pose.Heading != 90 {
		t.Fatalf("pose = %s", encoded)
	}
}

func TestAttributeReadWithoutForceOmitsForcing(t *testing.T) {
	cmd, endpoints, _ := newTestAttributeCommand(poseResponse())
	if err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/pose"}); err != nil {
		t.Fatal(err)
	}
	if endpoints.gotForce {
		t.Fatal("force requested without --force")
	}
}

func TestAttributeNotReported(t *testing.T) {
	cmd, _, _ := newTestAttributeCommand(poseResponse())
	err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/position"})
	if err == nil || err.Error() != "attribute spatial/position not reported" {
		t.Fatalf("err = %v", err)
	}
}

func TestAttributeRejectsMalformedName(t *testing.T) {
	cmd, _, _ := newTestAttributeCommand(poseResponse())
	for _, name := range []string{"pose", "/pose", "spatial/"} {
		if err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: name}); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestAttributeErrorBranch(t *testing.T) {
	response := poseResponse()
	(*response.Results[0].Attributes)[1].Value = nil
	(*response.Results[0].Attributes)[1].Error = &api.Error{Message: "provider has no state"}
	cmd, _, formatter := newTestAttributeCommand(response)
	err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/pose"})
	if err == nil || !strings.Contains(err.Error(), "provider has no state") || len(formatter.printed) != 0 {
		t.Fatalf("err = %v printed = %v", err, formatter.printed)
	}
}

func TestAttributeWarnsOnStaleValue(t *testing.T) {
	response := poseResponse()
	sampled := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	stale := api.STALE
	(*response.Results[0].Attributes)[1].Freshness = &stale
	(*response.Results[0].Attributes)[1].SampledAt = &sampled
	cmd, _, formatter := newTestAttributeCommand(response)
	var stderr bytes.Buffer
	cmd.stderr = &stderr
	if err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/pose", Force: true}); err != nil {
		t.Fatal(err)
	}
	if len(formatter.printed) != 1 {
		t.Fatal("value must still be printed on stdout")
	}
	warning := stderr.String()
	if strings.Count(warning, "\n") != 1 || !strings.Contains(warning, "STALE") || !strings.Contains(warning, "2026-11-10T08:00:00Z") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestAttributeFreshValueHasNoWarning(t *testing.T) {
	response := poseResponse()
	fresh := api.FRESH
	(*response.Results[0].Attributes)[1].Freshness = &fresh
	cmd, _, _ := newTestAttributeCommand(response)
	var stderr bytes.Buffer
	cmd.stderr = &stderr
	if err := cmd.Attribute(t.Context(), AttributeParams{EndpointID: "ep/1", Attribute: "spatial/pose"}); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("warning = %q", stderr.String())
	}
}
