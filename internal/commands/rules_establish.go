package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

const (
	// pluginResourcePrefix addresses a plugin by ID the way establishment rules do.
	pluginResourcePrefix = "port1/systems/zero/plugins/"
	// unconstrainedRegionalization given to --regionalization means no REGIONALIZATION condition.
	unconstrainedRegionalization = "*"
	// probeRegionalization is the regionalization an evaluate request carries when none is given:
	// the server requires a concrete value for plugin-establish-link, and a rule without a
	// REGIONALIZATION condition applies to every region.
	probeRegionalization = "global"
	// pluginEstablishLinkOperation is the operation the public API carries as a plain string; the
	// public projection does not publish the AccessControlOperation enum.
	pluginEstablishLinkOperation = "port1/systems/zero/operations/plugin-establish-link"
	// systemZeroOwner owns every plugin resource.
	systemZeroOwner   = "port1/systems/zero"
	establishPageSize = 100
)

// EstablishCommand manages the rules that let a principal establish a plugin auth link.
type EstablishCommand struct {
	apiClient      apiclient.Client
	configProvider *config.ManagerProvider
	formatter      output.Formatter
	logger         *logging.LoggerRef
}

// NewEstablishCommand creates the establish rules command.
func NewEstablishCommand(apiClient apiclient.Client, configProvider *config.ManagerProvider, formatter output.Formatter, logger *logging.LoggerRef) *EstablishCommand {
	return &EstablishCommand{apiClient: apiClient, configProvider: configProvider, formatter: formatter, logger: logger}
}

// EstablishParams are the flags shared by every establish subcommand.
type EstablishParams struct {
	Environment     string
	Plugin          string
	AuthVariant     string
	Principal       string
	Regionalization string // empty means not given; "*" is applied only when given explicitly
	DryRun          bool
	Reason          string
}

// EstablishResult is the printed outcome of an establish command.
type EstablishResult struct {
	Environment string                                `json:"environment"`
	DryRun      bool                                  `json:"dryRun,omitempty"`
	Rule        *api.UpsertAccessControlRuleRequest   `json:"rule,omitempty"`
	Evaluate    *api.EvaluateAccessControlRuleRequest `json:"evaluate,omitempty"`
	RuleID      string                                `json:"ruleId,omitempty"`
	DeletedIDs  []string                              `json:"deletedRuleIds,omitempty"`
	WouldDelete []string                              `json:"wouldDeleteRuleIds,omitempty"`
	Permission  string                                `json:"permission,omitempty"`
	Remaining   []string                              `json:"remainingAllowRuleIds,omitempty"`
	Rules       []api.AccessControlRule               `json:"rules,omitempty"`
}

// pluginResource maps a plugin ID (or a full plugin resource path) to the rule resource.
func pluginResource(plugin string) string {
	if strings.Contains(plugin, "/") {
		return plugin
	}
	return pluginResourcePrefix + plugin
}

// validate rejects a call before any network use when a required flag is missing.
func (p EstablishParams) validate() error {
	for _, f := range []struct{ name, value string }{
		{"environment", p.Environment}, {"plugin", p.Plugin}, {"auth-variant", p.AuthVariant}, {"principal", p.Principal},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("--%s is required", f.name)
		}
	}
	return nil
}

// region returns the regionalization the rule is constrained to, or "" when unconstrained.
func (p EstablishParams) region() string {
	if p.Regionalization == unconstrainedRegionalization {
		return ""
	}
	return p.Regionalization
}

// conditions returns the rule conditions: AUTH_VARIANT, plus REGIONALIZATION only when given.
func (p EstablishParams) conditions() []api.AccessControlRuleCondition {
	conds := []api.AccessControlRuleCondition{{Key: api.AUTHVARIANT, Comparator: api.EQUALS, Value: p.AuthVariant}}
	if p.region() != "" {
		conds = append(conds, api.AccessControlRuleCondition{Key: api.REGIONALIZATION, Comparator: api.EQUALS, Value: p.region()})
	}
	return conds
}

func (p EstablishParams) ruleRequest() *api.UpsertAccessControlRuleRequest {
	conds := p.conditions()
	req := &api.UpsertAccessControlRuleRequest{
		Identity:   p.Principal,
		Resource:   pluginResource(p.Plugin),
		Operation:  pluginEstablishLinkOperation,
		Effect:     api.AccessControlRuleEffectALLOW,
		Conditions: &conds,
	}
	if p.Reason != "" {
		reason := p.Reason
		req.Reason = &reason
	}
	return req
}

