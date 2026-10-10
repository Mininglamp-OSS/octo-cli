package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReviewSafetyPlacementNeverWrites(t *testing.T) {
	be := &backend{reply: `{"data":{}}`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	operation := `{"operation_id":"message.send","arguments":{"channel_id":"c","channel_type":1,"payload":{"type":1,"content":"fixture"}}}`
	cases := []string{
		`{"name":"execute","dry_run":true,"arguments":` + operation + `}`,
		`{"name":"execute","arguments":{"operation_id":"message.send","arguments":{"channel_id":"c","channel_type":1,"payload":{},"dry_run":true}}}`,
		`{"name":"execute","arguments":{"operation_id":"message.send","dry_run":true},"arguments":` + operation + `}`,
		`{"name":"execute","Arguments":` + operation + `}`,
		`{"name":"execute","arguments":{"operation_id":"message.send","arguments":{"channel_id":"c","channel_type":1,"payload":{},"schema_fingerprint":"x"}}}`,
	}
	for _, raw := range cases {
		be.path = ""
		response, _ := s.Dispatch(t.Context(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: json.RawMessage(raw)})
		if response.Error == nil || response.Error.Code != codeInvalidParams || be.path != "" {
			t.Fatalf("unsafe placement %s: %+v path=%s", raw, response, be.path)
		}
	}
	response, _ := s.Dispatch(t.Context(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: json.RawMessage(`{"name":"execute","arguments":{"operation_id":"message.send","dry_run":true,"arguments":{"channel_id":"c","channel_type":1,"payload":{"type":1,"content":"fixture"}}}}`)})
	if response.Error != nil || be.path != "" || !strings.Contains(string(response.Result), "dry_run") {
		t.Fatalf("canonical dry run: %+v", response)
	}
	response, _ = s.Dispatch(t.Context(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: json.RawMessage(`{"name":"execute","arguments":` + operation + `}`)})
	if response.Error != nil || be.path == "" {
		t.Fatalf("canonical execution failed: %+v", response)
	}
}

func TestReviewGetSkillClosed(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	response, _ := s.Dispatch(t.Context(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: json.RawMessage(`{"name":"get_skill","arguments":{"intent":"describe","operation_id":"message.send","dpeth":"summary"}}`)})
	if response.Error == nil {
		t.Fatal("unknown discovery key accepted")
	}
}
