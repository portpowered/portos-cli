package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	models "github.com/portpowered/portos-cli/internal/api"
	client "github.com/portpowered/portos-cli/internal/api_client"
)

func TestRuleCreateReturnsCanonicalEntityAndReplacementNeverRetriesConflict(t *testing.T) {
	id := "port1/principals/person/access-control-rules/assigned"
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("id") != id {
				t.Error("replacement did not query its exact identity")
			}
			_, _ = io.WriteString(w, `{"results":[{"id":"`+id+`","etag":"\"opaque-fresh\""}]}`)
			return
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("write omitted its action key")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(methods) == 1 {
			if _, present := body["id"]; present {
				t.Error("create selected its own identity")
			}
			if r.Header.Get("If-Match") != "" {
				t.Error("create used a version precondition")
			}
			_, _ = io.WriteString(w, `{"id":"`+id+`","etag":"\"created\"","owner":"port1/principals/person","effect":"ALLOW"}`)
			return
		}
		if r.Header.Get("If-Match") != `"opaque-fresh"` {
			t.Error("replace did not preserve the fresh opaque etag")
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = io.WriteString(w, `{"family":"PRECONDITION_FAILED","code":"PRECONDITION_FAILED","message":"changed"}`)
	}))
	defer srv.Close()
	rules := client.NewTestClientWithTransport(t, srv.URL, http.DefaultTransport).Rules()
	created, err := rules.Create(context.Background(), &models.UpsertAccessControlRuleRequest{Effect: models.AccessControlRuleEffectALLOW})
	if err != nil || created.Id != id || created.Etag == nil || *created.Etag != `"created"` || created.Owner != "port1/principals/person" {
		t.Fatalf("canonical create: %+v, %v", created, err)
	}
	_, err = rules.Create(context.Background(), &models.UpsertAccessControlRuleRequest{Id: &id, Effect: models.AccessControlRuleEffectALLOW})
	if err == nil || len(methods) != 3 {
		t.Fatalf("conflict must stop after POST, GET, POST: %v, %v", methods, err)
	}
}

func TestRuleDeleteUsesFreshOpaqueVersionAndNoBody(t *testing.T) {
	id := "port1/principals/person/access-control-rules/assigned"
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"results":[{"id":"`+id+`","etag":"\"delete-version\""}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodDelete || r.URL.Path != "/"+id || len(body) != 0 || r.Header.Get("If-Match") != `"delete-version"` || r.Header.Get("Idempotency-Key") == "" || r.URL.Query().Get("reason") != "operator request" {
			t.Error("delete did not preserve the observed version, reason and bodyless item route")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	rules := client.NewTestClientWithTransport(t, srv.URL, http.DefaultTransport).Rules()
	if err := rules.DeleteWithReason(context.Background(), id, "operator request"); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != http.MethodGet || methods[1] != http.MethodDelete {
		t.Fatalf("unexpected actions: %v", methods)
	}
}
