package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/portpowered/portos-cli/internal/api"
)

// EndpointsClientImpl implements the EndpointsClient interface
type EndpointsClientImpl struct {
	client *ClientImpl
}

// Enumerate lists all available endpoints for the authenticated user
func (e *EndpointsClientImpl) Enumerate(ctx context.Context, owner string, nextToken *string, maxResults *int, typeFilter *string, serialNumber *string) (*api.EndpointQueryResponse, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("owner", owner)

	if nextToken != nil && *nextToken != "" {
		params.Set("nextToken", *nextToken)
	}

	if maxResults != nil {
		params.Set("maxResults", fmt.Sprintf("%d", *maxResults))
	}

	if typeFilter != nil && *typeFilter != "" {
		params.Set("type", *typeFilter)
	}

	if serialNumber != nil && *serialNumber != "" {
		params.Set("serialNumber", *serialNumber)
	}

	// Build path with query parameters
	path := "/endpoints?" + params.Encode()

	// Perform request
	resp, err := e.client.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result api.EndpointQueryResponse
	if err := e.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetByID queries for an endpoint by its full ID
func (e *EndpointsClientImpl) GetByID(ctx context.Context, id string) (*api.EndpointQueryResponse, error) {
	return e.GetByIDWithExpand(ctx, id, "", false)
}

// GetByIDWithExpand queries for an endpoint by its full ID with optional expansion.
func (e *EndpointsClientImpl) GetByIDWithExpand(ctx context.Context, id string, expand string, forceDeviceAttributeQuery bool) (*api.EndpointQueryResponse, error) {
	// Build query parameters
	params := url.Values{}
	params.Set("id", id)
	if expand != "" {
		params.Set("expand", expand)
	}
	if forceDeviceAttributeQuery {
		params.Set("forceDeviceAttributeQuery", "true")
	}

	// Build path with query parameters
	path := "/endpoints?" + params.Encode()

	// Perform request
	resp, err := e.client.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	// Parse response
	var result api.EndpointQueryResponse
	if err := e.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}
