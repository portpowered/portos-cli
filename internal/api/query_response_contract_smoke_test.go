package api

import (
	"encoding/json"
	"testing"
)

func TestGeneratedQueryResponseTypesConsumeStandardQueryShapes(t *testing.T) {
	nextToken := "next-page"
	maxResults := 25
	paginationContext := &PaginationContext{
		NextToken:  &nextToken,
		MaxResults: &maxResults,
	}
	queryErrors := []EndpointQueryResponseError{{
		Code:     ErrorCodeDEEPQUERYFAILED,
		Family:   ErrorFamilyINTERNALSERVERERROR,
		Location: "results[0]",
		Message:  "partial endpoint refresh failure",
	}}
	queryResponse := roundTripQueryResponse(t, EndpointQueryResponse{
		Results: EndpointsList{{
			Id:         "endpoint-1",
			Interfaces: []ApplicationInterface{{Name: "power"}},
			Name:       TypeValue{Type: "plain", Value: "Desk lamp"},
			OwnerId:    "principal-1",
			Type:       "device",
		}},
		PaginationContext: paginationContext,
		Errors:            &queryErrors,
	})
	if len(queryResponse.Results) != 1 || queryResponse.Results[0].Id != "endpoint-1" {
		t.Fatalf("query POST response results did not round trip: %#v", queryResponse.Results)
	}
	if queryResponse.PaginationContext == nil || queryResponse.PaginationContext.NextToken == nil || *queryResponse.PaginationContext.NextToken != nextToken {
		t.Fatalf("query POST paginationContext did not round trip: %#v", queryResponse.PaginationContext)
	}
	if queryResponse.Errors == nil || len(*queryResponse.Errors) != 1 || (*queryResponse.Errors)[0].Location != "results[0]" {
		t.Fatalf("query POST errors did not round trip: %#v", queryResponse.Errors)
	}
}

func roundTripQueryResponse[T any](t *testing.T, response T) T {
	t.Helper()

	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal generated query response: %v", err)
	}

	var decoded T
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal generated query response: %v", err)
	}

	return decoded
}
