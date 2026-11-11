package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/portpowered/portos-cli/internal/api"
	apiclient "github.com/portpowered/portos-cli/internal/api_client"
	"github.com/portpowered/portos-cli/internal/config"
	"github.com/portpowered/portos-cli/internal/logging"
	"github.com/portpowered/portos-cli/internal/output"
)

type fakeRules struct {
	calls     []string
	created   []*api.UpsertAccessControlRuleRequest
	listed    []api.AccessControlRule
	deleted   []string
	reasons   []string
	evaluates []*api.EvaluateAccessControlRuleRequest
	permits   func() api.EvaluateAccessControlRuleResult

	listOwners []string
	failOwner  string
}

const callerOwner = "port1/principals/operator"

func (f *fakeRules) List(_ context.Context, params *api.GetAccessControlRulesParams) (*api.GetAccessControlRulesResponse, error) {
	f.calls = append(f.calls, "list")
	if params.Identity == nil || params.Resource == nil || params.Operation == nil ||
		*params.Operation != pluginEstablishLinkOperation ||
		*params.Resource != "port1/systems/zero/plugins/tuya" || *params.Identity != "port1/principals/alice" {
		return nil, fmt.Errorf("unexpected list filters: %+v", params)
	}
	owner := callerOwner
	if params.Owner != nil {
		owner = *params.Owner
	}
	f.listOwners = append(f.listOwners, owner)
	if f.failOwner != "" && f.failOwner == owner {
		return nil, fmt.Errorf("403 forbidden")
	}
	out := []api.AccessControlRule{}
	for _, r := range f.listed {
		ruleOwner := r.Owner
		if ruleOwner == "" {
			ruleOwner = callerOwner
		}
		if ruleOwner != owner {
			continue
		}
		gone := false
		for _, d := range f.deleted {
			gone = gone || (r.Id != "" && r.Id == d)
		}
		if !gone {
			out = append(out, r)
		}
	}
	return &api.GetAccessControlRulesResponse{Results: out}, nil
}

func (f *fakeRules) Create(_ context.Context, r *api.UpsertAccessControlRuleRequest) (*api.AccessControlRule, error) {
	f.calls = append(f.calls, "create")
	f.created = append(f.created, r)
	return &api.AccessControlRule{Id: "rule-new"}, nil
}

func (f *fakeRules) Delete(_ context.Context, id string) error {
	return f.DeleteWithReason(nil, id, "")
}

func (f *fakeRules) DeleteWithReason(_ context.Context, id, reason string) error {
	f.calls = append(f.calls, "delete")
	f.deleted = append(f.deleted, id)
	f.reasons = append(f.reasons, reason)
	return nil
}

func (f *fakeRules) Evaluate(_ context.Context, r *api.EvaluateAccessControlRuleRequest) (*api.EvaluateAccessControlRuleResponse, error) {
	f.calls = append(f.calls, "evaluate")
	f.evaluates = append(f.evaluates, r)
	res := api.EvaluateAccessControlRuleResultALLOW
	if f.permits != nil {
		res = f.permits()
	}
	return &api.EvaluateAccessControlRuleResponse{Result: &res}, nil
}

type fakeClient struct {
	apiclient.Client
	rules   *fakeRules
	baseURL string
}

func (c *fakeClient) RulesFor(baseURL string) apiclient.RulesClient {
	c.baseURL = baseURL
	return c.rules
}

type dirResolver string

func (d dirResolver) ConfigDir() string { return string(d) }

type establishHarness struct {
	cmd    *EstablishCommand
	client *fakeClient
	stdout *testWriter
}

