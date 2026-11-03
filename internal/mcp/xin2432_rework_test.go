package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// XIN-2432: owner decision — the ten Loop workspace/runtime management
// operations stay hidden. Every MCP path (discovery, describe, execute, the
// three- and two-tool facades) must refuse them without reaching the backend.
// `mcp serve` itself refuses to start under OCTO_CREDENTIAL_MODE=task (see the
// built-binary test); the agent_task factory variant here is defence in depth
// for the facade layer.
var xin2432HiddenLoopOps = []string{
	"workspace.create", "workspace.update", "workspace.delete",
	"runtime.update", "runtime.delete", "runtime.archive_and_delete",
	"runtime.update_request.create", "runtime.model_request.create",
	"runtime.local_skill_request.create", "runtime.local_skill_import.create",
}

func TestXIN2432LoopManagementOperationsDeniedOverMCP(t *testing.T) {
	for _, token := range []string{"bf_test", "mat_task"} {
		t.Run(token, func(t *testing.T) {
			var mu sync.Mutex
			hits := 0
			srv := policyFixtureServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				mu.Lock()
				hits++
				mu.Unlock()
			}))
			defer srv.Close()
			s := facadeServer(t, facadeBoth)
			base := fakeBackendFactory(srv.URL, token)
			s.factoryFn = func(tc TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
				f, out, errOut := base(tc)
				if token == "mat_task" {
					// Same shape the daemon-injected task credential produces
					// (cmdutil.Factory.buildTaskCredential).
					cred, _ := f.CredentialFunc()
					cred.BotKind = "agent_task"
				}
				return f, out, errOut
			}

			if token == "mat_task" {
				expectAdmissionRPC(t, s, "search_ops", map[string]any{"domain": "loop"}, -32001)
				return
			}

			discovery := []string{
				callTool(t, s, "search_ops", map[string]any{"domain": "loop"}).Content[0].Text,
				callTool(t, s, "search_ops", map[string]any{"domain": "loop", "query": "runtime"}).Content[0].Text,
				callTool(t, s, "search_ops", map[string]any{"domain": "loop", "query": "workspace"}).Content[0].Text,
				callTool(t, s, "get_skill", map[string]any{"intent": "search", "domain": "loop", "query": "runtime"}).Content[0].Text,
				callTool(t, s, "get_skill", map[string]any{"intent": "search", "domain": "loop", "query": "workspace"}).Content[0].Text,
			}
			for _, op := range xin2432HiddenLoopOps {
				for _, text := range discovery {
					if strings.Contains(text, `"`+op+`"`) {
						t.Errorf("%s must not be advertised: %s", op, text)
					}
				}
				calls := []struct {
					tool string
					args map[string]any
				}{
					{"describe_op", map[string]any{"operation_id": op}},
					{"get_skill", map[string]any{"intent": "describe", "operation_id": op}},
					{"call_op", map[string]any{"operation_id": op, "arguments": map[string]any{"runtime_id": "r1", "workspace_id": "w1"}}},
					{"execute", map[string]any{"operation_id": op, "arguments": map[string]any{"runtime_id": "r1", "workspace_id": "w1"}}},
					{"execute", map[string]any{"operation_id": op, "dry_run": true}},
				}
				for _, c := range calls {
					res := callTool(t, s, c.tool, c.args)
					text := res.Content[0].Text
					if !res.IsError && !strings.Contains(text, `"status":"validation_error"`) && !strings.Contains(text, `"status": "validation_error"`) {
						t.Errorf("%s(%s) must be refused, got %s", c.tool, op, text)
					}
					if strings.Contains(text, `"status":"ok"`) || strings.Contains(text, `"status": "ok"`) {
						t.Errorf("%s(%s) must not succeed: %s", c.tool, op, text)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if hits != 0 {
				t.Errorf("a hidden Loop operation reached the backend %d times", hits)
			}
		})
	}
}

// TestXIN2432DocsExportsOverMCP executes the docs#5 and docs#74 routes
// themselves (docs.ppt.export, docs.scene.export png/svg) through execute and
// checks the request and the bounded inline result, including a PPT export
// larger than the inline limit.
func TestXIN2432DocsExportsOverMCP(t *testing.T) {
	html := []byte("<!doctype html><title>deck</title>")
	png := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)
	big := bytes.Repeat([]byte("<p>slide</p>"), (maxInlineBinaryBytes/12)+2)
	var mu sync.Mutex
	var seen []string
	srv := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		mu.Unlock()
		switch {
		case r.URL.Path == "/v1/bot/docs/deck/ppt/export":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(html)
		case r.URL.Path == "/v1/bot/docs/huge/ppt/export":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(big)
		case r.URL.Path == "/v1/bot/docs/board/export" && r.URL.Query().Get("format") == "svg":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write(svg)
		case r.URL.Path == "/v1/bot/docs/board/export":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	data := func(op string, args map[string]any) map[string]any {
		t.Helper()
		out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": args}))
		if out["status"] != "ok" {
			t.Fatalf("%s: %v", op, out)
		}
		return out["envelope"].(map[string]any)["data"].(map[string]any)
	}
	digest := func(b []byte) string {
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:])
	}

	deck := data("docs.ppt.export", map[string]any{"docId": "deck"})
	if deck["inline"] != true || deck["content_text"] != string(html) || deck["sha256"] != digest(html) {
		t.Errorf("docs.ppt.export = %v", deck)
	}
	board := data("docs.scene.export", map[string]any{"docId": "board"})
	if board["inline"] != true || board["content_base64"] != "iVBORw0KGgo=" || board["sha256"] != digest(png) {
		t.Errorf("docs.scene.export png = %v", board)
	}
	vector := data("docs.scene.export", map[string]any{"docId": "board", "format": "svg"})
	if vector["inline"] != true || vector["content_text"] != string(svg) {
		t.Errorf("docs.scene.export svg = %v", vector)
	}
	huge := data("docs.ppt.export", map[string]any{"docId": "huge"})
	if huge["inline"] != false || huge["content_text"] != nil || huge["sha256"] != digest(big) {
		t.Errorf("an over-limit export must be described, not inlined: %v", huge)
	}
	if size, _ := huge["size"].(float64); int(size) != len(big) {
		t.Errorf("size = %v, want %d", huge["size"], len(big))
	}

	mu.Lock()
	defer mu.Unlock()
	// An omitted format is left to the backend default (html / png).
	want := []string{
		"GET /v1/bot/docs/deck/ppt/export?",
		"GET /v1/bot/docs/board/export?",
		"GET /v1/bot/docs/board/export?format=svg",
		"GET /v1/bot/docs/huge/ppt/export?",
	}
	if strings.Join(seen, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests =\n%s\nwant\n%s", strings.Join(seen, "\n"), strings.Join(want, "\n"))
	}
}
