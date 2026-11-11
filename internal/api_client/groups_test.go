package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGroupsEnumerateSendsPageAndExpand(t *testing.T) {
	var path string
	var query map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"id":"g/1","etag":"\"1\"","name":{"type":"PLAIN","value":"Hallway"},"ownerId":"p","enablement":"ENABLED","interfaces":[]}],"paginationContext":{"nextToken":"t2"}}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	groups := &GroupsClientImpl{client: client}

	token, max := "t1", 5
	page, err := groups.Enumerate(t.Context(), &token, &max, []string{"interfaces.attributes"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/endpoint-groups" || query["nextToken"][0] != "t1" || query["maxResults"][0] != "5" || query["expand"][0] != "interfaces.attributes" {
		t.Fatalf("path=%s query=%v", path, query)
	}
	if len(page.Results) != 1 || page.Results[0].Name.Value != "Hallway" || *page.PaginationContext.NextToken != "t2" {
		t.Fatalf("page=%+v", page)
	}
}

func TestGroupsEnumerateSendsNoQueryByDefault(t *testing.T) {
	var rawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	client, _ := newTestClient(t, server.URL)
	if _, err := (&GroupsClientImpl{client: client}).Enumerate(t.Context(), nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rawQuery != "" {
		t.Fatalf("query %q, want none", rawQuery)
	}
}
