package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// Discovery is available without a bearer, so a result payload must not echo
// a near-limit client string into a larger HTTP response.
func TestRound12UnauthenticatedDiscoveryResultIsBounded(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, prefix, suffix string }{
		{"domain", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_ops","arguments":{"domain":"`, `"}}}`},
		{"query", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_ops","arguments":{"query":"`, `"}}}`},
		{"operation_id", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"describe_op","arguments":{"operation_id":"`, `"}}}`},
	} {
		for _, size := range []int{150 << 10, maxHTTPBodyBytes - 16} {
			t.Run(tc.name+"/"+strconv.Itoa(size), func(t *testing.T) {
				payload := tc.prefix + strings.Repeat("<", size-len(tc.prefix)-len(tc.suffix)) + tc.suffix
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(payload)))
				if w.Body.Len() > 8192 {
					t.Fatalf("%s reflected %d bytes from %d request bytes", tc.name, w.Body.Len(), len(payload))
				}
				var resp rpcResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || len(resp.Result) == 0 {
					t.Fatalf("expected bounded tool result for %s: %v", tc.name, err)
				}
			})
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
