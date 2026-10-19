package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound11HTTPErrorBodyBoundedForEveryClientIdentifier(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		prefix string
		suffix string
		code   int
		id     string
	}{
		{"id", `{"jsonrpc":"2.0","id":"`, `","method":"resources/read","params":{"uri":"bad"}}`, codeInvalidRequest, "null"},
		{"method", `{"jsonrpc":"2.0","id":1,"method":"`, `"}`, codeMethodNotFound, "1"},
		{"tool_name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`, `","arguments":{}}}`, codeInvalidParams, "1"},
		{"resource_uri", `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":"octo://skills/octo-messaging/`, `"}}`, codeInvalidParams, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Raw '<' costs one request byte but six JSON response bytes. Marshal
			// would escape it before sending and hide the amplification.
			payload := tc.prefix + strings.Repeat("<", maxHTTPBodyBytes-len(tc.prefix)-len(tc.suffix)-16) + tc.suffix
			if len(payload) < maxHTTPBodyBytes-256 || len(payload) > maxHTTPBodyBytes {
				t.Fatalf("probe is not near the request limit: %d bytes", len(payload))
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(payload)))
			if w.Body.Len() > 8192 {
				t.Fatalf("%s reflected %d response bytes from %d request bytes", tc.name, w.Body.Len(), len(payload))
			}
			var resp rpcResponse
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error == nil || resp.Error.Code != tc.code || string(resp.ID) != tc.id {
				t.Fatalf("%s got unexpected JSON-RPC response: %s (%v)", tc.name, w.Body.String(), err)
			}
		})
	}
}

func TestRound11ResponseConstructorsBoundIDAndErrorMessage(t *testing.T) {
	id := json.RawMessage(`"` + strings.Repeat("<", 2048) + `"`)
	for _, resp := range []rpcResponse{
		newErrorResponse(id, codeInvalidParams, strings.Repeat("<", 2048)),
		newResultResponse(id, map[string]any{}),
	} {
		encoded, err := json.Marshal(resp)
		if err != nil || !bytes.Contains(encoded, []byte(`"id":null`)) || len(encoded) > 8192 {
			t.Fatalf("response constructor reflected oversized input: %d bytes (%v)", len(encoded), err)
		}
	}
	if !validRequestID(json.RawMessage(`"short"`)) {
		t.Fatal("ordinary JSON-RPC id was rejected")
	}
}

func TestTrustedScopeAuditCoversThreadShortID(t *testing.T) {
	if !isScopeField("short_id") || !isScopeField("X-Short-ID") {
		t.Fatal("thread short id escaped the trusted-scope coverage audit")
	}
}
