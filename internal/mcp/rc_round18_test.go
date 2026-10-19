package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestRound18JSONDepthBudget(t *testing.T) {
	within := strings.Repeat("[", maxMCPJSONDepth) + "0" + strings.Repeat("]", maxMCPJSONDepth)
	if err := validateJSONValueBudget([]byte(within), maxMCPJSONValues); err != nil {
		t.Fatalf("allowed nesting was refused: %v", err)
	}
	tooDeep := "[" + within + "]"
	if err := validateJSONValueBudget([]byte(tooDeep), maxMCPJSONValues); err == nil || !strings.Contains(err.Error(), "maximum depth") {
		t.Fatalf("over-depth value was not refused: %v", err)
	}
}

func TestRound18SearchScopeUsesSingleClassifier(t *testing.T) {
	be := &backend{reply: `{"items":[]}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "uk_test")
	s.trusted = TrustedContext{OnBehalfOf: "OBO-OPERATOR"}

	for _, key := range []string{
		"channel_id", "channel_ids", "channel_type", "channel_types",
		"group_no", "group_id", "thread_id", "short_id", "thread_short_id",
		"conversation_id", "chat_id", "space_id", "target_space_id",
		"principalSpaceId", "tenant_id", "on_behalf_of",
	} {
		if !isScopeField(key) {
			t.Fatalf("scope classifier missed %s", key)
		}
		be.path = ""
		result := s.callOp(context.Background(), "message.search", map[string]any{
			"keyword": "private", "filters": map[string]any{"or": []any{map[string]any{key: "ATTACKER"}}},
		})
		if !result.IsError || be.path != "" {
			t.Fatalf("nested %s escaped the OBO-only pin: result=%v path=%q", key, result, be.path)
		}
	}
	result := s.callOp(context.Background(), "message.search", map[string]any{
		"keyword": "safe", "filters": map[string]any{"sender_ids": []any{"u1"}},
	})
	if result.IsError || be.body["on_behalf_of"] != "OBO-OPERATOR" {
		t.Fatalf("ordinary filter or forced search identity failed: result=%v body=%v", result, be.body)
	}
}

func TestRound18PinnedThreadDeterminesHTMLPlacement(t *testing.T) {
	be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "GROUP-A", ChannelType: "2", ThreadID: "THREAD-OPERATOR"}

	result := s.callOp(context.Background(), "html.publish", map[string]any{
		"html": "<p>private</p>", "mount_type": "group",
	})
	if !result.IsError || be.path != "" {
		t.Fatalf("group placement bypassed pinned thread: result=%v path=%q", result, be.path)
	}
	result = s.callOp(context.Background(), "html.publish", map[string]any{"html": "<p>private</p>"})
	if result.IsError || be.body["mount_type"] != "thread" || be.body["group_no"] != "GROUP-A" || be.body["thread_id"] != "THREAD-OPERATOR" {
		t.Fatalf("thread pin was not applied to placement: result=%v body=%v", result, be.body)
	}
	for _, op := range []string{"html.publish", "html.draft.create"} {
		args := map[string]any{"mount_type": "group", "thread_id": "ATTACKER"}
		forced := s.trusted.apply(op, args)
		if args["mount_type"] != "thread" || args["thread_id"] != "THREAD-OPERATOR" || !forced["mount_type"] {
			t.Fatalf("%s did not enforce thread placement: args=%v forced=%v", op, args, forced)
		}
	}
}

func TestRound18GroupMountStripsCallerThread(t *testing.T) {
	be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "GROUP-A", ChannelType: "2"}
	result := s.callOp(context.Background(), "html.publish", map[string]any{
		"html": "<p>group</p>", "mount_type": "group", "thread_id": "ATTACKER",
	})
	if result.IsError || be.body["mount_type"] != "group" || be.body["group_no"] != "GROUP-A" {
		t.Fatalf("group placement failed: result=%v body=%v", result, be.body)
	}
	if _, present := be.body["thread_id"]; present {
		t.Fatalf("untrusted thread leaked into group mount: body=%v", be.body)
	}
}