func newEstablishHarness(t *testing.T, rules *fakeRules) *establishHarness {
	t.Helper()
	dir := t.TempDir()
	cfg := "environments:\n  pilot:\n    apiUrl: https://pilot.example.com/\n"
	if err := os.WriteFile(filepath.Join(dir, config.ConfigFileName), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	w := newTestWriter()
	logger := logging.NewLoggerRef()
	client := &fakeClient{rules: rules}
	cmd := NewEstablishCommand(client, config.NewManagerProvider(dirResolver(dir)), output.NewJSONFormatter(w, logger), logger)
	return &establishHarness{cmd: cmd, client: client, stdout: w}
}

func baseParams() EstablishParams {
	return EstablishParams{Environment: "pilot", Plugin: "tuya", AuthVariant: "TUYA_OAUTH", Principal: "port1/principals/alice"}
}

func TestEstablishRequiresKnownEnvironmentBeforeAnyHTTP(t *testing.T) {
	for name, env := range map[string]string{"missing": "", "unknown": "staging"} {
		t.Run(name, func(t *testing.T) {
			rules := &fakeRules{}
			h := newEstablishHarness(t, rules)
			p := baseParams()
			p.Environment = env
			for opName, op := range map[string]func(context.Context, EstablishParams) error{
				"grant": h.cmd.Grant, "revoke": h.cmd.Revoke, "check": h.cmd.Check, "list": h.cmd.List,
			} {
				if err := op(context.Background(), p); err == nil {
					t.Fatalf("%s: expected an error for %s environment", opName, name)
				}
			}
			if len(rules.calls) != 0 || h.client.baseURL != "" {
				t.Fatalf("no HTTP client may be used; calls=%v baseURL=%q", rules.calls, h.client.baseURL)
			}
		})
	}
}

func TestEstablishGrantSendsExactConditionsThenEvaluates(t *testing.T) {
	cases := map[string]struct {
		region        string
		wantConds     []api.AccessControlRuleCondition
		wantEvalRegio string
	}{
		"variant only":      {"", []api.AccessControlRuleCondition{{Key: api.AUTHVARIANT, Comparator: api.EQUALS, Value: "TUYA_OAUTH"}}, "global"},
		"explicit wildcard": {"*", []api.AccessControlRuleCondition{{Key: api.AUTHVARIANT, Comparator: api.EQUALS, Value: "TUYA_OAUTH"}}, "global"},
		"with region": {"eu", []api.AccessControlRuleCondition{
			{Key: api.AUTHVARIANT, Comparator: api.EQUALS, Value: "TUYA_OAUTH"},
			{Key: api.REGIONALIZATION, Comparator: api.EQUALS, Value: "eu"},
		}, "eu"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rules := &fakeRules{}
			h := newEstablishHarness(t, rules)
			p := baseParams()
			p.Regionalization, p.Reason = tc.region, "ticket-1"
			if err := h.cmd.Grant(context.Background(), p); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(rules.calls, ","); got != "create,evaluate" {
				t.Fatalf("call order = %s", got)
			}
			req := rules.created[0]
			if req.Resource != "port1/systems/zero/plugins/tuya" || req.Identity != p.Principal ||
				req.Operation != pluginEstablishLinkOperation || req.Effect != api.AccessControlRuleEffectALLOW {
				t.Fatalf("unexpected rule: %+v", req)
			}
			gotConds, _ := json.Marshal(req.Conditions)
			wantConds, _ := json.Marshal(&tc.wantConds)
			if string(gotConds) != string(wantConds) {
				t.Fatalf("conditions = %s, want %s", gotConds, wantConds)
			}
			if req.Reason == nil || *req.Reason != "ticket-1" {
				t.Fatalf("reason not sent: %+v", req.Reason)
			}
			ev := rules.evaluates[0]
			if *ev.OperationContext.Regionalization != tc.wantEvalRegio || *ev.OperationContext.AuthVariant != "TUYA_OAUTH" ||
				ev.RequesterContext.RequesterId != p.Principal {
				t.Fatalf("unexpected evaluate request: %+v", ev)
			}
			if h.client.baseURL != "https://pilot.example.com" {
				t.Fatalf("base URL = %q", h.client.baseURL)
			}
			if printedOutput(t, h)["permission"] != "ALLOW" {
				t.Fatalf("output lacks the readback: %s", h.stdout.stdout.String())
			}
		})
	}
}

