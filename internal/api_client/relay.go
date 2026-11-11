package api

import (
	"context"
	"net/http"

	"github.com/portpowered/portos-cli/internal/api"
)

// RelayClientImpl implements the RelayClient interface.
type RelayClientImpl struct {
	client *ClientImpl
}

// Credentials fetches short-lived TURN credentials for the Port OS media relay.
func (r *RelayClientImpl) Credentials(ctx context.Context) (*api.RelayCredentials, error) {
	resp, err := r.client.doRequest(ctx, http.MethodGet, "/relay-credentials", nil, nil)
	if err != nil {
		return nil, err
	}
	var result api.RelayCredentials
	if err := r.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