// evaluateRequest asks whether the principal may establish the plugin link in the given context.
func (p EstablishParams) evaluateRequest() *api.EvaluateAccessControlRuleRequest {
	operation := pluginEstablishLinkOperation
	regionalization := p.region()
	if regionalization == "" {
		regionalization = probeRegionalization
	}
	variant := p.AuthVariant
	entities := []api.AccessControlEntity{{Id: pluginResource(p.Plugin), Type: api.Plugins, Owner: systemZeroOwner}}
	return &api.EvaluateAccessControlRuleRequest{
		Entities:         entities,
		Operation:        operation,
		OperationContext: &api.AccessControlOperationContext{AuthVariant: &variant, Regionalization: &regionalization},
		RequesterContext: api.AccessControlRequesterContext{RequesterId: p.Principal},
		ResourceContext:  api.AccessControlResourceContext{ResourceId: pluginResource(p.Plugin)},
	}
}

// target validates the flags, then resolves the environment and its rules client. No HTTP happens here.
func (c *EstablishCommand) target(p EstablishParams) (apiclient.RulesClient, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	mgr, err := c.configProvider.GetManager()
	if err != nil {
		return nil, err
	}
	env, err := mgr.LoadEnvironment(p.Environment)
	if err != nil {
		return nil, err
	}
	return c.apiClient.RulesFor(env.APIURL), nil
}

func (c *EstablishCommand) readback(ctx context.Context, rules apiclient.RulesClient, p EstablishParams) (string, error) {
	resp, err := rules.Evaluate(ctx, p.evaluateRequest())
	if err != nil {
		return "", fmt.Errorf("failed to evaluate establish access: %w", err)
	}
	if resp.Result == nil {
		return "", fmt.Errorf("evaluate returned no result")
	}
	return string(*resp.Result), nil
}

// Grant creates the ALLOW rule, then prints the evaluate readback.
func (c *EstablishCommand) Grant(ctx context.Context, p EstablishParams) error {
	rules, err := c.target(p)
	if err != nil {
		return err
	}
	result := EstablishResult{Environment: p.Environment, Rule: p.ruleRequest()}
	if p.DryRun {
		result.DryRun, result.Evaluate = true, p.evaluateRequest()
		return c.formatter.Print(result)
	}
	created, err := rules.Create(ctx, result.Rule)
	if err != nil {
		return fmt.Errorf("failed to create establish rule: %w", err)
	}
	result.RuleID = created.Id
	if result.Permission, err = c.readback(ctx, rules, p); err != nil {
		return err
	}
	return c.formatter.Print(result)
}

// Check prints whether the principal may establish the plugin link.
func (c *EstablishCommand) Check(ctx context.Context, p EstablishParams) error {
	rules, err := c.target(p)
	if err != nil {
		return err
	}
	permission, err := c.readback(ctx, rules, p)
	if err != nil {
		return err
	}
	return c.formatter.Print(EstablishResult{Environment: p.Environment, Evaluate: p.evaluateRequest(), Permission: permission})
}

// listPrincipalRules pages through every visible establish rule for the principal and plugin. It
// queries the rules owned by the caller and the rules owned by system zero, merged by rule ID.
func (c *EstablishCommand) listPrincipalRules(ctx context.Context, rules apiclient.RulesClient, p EstablishParams) ([]api.AccessControlRule, error) {
	var all []api.AccessControlRule
	seen := map[string]bool{}
	for _, owner := range []string{"", systemZeroOwner} {
		owned, err := c.listOwnedRules(ctx, rules, p, owner)
		if err != nil {
			return nil, err
		}
		for _, r := range owned {
			if r.Id != "" {
				if seen[r.Id] {
					continue
				}
				seen[r.Id] = true
			}
			all = append(all, r)
		}
	}
	return all, nil
}

// listOwnedRules pages through the rules of one owner; an empty owner means the caller.
func (c *EstablishCommand) listOwnedRules(ctx context.Context, rules apiclient.RulesClient, p EstablishParams, owner string) ([]api.AccessControlRule, error) {
	operation, resource, identity := pluginEstablishLinkOperation, pluginResource(p.Plugin), p.Principal
	maxResults := establishPageSize
	params := &api.GetAccessControlRulesParams{Operation: &operation, Resource: &resource, Identity: &identity, MaxResults: &maxResults}
	label := "the caller"
	if owner != "" {
		params.Owner, label = &owner, owner
	}
	var all []api.AccessControlRule
	for {
		page, err := rules.List(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("failed to list establish rules owned by %s: %w", label, err)
		}
		all = append(all, page.Results...)
		if page.PaginationContext == nil || page.PaginationContext.NextToken == nil || *page.PaginationContext.NextToken == "" {
			return all, nil
		}
		params.NextToken = page.PaginationContext.NextToken
	}
}

