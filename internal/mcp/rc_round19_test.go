package mcp

import "testing"

func TestRound19StandaloneForcedThreadPinsHTMLPlacement(t *testing.T) {
	be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
	remote := be.server(t)
	for _, tool := range []string{"call_op", "execute"} {
		s := newTestServer(t).WithFacade(facadeBoth).WithTrustedContext(TrustedContext{ThreadID: "THREAD-OPERATOR"})
		s.factoryFn = fakeBackendFactory(remote.URL, "bf_fixture")
		for _, op := range []string{"html.publish", "html.draft.create"} {
			if !s.trusted.serverManaged(op)["mount_type"] {
				t.Fatalf("%s did not describe pinned placement as server-managed", op)
			}
			for _, mount := range []string{"space", "group"} {
				be.path = ""
				res := callTool(t, s, tool, map[string]any{"operation_id": op, "arguments": map[string]any{
					"html": "<p>fixture</p>", "mount_type": mount,
					"group_no": "GROUP-CALLER", "thread_id": "THREAD-CALLER",
				}})
				if !res.IsError || be.path != "" {
					t.Fatalf("%s %s mount=%s escaped standalone thread pin: result=%v path=%q", tool, op, mount, res, be.path)
				}
			}
			for _, explicit := range []bool{false, true} {
				be.path = ""
				args := map[string]any{"html": "<p>fixture</p>", "thread_id": "THREAD-CALLER", "group_no": "GROUP-CALLER"}
				if explicit {
					args["mount_type"] = "thread"
				}
				res := callTool(t, s, tool, map[string]any{"operation_id": op, "arguments": args})
				if res.IsError || be.path == "" || be.body["mount_type"] != "thread" || be.body["thread_id"] != "THREAD-OPERATOR" {
					t.Fatalf("%s %s explicit=%t did not reach pinned thread: result=%v path=%q body=%v", tool, op, explicit, res, be.path, be.body)
				}
				if _, present := be.body["group_no"]; present {
					t.Fatalf("%s %s retained caller group alongside standalone thread: %v", tool, op, be.body)
				}
			}
		}
	}

	s := newTestServer(t).WithFacade(facadeBoth)
	s.factoryFn = fakeBackendFactory(remote.URL, "bf_fixture")
	be.path = ""
	res := callTool(t, s, "execute", map[string]any{"operation_id": "html.publish", "arguments": map[string]any{
		"html": "<p>fixture</p>", "mount_type": "group", "group_no": "GROUP-CALLER",
	}})
	if res.IsError || be.path == "" || be.body["mount_type"] != "group" {
		t.Fatalf("unconfigured group placement regressed: result=%v path=%q body=%v", res, be.path, be.body)
	}
}