func TestEstablishDryRunMakesNoWritesAndPrintsJSON(t *testing.T) {
	rules := &fakeRules{listed: []api.AccessControlRule{ruleWith("r1", "TUYA_OAUTH", "")}}
	h := newEstablishHarness(t, rules)
	p := baseParams()
	p.DryRun = true

	for name, op := range map[string]func(context.Context, EstablishParams) error{"grant": h.cmd.Grant, "revoke": h.cmd.Revoke} {
		h.stdout.stdout.Reset()
		if err := op(context.Background(), p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var printed map[string]any
		if err := json.Unmarshal(h.stdout.stdout.Bytes(), &printed); err != nil {
			t.Fatalf("%s: output is not JSON: %v\n%s", name, err, h.stdout.stdout.String())
		}
		if printed["dryRun"] != true || printed["evaluate"] == nil {
			t.Fatalf("%s: dry run output incomplete: %v", name, printed)
		}
	}
	for _, call := range rules.calls {
		if call == "create" || call == "delete" || call == "evaluate" {
			t.Fatalf("dry run performed %q; calls=%v", call, rules.calls)
		}
	}
}

func printedOutput(t *testing.T, h *establishHarness) map[string]any {
	t.Helper()
	var printed map[string]any
	if err := json.Unmarshal(h.stdout.stdout.Bytes(), &printed); err != nil {
		t.Fatalf("output is not JSON: %v: %s", err, h.stdout.stdout.String())
	}
	return printed
}

func ruleWith(id, variant, region string) api.AccessControlRule {
	conds := []api.AccessControlRuleCondition{{Key: api.AUTHVARIANT, Comparator: api.EQUALS, Value: variant}}
	if region != "" {
		conds = append(conds, api.AccessControlRuleCondition{Key: api.REGIONALIZATION, Comparator: api.EQUALS, Value: region})
	}
	return api.AccessControlRule{Id: id, Effect: api.AccessControlRuleEffectALLOW, Conditions: conds}
}

func TestEstablishRevokeDeletesEveryMatchAndReadsBack(t *testing.T) {
	rules := &fakeRules{
		listed: []api.AccessControlRule{
			ruleWith("r1", "TUYA_OAUTH", ""), ruleWith("r2", "TUYA_OAUTH", ""),
			ruleWith("other-variant", "OTHER", ""), ruleWith("regional", "TUYA_OAUTH", "eu"),
		},
		permits: func() api.EvaluateAccessControlRuleResult { return api.EvaluateAccessControlRuleResultDENY },
	}
	h := newEstablishHarness(t, rules)
	p := baseParams()
	p.Reason = "cleanup"
	if err := h.cmd.Revoke(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rules.deleted, ",") != "r1,r2" {
		t.Fatalf("deleted = %v", rules.deleted)
	}
	if rules.reasons[0] != "cleanup" {
		t.Fatalf("reason not forwarded: %v", rules.reasons)
	}
	if rules.calls[len(rules.calls)-1] != "evaluate" {
		t.Fatalf("revoke must end with an evaluate readback: %v", rules.calls)
	}
}

func TestEstablishRevokeReportsRemainingAllow(t *testing.T) {
	rules := &fakeRules{
		listed: []api.AccessControlRule{ruleWith("r1", "TUYA_OAUTH", ""), ruleWith("regional", "TUYA_OAUTH", "global")},
	}
	h := newEstablishHarness(t, rules)
	err := h.cmd.Revoke(context.Background(), baseParams())
	if err == nil || !strings.Contains(err.Error(), "regional") {
		t.Fatalf("expected an error naming the remaining rule, got %v", err)
	}
	if got, _ := printedOutput(t, h)["remainingAllowRuleIds"].([]any); len(got) != 1 || got[0] != "regional" {
		t.Fatalf("output lacks remaining rule: %s", h.stdout.stdout.String())
	}
}

func TestEstablishListAndRevokeIncludeSystemZeroOwnedRules(t *testing.T) {
	sysRule := ruleWith("sys-1", "TUYA_OAUTH", "")
	sysRule.Owner = "port1/systems/zero"
	dupSys := ruleWith("r1", "TUYA_OAUTH", "")
	dupSys.Owner = "port1/systems/zero"
	rules := &fakeRules{listed: []api.AccessControlRule{ruleWith("r1", "TUYA_OAUTH", ""), sysRule, dupSys},
		permits: func() api.EvaluateAccessControlRuleResult { return api.EvaluateAccessControlRuleResultDENY }}
	h := newEstablishHarness(t, rules)

	if err := h.cmd.List(context.Background(), baseParams()); err != nil {
		t.Fatal(err)
	}
	listed, _ := printedOutput(t, h)["rules"].([]any)
	if len(listed) != 2 {
		t.Fatalf("list should merge caller and system-zero rules by ID, got %d", len(listed))
	}
	if strings.Join(rules.listOwners, ",") != callerOwner+",port1/systems/zero" {
		t.Fatalf("owners queried = %v", rules.listOwners)
	}

	if err := h.cmd.Revoke(context.Background(), baseParams()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rules.deleted, ",") != "r1,sys-1" {
		t.Fatalf("deleted = %v", rules.deleted)
	}
}

func TestEstablishSystemZeroListFailureFailsCommandNamingOwner(t *testing.T) {
	rules := &fakeRules{failOwner: "port1/systems/zero"}
	h := newEstablishHarness(t, rules)
	for name, op := range map[string]func(context.Context, EstablishParams) error{"list": h.cmd.List, "revoke": h.cmd.Revoke} {
		err := op(context.Background(), baseParams())
		if err == nil || !strings.Contains(err.Error(), "port1/systems/zero") {
			t.Fatalf("%s: expected an error naming the owner, got %v", name, err)
		}
	}
	if len(rules.deleted) != 0 {
		t.Fatal("nothing may be deleted when a list fails")
	}
}

func TestEstablishRevokeSkipsRulesWithExtraConditions(t *testing.T) {
	extra := ruleWith("extra", "TUYA_OAUTH", "")
	extra.Conditions = append(extra.Conditions, api.AccessControlRuleCondition{Key: api.TARGETOPERATION, Comparator: api.EQUALS, Value: "x"})
	rules := &fakeRules{listed: []api.AccessControlRule{ruleWith("r1", "TUYA_OAUTH", ""), extra},
		permits: func() api.EvaluateAccessControlRuleResult { return api.EvaluateAccessControlRuleResultDENY }}
	h := newEstablishHarness(t, rules)
	if err := h.cmd.Revoke(context.Background(), baseParams()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rules.deleted, ",") != "r1" {
		t.Fatalf("only the exact match may be deleted, got %v", rules.deleted)
	}
}

func TestEstablishRevokeDryRunLabelsWouldDelete(t *testing.T) {
	rules := &fakeRules{listed: []api.AccessControlRule{ruleWith("r1", "TUYA_OAUTH", "")}}
	h := newEstablishHarness(t, rules)
	p := baseParams()
	p.DryRun = true
	if err := h.cmd.Revoke(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	out := printedOutput(t, h)
	if out["deletedRuleIds"] != nil || out["wouldDeleteRuleIds"] == nil {
		t.Fatalf("dry run must report wouldDeleteRuleIds only: %v", out)
	}
}

func TestEstablishRemainingIgnoresOtherRegions(t *testing.T) {
	rules := &fakeRules{listed: []api.AccessControlRule{ruleWith("eu-only", "TUYA_OAUTH", "eu")}}
	h := newEstablishHarness(t, rules)
	_ = h.cmd.Revoke(context.Background(), baseParams()) // the evaluate fake still answers ALLOW
	got, _ := printedOutput(t, h)["remainingAllowRuleIds"].([]any)
	if len(got) != 0 {
		t.Fatalf("a rule for another region cannot permit the probe region: %v", got)
	}
}
