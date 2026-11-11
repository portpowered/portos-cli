package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/portpowered/portos-cli/internal/api"
)

// RulesClientImpl implements the RulesClient interface
type RulesClientImpl struct {
	client *ClientImpl
}

// List lists access control rules with optional filtering and pagination
func (r *RulesClientImpl) List(ctx context.Context, params *api.GetAccessControlRulesParams) (*api.GetAccessControlRulesResponse, error) {
	path := "/access-control-rules"
	queryParams := []string{}
	if params != nil {
		if params.Owner != nil && *params.Owner != "" {
			queryParams = append(queryParams, "owner="+url.QueryEscape(*params.Owner))
		}
		for _, f := range []struct {
			name  string
			value *string
		}{{"id", params.Id}, {"operation", params.Operation}, {"resource", params.Resource}, {"identity", params.Identity}} {
			if f.value != nil && *f.value != "" {
				queryParams = append(queryParams, f.name+"="+url.QueryEscape(*f.value))
			}
		}
		if params.NextToken != nil && *params.NextToken != "" {
			queryParams = append(queryParams, "nextToken="+url.QueryEscape(*params.NextToken))
		}
		if params.MaxResults != nil {
			queryParams = append(queryParams, fmt.Sprintf("maxResults=%d", *params.MaxResults))
		}
	}
	if len(queryParams) > 0 {
		path += "?" + strings.Join(queryParams, "&")
	}

	resp, err := r.client.doRequest(ctx, http.MethodGet, path, nil, nil)
	if err != nil {
		return nil, err
	}

	var result api.GetAccessControlRulesResponse
	if err := r.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Create creates a new access control rule
func (r *RulesClientImpl) Create(ctx context.Context, request *api.UpsertAccessControlRuleRequest) (*api.AccessControlRule, error) {
	if request == nil {
		return nil, fmt.Errorf("access control rule is required")
	}
	headers := map[string]string{"Idempotency-Key": uuid.NewString()}
	if request.Id != nil && *request.Id != "" {
		etag, err := r.currentVersion(ctx, *request.Id)
		if err != nil {
			return nil, err
		}
		headers["If-Match"] = etag
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	resp, err := r.client.doRequest(ctx, http.MethodPost, "/access-control-rules", strings.NewReader(string(body)), headers)
	if err != nil {
		return nil, err
	}

	var result api.AccessControlRule
	if err := r.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// Delete deletes an access control rule by its full ID.
// ruleID is the full port identifier path (e.g. port1/principals/abc/access-control-rules/def).
func (r *RulesClientImpl) Delete(ctx context.Context, ruleID string) error {
	return r.DeleteWithReason(ctx, ruleID, "")
}

// DeleteWithReason deletes a rule and records the operator-supplied reason in the audit log.
func (r *RulesClientImpl) DeleteWithReason(ctx context.Context, ruleID, reason string) error {
	etag, err := r.currentVersion(ctx, ruleID)
	if err != nil {
		return err
	}
	path := ruleID
	if reason != "" {
		path += "?reason=" + url.QueryEscape(reason)
	}
	resp, err := r.client.doRequest(ctx, http.MethodDelete, path, nil, map[string]string{
		"If-Match": etag, "Idempotency-Key": uuid.NewString(),
	})
	if err != nil {
		return err
	}
	return r.client.handleResponse(resp, nil)
}

func (r *RulesClientImpl) currentVersion(ctx context.Context, ruleID string) (string, error) {
	parts := strings.Split(ruleID, "/")
	if len(parts) != 5 || parts[0] != "port1" || parts[3] != "access-control-rules" || parts[1] == "" || parts[2] == "" || parts[4] == "" {
		return "", fmt.Errorf("access control rule ID must be a full resource identifier")
	}
	page, err := r.List(ctx, &api.GetAccessControlRulesParams{Id: &ruleID})
	if err != nil {
		return "", err
	}
	if len(page.Results) != 1 || page.Results[0].Id != ruleID || page.Results[0].Etag == nil || *page.Results[0].Etag == "" {
		return "", fmt.Errorf("access control rule response is missing the exact resource and its etag")
	}
	return *page.Results[0].Etag, nil
}

// Evaluate evaluates an operation for a requester against the stored rules.
func (r *RulesClientImpl) Evaluate(ctx context.Context, request *api.EvaluateAccessControlRuleRequest) (*api.EvaluateAccessControlRuleResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := r.client.doRequest(ctx, http.MethodPost, "/access-control-rules/evaluate", strings.NewReader(string(body)), nil)
	if err != nil {
		return nil, err
	}
	var result api.EvaluateAccessControlRuleResponse
	if err := r.client.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
