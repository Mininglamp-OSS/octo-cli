package mcp

import (
	"context"
	"testing"
)

func TestRound19StandaloneForcedThreadPinsHTMLPlacement(t *testing.T) {
	be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
	remote := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(remote.URL, "bf_fixture")
	s.trusted = TrustedContext{ThreadID: "THREAD-OPERATOR"}

	for _, op := range []string{"html.publish", "html.draft.create"} {
		if !s.trusted.serverManaged(op)["mount_type"] {
			t.Fatalf("%s did not describe pinned placement as server-managed", op)
		}
		for _, mount := range []string{"space", "group"} {
			be.path = ""
			res := s.callOp(context.Background(), op, map[string]any{
				"html": "<p>fixture</p>", "mount_type": mount,
				"group_no": "GROUP-CALLER", "thread_id": "THREAD-CALLER",
			})
			if !res.IsError || be.path != "" {
				t.Fatalf("%s mount=%s escaped standalone thread pin: result=%v path=%q", op, mount, res, be.path)
			}
		}
		for _, explicit := range []bool{false, true} {
			be.path = ""
			args := map[string]any{"html": "<p>fixture</p>", "thread_id": "THREAD-CALLER", "group_no": "GROUP-CALLER"}
			if explicit {
				args["mount_type"] = "thread"
			}
			res := s.callOp(context.Background(), op, args)
			if res.IsError || be.path == "" || be.body["mount_type"] != "thread" || be.body["thread_id"] != "THREAD-OPERATOR" {
				t.Fatalf("%s explicit=%t did not reach pinned thread: result=%v path=%q body=%v", op, explicit, res, be.path, be.body)
			}
			if _, present := be.body["group_no"]; present {
				t.Fatalf("%s retained caller group alongside standalone thread: %v", op, be.body)
			}
		}
	}

	s.trusted = TrustedContext{}
	be.path = ""
	res := s.callOp(context.Background(), "html.publish", map[string]any{
		"html": "<p>fixture</p>", "mount_type": "group", "group_no": "GROUP-CALLER",
	})
	if res.IsError || be.path == "" || be.body["mount_type"] != "group" {
		t.Fatalf("unconfigured group placement regressed: result=%v path=%q body=%v", res, be.path, be.body)
	}
}
