package commands

import (
	"context"
	"fmt"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/auth"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
	"go.uber.org/zap"
)

// RulesCommand handles access control rule operations commands
type RulesCommand struct {
	apiClient   apiclient.Client
	authService auth.Service
	formatter   output.Formatter
	logger      *logging.LoggerRef
}

// NewRulesCommand creates a new rules command
func NewRulesCommand(apiClient apiclient.Client, authService auth.Service, formatter output.Formatter, logger *logging.LoggerRef) *RulesCommand {
	return &RulesCommand{
		apiClient:   apiClient,
		authService: authService,
		formatter:   formatter,
		logger:      logger,
	}
}

// ListRulesParams contains parameters for the list rules command
type ListRulesParams struct {
	Owner      string
	MaxResults *int
	NextToken  string
}

// CreateRuleParams contains parameters for the create rule command
type CreateRuleParams struct {
	Resource  string
	Operation string
	Identity  string
	Effect    string
}

// DeleteRuleParams contains parameters for the delete rule command
type DeleteRuleParams struct {
	RuleID string
}

// RuleDisplay represents an access control rule for display purposes
type RuleDisplay struct {
	ID        string `json:"id"`
	Resource  string `json:"resource"`
	Operation string `json:"operation"`
	Identity  string `json:"identity"`
	Effect    string `json:"effect"`
	Owner     string `json:"owner,omitempty"`
}

// RuleListResult represents the result of listing rules
type RuleListResult struct {
	Rules []RuleDisplay `json:"rules"`
}

// List lists access control rules
func (c *RulesCommand) List(ctx context.Context, params ListRulesParams) error {
	queryParams := &api.GetAccessControlRulesParams{}
	if params.Owner != "" {
		queryParams.Owner = &params.Owner
	}
	if params.MaxResults != nil {
		queryParams.MaxResults = params.MaxResults
	}
	if params.NextToken != "" {
		queryParams.NextToken = &params.NextToken
	}

	response, err := c.apiClient.Rules().List(ctx, queryParams)
	if err != nil {
		return fmt.Errorf("failed to list access control rules: %w", err)
	}

	c.logger.Logger().Debug("rules listed", zap.Any("response", response))

	if c.formatter.IsJSON() {
		displays := make([]RuleDisplay, 0)
		for _, r := range response.Results {
			id := ""
			if r.Id != "" {
				id = r.Id
			}
			displays = append(displays, RuleDisplay{
				ID:        id,
				Resource:  r.Resource,
				Operation: r.Operation,
				Identity:  r.Identity,
				Effect:    string(r.Effect),
				Owner:     r.Owner,
			})
		}
		return c.formatter.Print(RuleListResult{Rules: displays})
	}

	// Human-readable output
	if len(response.Results) == 0 {
		return c.formatter.Print("No access control rules found\n")
	}

	c.formatter.Print("Access Control Rules:\n")
	c.formatter.Print(fmt.Sprintf("  %-40s  %-30s  %-20s  %-30s  %s\n", "ID", "RESOURCE", "OPERATION", "IDENTITY", "EFFECT"))
	for _, r := range response.Results {
		id := ""
		if r.Id != "" {
			id = r.Id
		}
		c.formatter.Print(fmt.Sprintf("  %-40s  %-30s  %-20s  %-30s  %s\n", id, r.Resource, r.Operation, r.Identity, r.Effect))
	}

	return nil
}

// Create creates a new access control rule
func (c *RulesCommand) Create(ctx context.Context, params CreateRuleParams) error {
	effect := api.AccessControlRuleEffect(params.Effect)

	request := &api.UpsertAccessControlRuleRequest{
		Resource:  params.Resource,
		Operation: params.Operation,
		Identity:  params.Identity,
		Effect:    effect,
	}

	response, err := c.apiClient.Rules().Create(ctx, request)
	if err != nil {
		return fmt.Errorf("failed to create access control rule: %w", err)
	}

	c.logger.Logger().Info("rule created successfully", zap.String("rule_id", response.Id))

	if c.formatter.IsJSON() {
		result := struct {
			RuleID  string `json:"ruleId"`
			Message string `json:"message"`
		}{
			RuleID:  response.Id,
			Message: "Access control rule created successfully",
		}
		return c.formatter.Print(result)
	}

	return c.formatter.Print(fmt.Sprintf("Access control rule created successfully: %s\n", response.Id))
}

// Delete deletes an access control rule by ID
func (c *RulesCommand) Delete(ctx context.Context, params DeleteRuleParams) error {
	err := c.apiClient.Rules().Delete(ctx, params.RuleID)
	if err != nil {
		return fmt.Errorf("failed to delete access control rule '%s': %w", params.RuleID, err)
	}

	c.logger.Logger().Info("rule deleted successfully", zap.String("rule_id", params.RuleID))

	if c.formatter.IsJSON() {
		result := struct {
			RuleID  string `json:"ruleId"`
			Message string `json:"message"`
		}{
			RuleID:  params.RuleID,
			Message: "Access control rule deleted successfully",
		}
		return c.formatter.Print(result)
	}

	return c.formatter.Print(fmt.Sprintf("Access control rule deleted successfully: %s\n", params.RuleID))
}
