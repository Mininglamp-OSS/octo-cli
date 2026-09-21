package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// rawToolCall dispatches a tools/call with a raw arguments JSON blob and returns
// the JSON-RPC response, so malformed safety-field decoding (which yields a
// protocol-level error, not a tool result) can be asserted.
func rawToolCall(t *testing.T, s *Server, argsJSON string) rpcResponse {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": "execute", "arguments": json.RawMessage(argsJSON)})
	resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if !has {
		t.Fatalf("tools/call produced no response")
	}
	return resp
}

// TestExecute_MalformedSafetyFieldsRejected pins the malformed-safety-field
// decoding seam: a non-object `arguments`, or a wrong-typed `dry_run`, is a
// protocol error (-32602), never a silent degrade to empty/false.
func TestExecute_MalformedSafetyFieldsRejected(t *testing.T) {
	s := facadeServer(t, facadeTwo)

	// arguments must be a JSON object.
	resp := rawToolCall(t, s, `{"operation_id":"message.send","arguments":"not-an-object"}`)
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Errorf("non-object arguments must be -32602, got %+v", resp.Error)
	}

	// dry_run must be a boolean; a string is an invalid shape.
	resp = rawToolCall(t, s, `{"operation_id":"message.send","dry_run":"yes"}`)
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Errorf("non-boolean dry_run must be -32602, got %+v", resp.Error)
	}

	// A well-formed empty object is fine (missing operation_id is a tool-level
	// validation_error, not a protocol error) — the legitimate direction.
	resp = rawToolCall(t, s, `{}`)
	if resp.Error != nil {
		t.Errorf("empty object must not be a protocol error, got %+v", resp.Error)
	}
}

// TestSpliceGlobalFlags_InsertsBeforePositionalSeparator pins the global-flag
// insertion seam: a global flag (e.g. --dry-run) must land BEFORE the "--"
// positional separator, or cobra parses it as a positional path value.
func TestSpliceGlobalFlags_InsertsBeforePositionalSeparator(t *testing.T) {
	argv := []string{"docs", "get", "--locale=en", "--", "D123"}
	out := spliceGlobalFlags(argv, []string{"--dry-run"})

	sep, flag := -1, -1
	for i, a := range out {
		if a == "--" {
			sep = i
		}
		if a == "--dry-run" {
			flag = i
		}
	}
	if flag < 0 || sep < 0 || flag > sep {
		t.Errorf("--dry-run must appear before the -- separator, got %v", out)
	}
	// The positional must still be last and intact.
	if out[len(out)-1] != "D123" {
		t.Errorf("positional must remain after --, got %v", out)
	}
}

// TestSpliceGlobalFlags_NoSeparatorAppends pins the no-positional case: with no
// "--" separator the flag goes at the end (same effective position).
func TestSpliceGlobalFlags_NoSeparatorAppends(t *testing.T) {
	out := spliceGlobalFlags([]string{"event", "list", "--locale=en"}, []string{"--dry-run"})
	if out[len(out)-1] != "--dry-run" {
		t.Errorf("with no -- separator, --dry-run should append at the end, got %v", out)
	}
}

// TestMultipartDryRun_TrustedContextApplied pins that trusted-context projection
// runs on the multipart dry-run plan path and does not break it or hit the
// backend. Multipart ops declare no session-bound overridable field, so the
// forced values pass through as a no-op — the guarantee is that setting a
// trusted context neither errors nor leaks nor sends.
func TestMultipartDryRun_TrustedContextApplied(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{SpaceID: "forced-space", ChannelID: "forced-chan"}

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"dry_run":      true,
		"arguments":    map[string]any{"file_path": "/tmp/x.bin"},
	})
	if res.IsError {
		t.Fatalf("multipart dry-run with a trusted context must still plan ok: %s", res.Content[0].Text)
	}
	out := toolText(t, res)
	if out["status"] != "ok" || out["dry_run"] != true {
		t.Errorf("status=%v dry_run=%v, want ok/true", out["status"], out["dry_run"])
	}
	if be.path != "" {
		t.Errorf("multipart dry-run must not contact the backend; saw %q", be.path)
	}
	// No forced secret leaks into the plan text.
	if strings.Contains(res.Content[0].Text, "bf_test") {
		t.Errorf("plan must not contain the connection token")
	}
}

// TestDispatch_CrossFacadeToolRejected pins the facade-selection plumbing at the
// dispatch seam: under --facade two a three-tool name (call_op) is an unknown
// tool, and under --facade three a two-tool name (execute) is unknown. This
// kills a mutation that bypasses the toolEnabled gate in handleToolCall.
func TestDispatch_CrossFacadeToolRejected(t *testing.T) {
	call := func(s *Server, name string) *rpcError {
		params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(`{"operation_id":"message.send"}`)})
		resp, _ := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
		return resp.Error
	}
	two := facadeServer(t, facadeTwo)
	if e := call(two, "call_op"); e == nil || !strings.Contains(e.Message, "unknown tool") {
		t.Errorf("call_op under facade two must be an unknown tool, got %+v", e)
	}
	if e := call(two, "execute"); e != nil {
		t.Errorf("execute under facade two must dispatch, got rpc error %+v", e)
	}
	three := facadeServer(t, facadeThree)
	if e := call(three, "execute"); e == nil || !strings.Contains(e.Message, "unknown tool") {
		t.Errorf("execute under facade three must be an unknown tool, got %+v", e)
	}
}
