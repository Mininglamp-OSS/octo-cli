package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestRound17SearchFiltersCannotEscapeForcedChannel(t *testing.T) {
	be := &backend{reply: `{"items":[]}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "uk_test")
	s.trusted = TrustedContext{ChannelID: "GROUP-A____THREAD-1", ChannelType: "5"}

	for _, filters := range []map[string]any{
		{"channel_ids": []any{"GROUP-B"}},
		{"nested": map[string]any{"channel_types": []any{2}}},
	} {
		be.path = ""
		result := s.callOp(context.Background(), "message.search", map[string]any{"keyword": "confidential", "filters": filters})
		if !result.IsError || be.path != "" {
			t.Fatalf("nested channel selector reached backend: filters=%v result=%v path=%q", filters, result, be.path)
		}
	}

	be.path = ""
	result := s.callOp(context.Background(), "message.search", map[string]any{"keyword": "safe", "filters": map[string]any{"sender_ids": []any{"u1"}}})
	if result.IsError || !strings.HasSuffix(be.path, "/messages/_search") || be.body["channel_id"] != "GROUP-A____THREAD-1" {
		t.Fatalf("safe search filter or forced channel failed: result=%v path=%q body=%v", result, be.path, be.body)
	}
}

func TestRound17HTMLPlacementUsesPinnedChannel(t *testing.T) {
	for _, tc := range []struct {
		scope  TrustedContext
		mount  string
		group  string
		thread string
	}{
		{TrustedContext{ChannelID: "GROUP-A", ChannelType: "2"}, "group", "GROUP-A", ""},
		{TrustedContext{ChannelID: "GROUP-A____THREAD-1", ChannelType: "5"}, "thread", "GROUP-A", "THREAD-1"},
	} {
		be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
		srv := be.server(t)
		s := newTestServer(t)
		s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
		s.trusted = tc.scope

		bad := "space"
		if tc.mount == "group" {
			bad = "thread"
		}
		result := s.callOp(context.Background(), "html.publish", map[string]any{"html": "<p>test</p>", "mount_type": bad, "thread_id": "ATTACKER"})
		if !result.IsError || be.path != "" {
			t.Fatalf("conflicting placement reached backend: result=%v path=%q", result, be.path)
		}
		result = s.callOp(context.Background(), "html.publish", map[string]any{"html": "<p>test</p>"})
		if result.IsError || be.body["mount_type"] != tc.mount || be.body["group_no"] != tc.group {
			t.Fatalf("forced placement did not reach backend: result=%v body=%v", result, be.body)
		}
		if tc.thread != "" && be.body["thread_id"] != tc.thread {
			t.Fatalf("forced thread was lost: body=%v", be.body)
		}
		if tc.thread == "" {
			if _, present := be.body["thread_id"]; present {
				t.Fatalf("unforced thread leaked into group mount: body=%v", be.body)
			}
		}
	}
}

func TestRound17IncompletePinnedThreadRouteFailsBeforeBackend(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "GROUP-A", ChannelType: "2"}
	result := s.callOp(context.Background(), "thread.md-update", map[string]any{"group_no": "GROUP-A", "short_id": "ATTACKER"})
	if !result.IsError || be.path != "" {
		t.Fatalf("unresolved thread route reached backend: result=%v path=%q", result, be.path)
	}
}
