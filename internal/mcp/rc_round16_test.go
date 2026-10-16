package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound16JSONValueBudgetMatchesBusinessSchema(t *testing.T) {
	nested := func(count int) string {
		var parts []string
		for i := range count {
			parts = append(parts, fmt.Sprintf(`"key%d":%d`, i, i))
		}
		return `{"nest":{"items":[{` + strings.Join(parts, ",") + `}]}}`
	}
	if _, err := decodeArgs(json.RawMessage(nested(1200))); err != nil {
		t.Fatalf("valid nested business body rejected: %v", err)
	}
	relations := make([]map[string]any, 200)
	for i := range relations {
		relations[i] = map[string]any{"a": i, "b": i, "c": i, "d": i, "e": i, "f": i}
	}
	validBusiness, err := json.Marshal(map[string]any{"relations": relations, "name": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArgs(validBusiness); err != nil {
		t.Fatalf("200 six-field relations were rejected: %v", err)
	}
	hugeArray := `{"a":[` + strings.TrimSuffix(strings.Repeat("1,", maxMCPJSONValues), ",") + `]}`
	if _, err := decodeArgs(json.RawMessage(hugeArray)); err == nil || !strings.Contains(err.Error(), "too many JSON values") {
		t.Fatalf("array amplification was accepted: %v", err)
	}
	top := func(count int) string {
		var parts []string
		for i := range count {
			parts = append(parts, fmt.Sprintf(`"key%d":%d`, i, i))
		}
		return `{` + strings.Join(parts, ",") + `}`
	}
	if _, err := decodeArgs(json.RawMessage(top(maxMCPObjectKeys))); err != nil {
		t.Fatalf("512 top-level keys rejected: %v", err)
	}
	if _, err := decodeArgs(json.RawMessage(top(maxMCPObjectKeys + 1))); err == nil || !strings.Contains(err.Error(), "too many object keys") {
		t.Fatalf("513 top-level keys accepted: %v", err)
	}
	for _, tool := range []string{"call_op", "execute"} {
		facade := facadeThree
		if tool == "execute" {
			facade = facadeTwo
		}
		h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facade)
		if err != nil {
			t.Fatal(err)
		}
		raw := `{"jsonrpc":"2.0","id":17,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"operation_id":"docs.list","arguments":` + hugeArray + `}}}`
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw)))
		var response rpcResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error == nil || response.Error.Code != codeInvalidParams || string(response.ID) != "17" || !strings.Contains(response.Error.Message, "too many JSON values") {
			t.Fatalf("%s: budget error must be -32602 with the request id before auth: %s", tool, w.Body.String())
		}
	}
}
