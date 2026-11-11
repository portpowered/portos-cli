package commands

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/portpowered/portos-cli/internal/api"
	"github.com/portpowered/portos-cli/internal/output"
	"github.com/stretchr/testify/require"
)

// captureFormatter is a test formatter that captures printed output.
type captureFormatter struct {
	captured string
}

func (f *captureFormatter) Print(data interface{}) error {
	f.captured = data.(string)
	return nil
}

func (f *captureFormatter) PrintError(err error) error { return nil }
func (f *captureFormatter) IsJSON() bool               { return false }
func (f *captureFormatter) IsQuiet() bool              { return false }

var _ output.Formatter = (*captureFormatter)(nil)

func TestEndpointDisplay_JSONContainsID(t *testing.T) {
	display := EndpointDisplay{
		ID:   "port1/systems/zero/endpoints/abc-123",
		Name: "Test Endpoint",
		Type: "device",
	}

	data, err := json.Marshal(display)
	if err != nil {
		t.Fatalf("failed to marshal EndpointDisplay: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	id, ok := result["id"]
	if !ok {
		t.Fatal("expected 'id' field in JSON output, but it was missing")
	}
	if id != "port1/systems/zero/endpoints/abc-123" {
		t.Errorf("expected id 'port1/systems/zero/endpoints/abc-123', got %q", id)
	}

	// short_id should NOT be present
	if _, ok := result["short_id"]; ok {
		t.Error("expected no 'short_id' field in JSON output, but it was present")
	}
}

// TestEndpointShow_JSONContainsFullID verifies that the endpoint show command's
// JSON output (which serializes a single EndpointDisplay) includes the full ID.
// The Show command reuses EndpointDisplay via transformEndpoint(), so this
// confirms the US-001 fix applies to endpoint show as well as endpoint list.
func TestEndpointShow_JSONContainsFullID(t *testing.T) {
	manufacturer := "Philips"
	model := "Hue White"
	serial := "12345678"
	desc := "A smart light bulb"

	display := EndpointDisplay{
		ID:           "port1/principals/user123/endpoints/endpoint1",
		Name:         "Living Room Light",
		Type:         "smart_light",
		Manufacturer: &manufacturer,
		Model:        &model,
		SerialNumber: &serial,
		Description:  &desc,
		Interfaces: []InterfaceDisplay{
			{Name: "power", Messages: []string{"turn-off"}, Attributes: []string{"power"}},
		},
	}

	data, err := json.Marshal(display)
	if err != nil {
		t.Fatalf("failed to marshal EndpointDisplay: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	// Full ID must be present
	id, ok := result["id"]
	if !ok {
		t.Fatal("expected 'id' field in endpoint show JSON output")
	}
	if id != "port1/principals/user123/endpoints/endpoint1" {
		t.Errorf("expected full endpoint ID, got %q", id)
	}

	// short_id should NOT be present
	if _, ok := result["short_id"]; ok {
		t.Error("expected no 'short_id' field in endpoint show JSON output")
	}

	// Verify other fields are intact
	if result["name"] != "Living Room Light" {
		t.Errorf("expected name 'Living Room Light', got %q", result["name"])
	}
}

func TestListResult_JSONContainsEndpointIDs(t *testing.T) {
	listResult := ListResult{
		Endpoints: []EndpointDisplay{
			{
				ID:   "port1/systems/zero/endpoints/ep-001",
				Name: "Endpoint One",
				Type: "device",
			},
			{
				ID:   "port1/systems/zero/endpoints/ep-002",
				Name: "Endpoint Two",
				Type: "simulator",
			},
		},
	}

	data, err := json.Marshal(listResult)
	if err != nil {
		t.Fatalf("failed to marshal ListResult: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	endpoints, ok := result["endpoints"].([]any)
	if !ok {
		t.Fatal("expected 'endpoints' array in JSON output")
	}

	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}

	for i, ep := range endpoints {
		epMap, ok := ep.(map[string]any)
		if !ok {
			t.Fatalf("endpoint %d is not an object", i)
		}

		if _, ok := epMap["id"]; !ok {
			t.Errorf("endpoint %d missing 'id' field", i)
		}
	}

	// Verify specific IDs
	ep0 := endpoints[0].(map[string]any)
	if ep0["id"] != "port1/systems/zero/endpoints/ep-001" {
		t.Errorf("endpoint 0: expected id 'port1/systems/zero/endpoints/ep-001', got %q", ep0["id"])
	}

	ep1 := endpoints[1].(map[string]any)
	if ep1["id"] != "port1/systems/zero/endpoints/ep-002" {
		t.Errorf("endpoint 1: expected id 'port1/systems/zero/endpoints/ep-002', got %q", ep1["id"])
	}
}

func TestPrintListHumanReadable_InterfaceVersionEmpty(t *testing.T) {
	f := &captureFormatter{}
	cmd := &EndpointCommand{formatter: f}

	result := ListResult{
		Endpoints: []EndpointDisplay{
			{
				ID:   "port1/systems/zero/endpoints/ep-001",
				Name: "Test Endpoint",
				Type: "device",
				Interfaces: []InterfaceDisplay{
					{Name: "portos.simulator.light", Version: ""},
				},
			},
		},
	}

	if err := cmd.printListHumanReadable(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(f.captured, "(v)") {
		t.Error("expected no empty version suffix '(v)' in list output")
	}
	if !strings.Contains(f.captured, "  - portos.simulator.light\n") {
		t.Errorf("expected interface name without version suffix, got:\n%s", f.captured)
	}
}

func TestPrintListHumanReadable_InterfaceVersionPopulated(t *testing.T) {
	f := &captureFormatter{}
	cmd := &EndpointCommand{formatter: f}

	result := ListResult{
		Endpoints: []EndpointDisplay{
			{
				ID:   "port1/systems/zero/endpoints/ep-001",
				Name: "Test Endpoint",
				Type: "device",
				Interfaces: []InterfaceDisplay{
					{Name: "portos.simulator.light", Version: "1.0"},
				},
			},
		},
	}

	if err := cmd.printListHumanReadable(result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(f.captured, "  - portos.simulator.light (v1.0)\n") {
		t.Errorf("expected interface with version suffix '(v1.0)', got:\n%s", f.captured)
	}
}

func TestPrintShowHumanReadable_InterfaceVersionEmpty(t *testing.T) {
	cmd := &EndpointCommand{}

	endpoint := EndpointDisplay{
		ID:   "port1/systems/zero/endpoints/ep-001",
		Name: "Test Endpoint",
		Type: "device",
		Interfaces: []InterfaceDisplay{
			{Name: "portos.simulator.light", Version: ""},
		},
		State: map[string]interface{}{},
	}

	result := cmd.printShowHumanReadable(endpoint)

	if strings.Contains(result, "(v)") {
		t.Error("expected no empty version suffix '(v)' in show output")
	}
	if !strings.Contains(result, "  portos.simulator.light\n") {
		t.Errorf("expected interface name without version suffix, got:\n%s", result)
	}
}

func TestPrintShowHumanReadable_InterfaceVersionPopulated(t *testing.T) {
	cmd := &EndpointCommand{}

	endpoint := EndpointDisplay{
		ID:   "port1/systems/zero/endpoints/ep-001",
		Name: "Test Endpoint",
		Type: "device",
		Interfaces: []InterfaceDisplay{
			{Name: "portos.simulator.light", Version: "2.1"},
		},
		State: map[string]interface{}{},
	}

	result := cmd.printShowHumanReadable(endpoint)

	if !strings.Contains(result, "  portos.simulator.light (v2.1)\n") {
		t.Errorf("expected interface with version suffix '(v2.1)', got:\n%s", result)
	}
}

func TestStateFromAttributes_PrefersValueFieldAndDisambiguatesDuplicateNames(t *testing.T) {
	attributes := []api.AttributeState{
		{
			Name:      "power",
			Namespace: "port1/systems/zero/capability-interfaces/power",
			Value: &map[string]interface{}{
				"value": "ON",
			},
		},
		{
			Name:      "power",
			Namespace: "port1/vendor/example",
			Value: &map[string]interface{}{
				"value": "secondary",
			},
		},
	}

	state := stateFromAttributes(&attributes)

	if state["power"] != "ON" {
		t.Fatalf("expected primary power value to be flattened, got %#v", state["power"])
	}
	if state["port1/vendor/example/power"] != "secondary" {
		t.Fatalf("expected duplicate attribute name to use namespace-qualified key, got %#v", state["port1/vendor/example/power"])
	}
}

func TestStateFromAttributes_OmitsAbsentValuesAndPreservesEmptyObjects(t *testing.T) {
	attributes := []api.AttributeState{
		{Name: "absent"},
		{Name: "empty", Value: &map[string]interface{}{}},
	}
	state := stateFromAttributes(&attributes)
	if _, present := state["absent"]; present {
		t.Fatal("an absent attribute value appeared in the display state")
	}
	value, present := state["empty"]
	if empty, ok := value.(map[string]interface{}); !present || !ok || empty == nil || len(empty) != 0 {
		t.Fatalf("present empty value was lost: %#v", state)
	}
}
func TestMessageHumanReceiptRetainsOperationAndObservedStatus(t *testing.T) {
	for _, status := range []api.MessageRequestStatus{api.QUEUED, api.INPROGRESS, api.SUCCEEDED, api.PARTIALLYSUCCEEDED, api.FAILED} {
		t.Run(string(status), func(t *testing.T) {
			formatter := &mockFormatter{}
			command := &MessageCommand{formatter: formatter}
			response := &api.MessageRequest{Id: "durable-request", Status: status,
				Results: []api.MessageRequestResult{}, Errors: []api.MessageRequestError{}}
			err := command.printResult(RunParams{Command: "power/turn-on", EndpointID: "ep/typed-alias"}, response)
			if status == api.FAILED || status == api.PARTIALLYSUCCEEDED {
				require.ErrorContains(t, err, "inspect its results and errors")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, []interface{}{"Message request durable-request for power/turn-on -> ep/typed-alias: " + string(status) + " (0 successful dispatches, 0 failed dispatches)"}, formatter.outputs)
			require.Equal(t, status, response.Status, "formatting never promotes admission into success")
		})
	}
}

func TestMessageJSONOutputRetainsExactCanonicalReceiptAndPartialOutcomes(t *testing.T) {
	formatter := &mockFormatter{json: true}
	command := &MessageCommand{formatter: formatter}
	response := &api.MessageRequest{Id: "durable-request", Status: api.PARTIALLYSUCCEEDED, Deadline: time.Now(), SyncDeadlineSeconds: 30,
		Results: []api.MessageRequestResult{{TargetId: "accepted-target", RouteId: "accepted-route"}},
		Errors:  []api.MessageRequestError{{TargetId: "uncertain-target", Code: api.TIMEDOUT}}}
	err := command.printResult(RunParams{Command: "turn-on", EndpointID: "ep/typed-alias"}, response)
	require.Error(t, err)
	require.Len(t, formatter.outputs, 1)
	require.Same(t, response, formatter.outputs[0], "JSON passes the canonical receipt to the shared formatter without a legacy wrapper")
	require.Equal(t, "accepted-target", response.Results[0].TargetId)
	require.Equal(t, "uncertain-target", response.Errors[0].TargetId)
	formatter.json, formatter.outputs = false, nil
	require.Error(t, command.printResult(RunParams{Command: "turn-on", EndpointID: "ep/typed-alias"}, response))
	require.Equal(t, []interface{}{"Message request durable-request for turn-on -> ep/typed-alias: PARTIALLY_SUCCEEDED (1 successful dispatches, 1 failed dispatches)"}, formatter.outputs)
}

func TestMessageMissingReceiptIsAnErrorWithoutOutput(t *testing.T) {
	formatter := &mockFormatter{}
	command := &MessageCommand{formatter: formatter}
	require.ErrorContains(t, command.printResult(RunParams{}, nil), "message request was not returned")
	require.Empty(t, formatter.outputs)
}