// List prints the rules for the principal and plugin that match the auth variant (and regionalization if given).
func (c *EstablishCommand) List(ctx context.Context, p EstablishParams) error {
	rules, err := c.target(p)
	if err != nil {
		return err
	}
	all, err := c.listPrincipalRules(ctx, rules, p)
	if err != nil {
		return err
	}
	matching := make([]api.AccessControlRule, 0, len(all))
	for _, r := range all {
		if ruleMatches(r, p) {
			matching = append(matching, r)
		}
	}
	return c.formatter.Print(EstablishResult{Environment: p.Environment, Rules: matching})
}

// ruleMatches reports whether r is an ALLOW rule whose complete condition set equals the params
// conditions (key, comparator and value).
func ruleMatches(r api.AccessControlRule, p EstablishParams) bool {
	if r.Effect != api.AccessControlRuleEffectALLOW {
		return false
	}
	want := p.conditions()
	if len(r.Conditions) != len(want) {
		return false
	}
	for _, w := range want {
		found := false
		for _, got := range r.Conditions {
			found = found || got == w
		}
		if !found {
			return false
		}
	}
	return true
}

// permitsProbe reports whether an ALLOW rule with only AUTH_VARIANT and REGIONALIZATION conditions would
// permit the evaluated context (the auth variant and the requested or probe region).
func permitsProbe(r api.AccessControlRule, p EstablishParams) bool {
	if r.Effect != api.AccessControlRuleEffectALLOW {
		return false
	}
	region := p.region()
	if region == "" {
		region = probeRegionalization
	}
	variantOK := false
	for _, cond := range r.Conditions {
		switch {
		case cond.Key == api.AUTHVARIANT && cond.Comparator == api.EQUALS:
			variantOK = cond.Value == p.AuthVariant
		case cond.Key == api.REGIONALIZATION && cond.Comparator == api.EQUALS:
			if cond.Value != region {
				return false
			}
		default:
			return false
		}
	}
	return variantOK
}

// Revoke deletes every matching ALLOW rule the operator can see, then reads back.
// It prints the remaining ALLOW rule IDs and fails when permission is still ALLOW.
func (c *EstablishCommand) Revoke(ctx context.Context, p EstablishParams) error {
	rules, err := c.target(p)
	if err != nil {
		return err
	}
	all, err := c.listPrincipalRules(ctx, rules, p)
	if err != nil {
		return err
	}
	result := EstablishResult{Environment: p.Environment}
	for _, r := range all {
		if r.Id != "" && ruleMatches(r, p) {
			result.DeletedIDs = append(result.DeletedIDs, r.Id)
		}
	}
	if p.DryRun {
		result.DryRun, result.Evaluate = true, p.evaluateRequest()
		result.WouldDelete, result.DeletedIDs = result.DeletedIDs, nil
		return c.formatter.Print(result)
	}
	for _, id := range result.DeletedIDs {
		if err := rules.DeleteWithReason(ctx, id, p.Reason); err != nil {
			return fmt.Errorf("failed to delete rule %q: %w", id, err)
		}
	}
	if result.Permission, err = c.readback(ctx, rules, p); err != nil {
		return err
	}
	if result.Permission != string(api.EvaluateAccessControlRuleResultALLOW) {
		return c.formatter.Print(result)
	}
	if result.Remaining, err = c.remainingAllow(ctx, rules, p); err != nil {
		return err
	}
	if printErr := c.formatter.Print(result); printErr != nil {
		return printErr
	}
	return fmt.Errorf("permission is still ALLOW after revoke; remaining visible ALLOW rules: %v", result.Remaining)
}

// remainingAllow lists the visible ALLOW rules for this variant that still permit the principal.
func (c *EstablishCommand) remainingAllow(ctx context.Context, rules apiclient.RulesClient, p EstablishParams) ([]string, error) {
	all, err := c.listPrincipalRules(ctx, rules, p)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, r := range all {
		if r.Id != "" && permitsProbe(r, p) {
			ids = append(ids, r.Id)
		}
	}
	return ids, nil
}
