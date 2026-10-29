package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Exercise the inherited CLI routes and both MCP consumers on real transports.
// The integration records new registry entries without widening MCP exposure.
func TestBuiltBinary_XIN2572Integration(t *testing.T) {
	bin := buildOctoCLI(t)
	var mu sync.Mutex
	var seen []recordedRequest
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Body: string(raw)})
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/file") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("version-file-fixture"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer backend.Close()
	take := func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		return out
	}
	outputPath := filepath.Join(t.TempDir(), "SKILL.md")
	operations := []struct {
		id, path, query string
		cli             []string
	}{
		{"plugin.version.get", "/market/api/v1/plugins/p1/versions/v1", "", []string{"plugin", "version", "get", "p1", "v1"}},
		{"plugin.version.download", "/market/api/v1/plugins/p1/versions/v1/file", "path=SKILL.md", []string{"plugin", "version", "download", "p1", "v1", "--path", "SKILL.md", "--output", outputPath}},
		{"plugin.dependents.list", "/market/api/v1/plugins/p1/dependents", "", []string{"plugin", "dependents", "list", "p1"}},
		{"plugin.graph.get", "/market/api/v1/plugins/detail_graph", "plugin_id=p1", []string{"plugin", "graph", "get", "--plugin-id", "p1"}},
	}
	for _, op := range operations {
		t.Run("cli/"+op.id, func(t *testing.T) {
			cmd := exec.Command(bin, append([]string{"marketplace"}, op.cli...)...)
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("CLI: %v %s", err, out)
			}
			if hits := take(); len(hits) != 1 || hits[0].Method != "GET" || hits[0].Path != op.path || hits[0].Query != op.query || hits[0].Auth != "Bearer bf_smoke" {
				t.Fatalf("CLI request mismatch: %+v", hits)
			}
		})
	}
	if raw, err := os.ReadFile(outputPath); err != nil || string(raw) != "version-file-fixture" {
		t.Fatalf("inherited binary CLI download = %q, %v", raw, err)
	}
	for _, transport := range []string{"stdio", "http"} {
		for _, surface := range []struct{ facade, search, describe, execute string }{
			{"two", "get_skill", "get_skill", "execute"},
			{"three", "search_ops", "describe_op", "call_op"},
		} {
			t.Run(transport+"/"+surface.facade, func(t *testing.T) {
				call := func(tool string, args map[string]any) string {
					raw, _ := json.Marshal(args)
					return xin2453Text(t, xin2453Serve(t, bin, transport, surface.facade, backend.URL, xin2453Request(tool, string(raw))))
				}
				searchArgs := map[string]any{"domain": "marketplace", "query": "plugin"}
				if surface.facade == "two" {
					searchArgs["intent"] = "search"
				}
				search := call(surface.search, searchArgs)
				if !strings.Contains(search, "plugin.list") {
					t.Fatalf("unchanged exposed operation missing from search: %s", search)
				}
				for _, op := range operations {
					if strings.Contains(search, op.id) {
						t.Errorf("withheld %s appeared in discovery", op.id)
					}
					searchArgs["query"] = op.id
					var exact struct {
						Operations []json.RawMessage `json:"operations"`
					}
					text := call(surface.search, searchArgs)
					if err := json.Unmarshal([]byte(text), &exact); err != nil || len(exact.Operations) != 0 || !strings.Contains(text, `"operations"`) {
						t.Fatalf("exact search advertised withheld %s: %s (%v)", op.id, text, err)
					}
					for _, tool := range []string{surface.describe, surface.execute} {
						args := map[string]any{"operation_id": op.id}
						switch tool {
						case "get_skill":
							args["intent"] = "describe"
						case surface.execute:
							args["arguments"] = map[string]any{"plugin_id": "p1", "version_id": "v1", "path": "SKILL.md"}
						}
						text := call(tool, args)
						if !strings.Contains(text, "not classified for MCP exposure") {
							t.Errorf("%s %s must be denied: %s", tool, op.id, text)
						}
					}
				}
				if hits := take(); len(hits) != 0 {
					t.Fatalf("withheld operations reached backend: %+v", hits)
				}

				// The inherited flag rename must map MCP wire names to the new
				// flags, then preserve the original query fields on the wire.
				call(surface.execute, map[string]any{"operation_id": "task.list", "arguments": map[string]any{
					"X-Workspace-ID": "11111111-1111-4111-8111-111111111111", "assignee_octo_uid": "Human/CaseSensitive", "creator_octo_uid": "Creator/Exact",
				}})
				if hits := take(); len(hits) != 1 || hits[0].Path != "/fleet/api/v1/tasks" {
					t.Fatalf("member-ID query did not reach task.list: %+v", hits)
				} else if q, err := url.ParseQuery(hits[0].Query); err != nil || q.Get("assignee_octo_uid") != "Human/CaseSensitive" || q.Get("creator_octo_uid") != "Creator/Exact" || q.Has("assignee_member_id") || q.Has("creator_member_id") {
					t.Fatalf("member-ID wire fields changed: %s, %v", hits[0].Query, err)
				}

				// Upstream relation validation must still run through the shared
				// engine; malformed relations never reach the backend.
				plugin := map[string]any{"plugin_name": "fixture", "plugin_type": "expert", "visibility": "private", "manifest_json": map[string]any{}, "plugin_json": map[string]any{}}
				for _, valid := range []bool{false, true} {
					relation := map[string]any{"relation_type": "expert_connector", "target_plugin_id": "c1"}
					if !valid {
						relation["target_version_id"] = "forbidden-pin"
					}
					text := call(surface.execute, map[string]any{"operation_id": "plugin.upsert", "arguments": map[string]any{"plugin": plugin, "relations": []any{relation}}})
					hits := take()
					if !valid {
						if len(hits) != 0 || !strings.Contains(text, "target_version_id") {
							t.Fatalf("invalid connector pin must fail locally: %s, %+v", text, hits)
						}
					} else if len(hits) != 1 || hits[0].Method != "POST" || hits[0].Path != "/market/api/v1/plugins/upsert" || !strings.Contains(hits[0].Body, `"target_plugin_id":"c1"`) {
						t.Fatalf("valid inherited relation rejected/changed: %s, %+v", text, hits)
					}
				}
			})
		}
	}
}
