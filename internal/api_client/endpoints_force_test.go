package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetByIDWithExpandSendsForceOnlyWhenAsked(t *testing.T) {
	for _, force := range []bool{true, false} {
		var query map[string][]string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
		}))
		client, _ := newTestClient(t, server.URL)
		endpoints := &EndpointsClientImpl{client: client}
		if _, err := endpoints.GetByIDWithExpand(t.Context(), "ep/1", "interfaces.attributes", force); err != nil {
			t.Fatal(err)
		}
		server.Close()
		values, sent := query["forceDeviceAttributeQuery"]
		if sent != force || (force && values[0] != "true") || query["expand"][0] != "interfaces.attributes" || query["id"][0] != "ep/1" {
			t.Fatalf("force=%v query=%v", force, query)
		}
	}
}
