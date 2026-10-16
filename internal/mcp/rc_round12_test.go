package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// The two-tool schema-drift path is reached before credential validation.
// A raw '<' costs one inbound byte and expands in nested JSON output, so the
// total wire response is the assertion rather than a decoded field length.
func TestRound12UnauthenticatedToolResultsAreBounded(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	for _, facade := range []facadeMode{facadeTwo, facadeBoth} {
		h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facade)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ name, prefix, suffix string }{
			{"fingerprint", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"docs.list","schema_fingerprint":"`, `"}}}`},
			{"dry_run_fingerprint", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"docs.list","dry_run":true,"schema_fingerprint":"`, `"}}}`},
			{"get_skill_domain", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_skill","arguments":{"intent":"search","domain":"`, `"}}}`},
			{"get_skill_operation_id", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_skill","arguments":{"intent":"describe","operation_id":"`, `"}}}`},
		} {
			for _, size := range []int{150 << 10, maxMCPMessageBytes - 16} {
				t.Run(string(facade)+"/"+tc.name+"/"+strconv.Itoa(size), func(t *testing.T) {
					payload := tc.prefix + strings.Repeat("<", size-len(tc.prefix)-len(tc.suffix)) + tc.suffix
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(payload)))
					if w.Body.Len() > 8192 {
						t.Fatalf("%s reflected %d bytes from %d request bytes", tc.name, w.Body.Len(), len(payload))
					}
					var resp rpcResponse
					if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || (len(resp.Result) == 0 && resp.Error == nil) {
						t.Fatalf("expected bounded RPC response for %s: %v", tc.name, err)
					}
				})
			}
		}
	}
}

func TestRound12MetadataResultConstructorHasAggregateLimit(t *testing.T) {
	result := jsonToolResult(map[string]any{"future_client_echo": strings.Repeat("<", 2<<20)}, false)
	if !result.IsError || len(result.Content) != 1 || len(result.Content[0].Text) > 512 ||
		!strings.Contains(result.Content[0].Text, "RESULT_TOO_LARGE") {
		t.Fatalf("metadata result escaped aggregate limit: length=%d", len(result.Content[0].Text))
	}
}

func TestRound12SuccessfulBackendResultKeepsLargeBusinessData(t *testing.T) {
	data := strings.Repeat("business-data", 180000)
	env, err := json.Marshal(map[string]any{"ok": true, "data": data})
	if err != nil {
		t.Fatal(err)
	}
	result := classifyExecuteResult(env, true, "docs.list", "sha256:example", false, false, nil)
	if result.IsError || len(result.Content) != 1 || len(result.Content[0].Text) <= 1<<20 ||
		!strings.Contains(result.Content[0].Text, data[:64]) {
		t.Fatal("authenticated business result was truncated by metadata limit")
	}
}
