package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
)

type fakeGroups struct {
	gotToken  *string
	gotMax    *int
	gotExpand []string
	response  *api.EnumerateEndpointGroupsResponse
}

func (f *fakeGroups) Enumerate(_ context.Context, nextToken *string, maxResults *int, expand []string) (*api.EnumerateEndpointGroupsResponse, error) {
	f.gotToken, f.gotMax, f.gotExpand = nextToken, maxResults, expand
	return f.response, nil
}

type groupTestClient struct {
	apiclient.Client
	groups *fakeGroups
}

func (c *groupTestClient) Groups() apiclient.GroupsClient { return c.groups }

func hallway() api.EndpointGroup {
	geometry := map[string]interface{}{
		"relativeToGroupRouteId": "robot/map:0",
		"shape":                  map[string]interface{}{"type": "MultiPolygon", "coordinates": []interface{}{}},
	}
	members := []string{"ep/speaker"}
	controllers := []api.EndpointGroupController{{EndpointId: "ep/vacuum"}}
	attributes := []api.GroupRouteAttributeState{{GroupRouteId: "gr/1", Namespace: "port1/systems/zero/capability-interfaces/spatial", Name: "geometry", Value: &geometry}}
	return api.EndpointGroup{Id: "g/hall", Name: api.TypeValue{Type: "PLAIN", Value: "Hallway"}, MemberEndpointIds: &members, Controllers: &controllers, Attributes: &attributes}
}

func TestGroupListPrintsTheAPIGroupsAsJSON(t *testing.T) {
	next := "t2"
	groups := &fakeGroups{response: &api.EnumerateEndpointGroupsResponse{Results: []api.EndpointGroup{hallway()}, PaginationContext: &api.PaginationContext{NextToken: &next}}}
	formatter := &recordingFormatter{json: true}
	cmd := NewGroupCommand(&groupTestClient{groups: groups}, formatter)

	if err := cmd.List(t.Context(), GroupListParams{Expand: []string{"interfaces.attributes"}}); err != nil {
		t.Fatal(err)
	}
	if len(groups.gotExpand) != 1 || groups.gotExpand[0] != "interfaces.attributes" {
		t.Fatalf("expand %v", groups.gotExpand)
	}
	result, ok := formatter.printed[0].(GroupListResult)
	if !ok {
		t.Fatalf("printed %T", formatter.printed[0])
	}
	if len(result.Groups) != 1 || (*result.Groups[0].Attributes)[0].Name != "geometry" || *result.NextToken != "t2" {
		t.Fatalf("result %+v", result)
	}
}

func TestGroupListPrintsAnEmptyListNotNull(t *testing.T) {
	formatter := &recordingFormatter{json: true}
	cmd := NewGroupCommand(&groupTestClient{groups: &fakeGroups{response: &api.EnumerateEndpointGroupsResponse{}}}, formatter)
	if err := cmd.List(t.Context(), GroupListParams{}); err != nil {
		t.Fatal(err)
	}
	result := formatter.printed[0].(GroupListResult)
	if result.Groups == nil || result.NextToken != nil {
		t.Fatalf("result %+v", result)
	}
}

func TestGroupListTextNamesEachGroup(t *testing.T) {
	formatter := &recordingFormatter{}
	cmd := NewGroupCommand(&groupTestClient{groups: &fakeGroups{response: &api.EnumerateEndpointGroupsResponse{Results: []api.EndpointGroup{hallway()}}}}, formatter)
	if err := cmd.List(t.Context(), GroupListParams{}); err != nil {
		t.Fatal(err)
	}
	text := formatter.printed[0].(string)
	if !strings.Contains(text, "Hallway  g/hall  members 1, controllers 1") {
		t.Fatalf("text %q", text)
	}
}
