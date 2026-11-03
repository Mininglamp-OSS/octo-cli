package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// XIN-2532 (F-C1) against the real binary over stdio and HTTP: task.search's
// positional query q reaches the backend from get_skill describe -> execute,
// from execute's dry run (no request), from call_op, and from the CLI itself,
// with the same path, query and workspace header for every search term.

type xin2532BinBackend struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
}

func newXIN2532BinBackend(t *testing.T) *xin2532BinBackend {
	t.Helper()
	b := &xin2532BinBackend{}
	b.srv = binaryPolicyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.hits = append(b.hits, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery+" ws="+r.Header.Get("X-Workspace-ID"))
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":2,"page_size":5}}`))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *xin2532BinBackend) take() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.hits
	b.hits = nil
	return out
}

func xin2532Want(q string) string {
	v := url.Values{}
	v.Set("page", "2")
	v.Set("page_size", "5")
	v.Set("q", q)
	return "GET /fleet/api/v1/tasks/_search?" + v.Encode() + " ws=11111111-1111-4111-8111-111111111111"
}

func TestBuiltBinary_XIN2532TaskSearchPositionalQuery(t *testing.T) {
	bin := buildOctoCLI(t)
	backend := newXIN2532BinBackend(t)
	values := []string{"XIN-2452", "-x", "--profile=evil", "two words", "中文 検索", `"q" $(id); | &`}

	for _, transport := range []string{"stdio", "http"} {
		describe := xin2453Request("get_skill", `{"intent":"describe","operation_id":"task.search"}`)
		text := xin2453Text(t, xin2453Serve(t, bin, transport, "two", backend.srv.URL, describe))
		if !strings.Contains(text, `"name": "q"`) || !strings.Contains(text, `"positional_name": "query"`) {
			t.Errorf("%s: describe must publish q with its operand label: %.600s", transport, text)
		}

		for _, q := range values {
			args, _ := json.Marshal(map[string]any{
				"operation_id": "task.search",
				"arguments":    map[string]any{"X-Workspace-ID": "11111111-1111-4111-8111-111111111111", "q": q, "page": 2, "page_size": 5},
			})
			dry, _ := json.Marshal(map[string]any{
				"operation_id": "task.search", "dry_run": true,
				"arguments": map[string]any{"X-Workspace-ID": "11111111-1111-4111-8111-111111111111", "q": q, "page": 2, "page_size": 5},
			})

			text := xin2453Text(t, xin2453Serve(t, bin, transport, "two", backend.srv.URL, xin2453Request("execute", string(dry))))
			if !strings.Contains(text, `"status": "ok"`) || !strings.Contains(text, `"dry_run": true`) {
				t.Errorf("%s dry_run q=%q: %.600s", transport, q, text)
			}
			if hits := backend.take(); len(hits) != 0 {
				t.Errorf("%s dry_run q=%q sent %v", transport, q, hits)
			}

			for _, c := range []struct{ facade, tool string }{{"two", "execute"}, {"three", "call_op"}} {
				text := xin2453Text(t, xin2453Serve(t, bin, transport, c.facade, backend.srv.URL, xin2453Request(c.tool, string(args))))
				if strings.Contains(text, "not recognised") || strings.Contains(text, `"ok": false`) {
					t.Errorf("%s %s q=%q: %.600s", transport, c.tool, q, text)
				}
				if hits := backend.take(); len(hits) != 1 || hits[0] != xin2532Want(q) {
					t.Errorf("%s %s q=%q: backend saw %q, want [%q]", transport, c.tool, q, hits, xin2532Want(q))
				}
			}
		}
	}

	// The CLI on the same binary and configuration sends the same request.
	for _, q := range values {
		cmd := exec.Command(bin, "loop", "task", "search", "--workspace-id", "11111111-1111-4111-8111-111111111111", "--page", "2", "--page-size", "5", "--", q)
		cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.srv.URL)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("cli q=%q: %v %s", q, err, out)
		}
		if hits := backend.take(); len(hits) != 1 || hits[0] != xin2532Want(q) {
			t.Errorf("cli q=%q: backend saw %q, want [%q]", q, hits, xin2532Want(q))
		}
	}
}
