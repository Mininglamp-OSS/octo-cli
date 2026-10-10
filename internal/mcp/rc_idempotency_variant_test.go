package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRCRepublishWithoutReplayKeyDoesNotRetry(t *testing.T) {
	var hits atomic.Int32
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(502)
		} else {
			w.WriteHeader(409)
		}
		_, _ = w.Write([]byte(`{"error":{"code":"version_conflict"}}`))
	}))
	defer be.Close()
	s := newTestServer(t).WithFacade(facadeBoth)
	t.Setenv("OCTO_API_BASE_URL", be.URL)
	t.Setenv("OCTO_BOT_TOKEN", "bf_test")
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	r := callTool(t, s, "execute", map[string]any{"operation_id": "html.publish", "arguments": map[string]any{"html": "<p>x</p>", "slug": "existing", "version": 2}})
	x := toolText(t, r)
	if hits.Load() != 1 || x["status"] != "result_unknown" {
		t.Fatalf("republish without replay key must preserve first uncertain outcome: hits=%d result=%v", hits.Load(), x)
	}
}

func TestRCCreateInvalidResponsePreservesReplayKey(t *testing.T) {
	for _, op := range []string{"html.publish", "html.draft.create"} {
		for _, body := range []string{"<html>SPA</html>", `{"data":`} {
			t.Run(op+"/"+body, func(t *testing.T) {
				var sent string
				be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var a map[string]any
					if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
						t.Error(err)
					}
					sent, _ = a["idempotency_key"].(string)
					_, _ = w.Write([]byte(body))
				}))
				defer be.Close()
				s := newTestServer(t).WithFacade(facadeBoth)
				t.Setenv("OCTO_API_BASE_URL", be.URL)
				t.Setenv("OCTO_BOT_TOKEN", "bf_test")
				t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
				r := callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": map[string]any{"html": "<p>x</p>"}})
				x := toolText(t, r)
				env, _ := x["envelope"].(map[string]any)
				err, _ := env["error"].(map[string]any)
				detail, _ := err["detail"].(map[string]any)
				if sent == "" || detail["idempotency_key"] != sent || x["status"] != "result_unknown" {
					t.Fatalf("invalid successful response must preserve actual replay key: sent=%q result=%v", sent, x)
				}
			})
		}
	}
}
