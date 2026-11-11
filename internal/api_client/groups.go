package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
)

// GroupsClientImpl implements the GroupsClient interface.
type GroupsClientImpl struct {
	client *ClientImpl
}

// Enumerate lists one page of the caller's endpoint groups.
func (g *GroupsClientImpl) Enumerate(ctx context.Context, nextToken *string, maxResults *int, expand []string) (*api.EnumerateEndpointGroupsResponse, error) {
	params := url.Values{}
	if nextToken != nil && *nextToken != "" {
		params.Set("nextToken", *nextToken)
	}
	if maxResults != nil {
		params.Set("maxResults", fmt.Sprintf("%d", *maxResults))
	}
	if len(expand) > 0 {
		params.Set("expand", strings.Join(expand, ","))
	}
	path := "/endpoint-groups"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := g.client.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}
	var result api.EnumerateEndpointGroupsResponse
	if err := g.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
