package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- section/resource correctness and production-tree parity ---

func TestReadResource_SectionURIFragmentResolves(t *testing.T) {
	s := newTestServer(t)
	// A section_uri carrying an anchor must resolve to the whole file.
	res, err := s.readResource("octo://skills/octo-mail/SKILL.md#compose-and-send")
	if err != nil {
		t.Fatalf("section_uri with fragment must read the file: %v", err)
	}
	contents := res["contents"].([]resourceContent)
	if len(contents) == 0 || !strings.Contains(contents[0].Text, "name: octo-mail") {
		t.Errorf("fragment URI should return the SKILL.md body")
	}
}

func TestDescribeOp_DisabledServiceNotDescribable(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "describe_op", map[string]any{"operation_id": "matter.create"})
	if !res.IsError {
		t.Fatalf("a disabled-service operation must not be describable")
	}
}

func TestDivergentDriveShareOps_NotAdvertisedOrCallable(t *testing.T) {
	s := newTestServer(t)

	// search_ops(domain drive) must not list the divergent hand-written ops.
	res := callTool(t, s, "search_ops", map[string]any{"domain": "drive"})
	payload := toolText(t, res)
	for _, o := range payload["operations"].([]any) {
		id := o.(map[string]any)["id"]
		if _, div := divergentMCPOps[id.(string)]; div {
			t.Errorf("divergent op %v must not be advertised via search_ops", id)
		}
	}

	// describe_op and call_op on a divergent op point at the CLI form.
	for _, opID := range []string{"drive.share.access", "drive.share.blob-create", "drive.share.download"} {
		d := callTool(t, s, "describe_op", map[string]any{"operation_id": opID})
		if !d.IsError || !strings.Contains(d.Content[0].Text, "octo-cli drive share") {
			t.Errorf("describe_op(%q) must refuse and point at the CLI, got %s", opID, d.Content[0].Text)
		}
		c := callTool(t, s, "call_op", map[string]any{"operation_id": opID, "arguments": map[string]any{}})
		if !c.IsError || !strings.Contains(c.Content[0].Text, "octo-cli drive share") {
			t.Errorf("call_op(%q) must refuse and point at the CLI, got %s", opID, c.Content[0].Text)
		}
	}
}

func TestDispatch_NotificationsGetNoResponse(t *testing.T) {
	s := newTestServer(t)
	// A known method sent as a notification (no id) must produce no response.
	for _, method := range []string{"ping", "tools/list", "notifications/initialized", "notifications/progress"} {
		_, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", Method: method})
		if has {
			t.Errorf("notification %q must not receive a response", method)
		}
	}
	// The same method WITH an id is a request and does get a response.
	if _, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "ping"}); !has {
		t.Errorf("ping request (with id) must receive a response")
	}
}

func TestDispatch_InvalidArgumentShapeReturns32602(t *testing.T) {
	s := newTestServer(t)
	// tools/call arguments that are not a JSON object → -32602, not empty-args.
	params, _ := json.Marshal(map[string]any{"name": "describe_op", "arguments": "not-an-object"})
	resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if !has || resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("non-object arguments must return -32602, got %+v", resp)
	}
	// call_op nested arguments that are not an object → -32602.
	params2, _ := json.Marshal(map[string]any{"name": "call_op", "arguments": map[string]any{"operation_id": "message.send", "arguments": []int{1, 2}}})
	resp2, _ := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`2`), Method: "tools/call", Params: params2})
	if resp2.Error == nil || resp2.Error.Code != codeInvalidParams {
		t.Fatalf("non-object call_op arguments must return -32602, got %+v", resp2)
	}
}
