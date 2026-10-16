package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRound9UnexpectedRedirectKeepsWriteOutcomeUnknown(t *testing.T) {
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer backend.Close()
	s := newTestServer(t).WithFacade(facadeTwo)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
	for _, tc := range []struct {
		operation string
		args      map[string]any
		want      string
	}{
		{"message.send", map[string]any{"channel_id": "c", "channel_type": 1, "payload": map[string]any{"text": "x"}}, "result_unknown"},
		{"docs.list", map[string]any{}, "execution_error"},
	} {
		res := callTool(t, s, "execute", map[string]any{"operation_id": tc.operation, "arguments": tc.args})
		if !res.IsError {
			t.Fatalf("%s unexpectedly succeeded", tc.operation)
		}
		if status := toolText(t, res)["status"]; status != tc.want {
			t.Errorf("%s status = %v, want %s", tc.operation, status, tc.want)
		}
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("redirect backend saw %d requests, want one per operation", got)
	}
}

func TestRound9FingerprintCoversResponseExecutionFields(t *testing.T) {
	base, ok := registry.MustNew().GetOperation("html.publish")
	if !ok || base.ResponseSchema == nil {
		t.Fatal("html.publish response schema missing")
	}
	clone := func() *registry.OperationDetail {
		t.Helper()
		raw, err := json.Marshal(base.ResponseSchema)
		if err != nil {
			t.Fatal(err)
		}
		var response registry.SchemaInfo
		if err := json.Unmarshal(raw, &response); err != nil {
			t.Fatal(err)
		}
		copyDetail := *base
		copyDetail.ResponseSchema = &response
		return &copyDetail
	}
	before := schemaFingerprint(base)
	change := clone()
	if change.ResponseSchema.Properties["doc_id"].Type != "string" {
		t.Fatal("html.publish doc_id response type changed; update the test fixture")
	}
	docID := change.ResponseSchema.Properties["doc_id"]
	docID.Type = "integer"
	change.ResponseSchema.Properties["doc_id"] = docID
	if schemaFingerprint(change) == before {
		t.Fatal("response doc_id type did not invalidate the fingerprint")
	}
	change = clone()
	change.ResponseSchema.Required = append(change.ResponseSchema.Required, "share_url")
	if schemaFingerprint(change) == before {
		t.Fatal("response required-field change did not invalidate the fingerprint")
	}
	change = clone()
	docID = change.ResponseSchema.Properties["doc_id"]
	docID.Description = "different prose"
	change.ResponseSchema.Properties["doc_id"] = docID
	if schemaFingerprint(change) != before {
		t.Fatal("response prose alone invalidated the fingerprint")
	}
}
