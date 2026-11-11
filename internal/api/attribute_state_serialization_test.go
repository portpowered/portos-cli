package api

import (
	"encoding/json"
	"testing"
)

func TestAttributeStatePreservesValuePresence(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		value    string
		hasError bool
	}{
		{name: "absent", input: `{}`},
		{name: "empty object", input: `{"value":{}}`, value: `{}`},
		{name: "populated", input: `{"value":{"value":"ON"}}`, value: `{"value":"ON"}`},
		{name: "error only", input: `{"error":{"family":"ENDPOINT_UNAVAILABLE","code":"DEEP_QUERY_FAILED","message":"Check the device and retry."}}`, hasError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state AttributeState
			if err := json.Unmarshal([]byte(test.input), &state); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			value, present := fields["value"]
			if present != (test.value != "") || string(value) != test.value {
				t.Fatalf("value after round trip = %s (present=%t), want %s; JSON: %s", value, present, test.value, encoded)
			}
			if _, present := fields["error"]; present != test.hasError {
				t.Fatalf("error after round trip present=%t, want %t", present, test.hasError)
			}
		})
	}
}
