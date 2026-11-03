package mcp

import (
	"fmt"
	"testing"
)

// Two-directional wire pins for the message.search* force-when-configured fix:
// with a configured trusted context the operator --force-* values must reach
// the wire (and be disclosed in _forced_arguments); with an empty context the
// model's values must still flow (the documented cross-channel / OBO default).

var searchFamilyOps = []struct {
	opID        string
	extraArgs   map[string]any
	declareChan bool // declares channel_id/channel_type
	declareOBO  bool // declares on_behalf_of
}{
	{opID: "message.search", extraArgs: map[string]any{"keyword": "x"}, declareChan: true, declareOBO: true},
	{opID: "message.search.all", extraArgs: map[string]any{"keyword": "x"}, declareChan: true, declareOBO: true},
	{opID: "message.search.files", extraArgs: map[string]any{"keyword": "x"}, declareChan: true, declareOBO: true},
	{opID: "message.search.media", extraArgs: map[string]any{}, declareChan: true, declareOBO: true},
	{opID: "message.search.around", extraArgs: map[string]any{"anchor_message_id": "m1"}, declareChan: true, declareOBO: true},
	{opID: "message.search.groups", extraArgs: map[string]any{"keyword": "x"}, declareChan: false, declareOBO: true},
}

func TestCallOp_SearchFamily_ForcedValuesReachWire(t *testing.T) {
	for _, tc := range searchFamilyOps {
		t.Run(tc.opID, func(t *testing.T) {
			be := &backend{reply: `{"items":[]}`}
			srv := be.server(t)
			s := newTestServer(t)
			s.factoryFn = fakeBackendFactory(srv.URL, "bf_search")
			s.trusted = TrustedContext{ChannelID: "C-forced", ChannelType: "1"}

			args := map[string]any{
				"channel_id":   "attacker-c",
				"channel_type": 9,
			}
			for k, v := range tc.extraArgs {
				args[k] = v
			}
			res := callTool(t, s, "call_op", map[string]any{"operation_id": tc.opID, "arguments": args})
			if res.IsError {
				t.Fatalf("call_op %s errored: %s", tc.opID, res.Content[0].Text)
			}

			// Forced values must win on the wire; attacker values nowhere.
			if tc.declareChan {
				if be.body["channel_id"] != "C-forced" {
					t.Errorf("channel_id = %v, want C-forced (operator value must win)", be.body["channel_id"])
				}
				if fmt.Sprint(be.body["channel_type"]) != "1" {
					t.Errorf("channel_type = %v, want 1 (integer, operator value must win)", be.body["channel_type"])
				}
			}
			if be.body["on_behalf_of"] != nil {
				t.Errorf("Bot-only call must omit on_behalf_of")
			}

			// The audit trail must disclose exactly the enforced fields.
			env := toolText(t, res)
			forced, _ := env["_forced_arguments"].([]any)
			got := map[string]bool{}
			for _, f := range forced {
				got[fmt.Sprint(f)] = true
			}
			if tc.declareChan && (!got["channel_id"] || !got["channel_type"]) {
				t.Errorf("_forced_arguments must disclose channel fields, got %v", forced)
			}
			if got["on_behalf_of"] {
				t.Errorf("Bot-only call must not force OBO, got %v", forced)
			}
		})
	}
}

func TestCallOp_SearchFamily_EmptyContextKeepsModelValues(t *testing.T) {
	for _, tc := range searchFamilyOps {
		t.Run(tc.opID, func(t *testing.T) {
			be := &backend{reply: `{"items":[]}`}
			srv := be.server(t)
			s := newTestServer(t) // no trusted context configured
			s.factoryFn = fakeBackendFactory(srv.URL, "bf_search")

			args := map[string]any{
				"channel_id":   "model-chan",
				"channel_type": 9,
			}
			for k, v := range tc.extraArgs {
				args[k] = v
			}
			res := callTool(t, s, "call_op", map[string]any{"operation_id": tc.opID, "arguments": args})
			if res.IsError {
				t.Fatalf("call_op %s errored: %s", tc.opID, res.Content[0].Text)
			}
			if tc.declareChan {
				if be.body["channel_id"] != "model-chan" || fmt.Sprint(be.body["channel_type"]) != "9" {
					t.Errorf("empty context must keep model values: channel_id=%v channel_type=%v", be.body["channel_id"], be.body["channel_type"])
				}
			}
			if be.body["on_behalf_of"] != nil {
				t.Errorf("Bot-only call must omit on_behalf_of")
			}
			if env := toolText(t, res); env["_forced_arguments"] != nil {
				t.Errorf("unforced call must not carry _forced_arguments, got %v", env["_forced_arguments"])
			}
		})
	}
}

// TestDescribeOp_SearchFamily_ReportsServerManaged pins acceptance (b): with a
// configured context, describe_op reflects the enforced fields for every
// message.search* op.
func TestDescribeOp_SearchFamily_ReportsServerManaged(t *testing.T) {
	s := newTestServer(t)
	s.trusted = TrustedContext{ChannelID: "C-forced", ChannelType: "1"}
	for _, tc := range searchFamilyOps {
		t.Run(tc.opID, func(t *testing.T) {
			res := callTool(t, s, "describe_op", map[string]any{"operation_id": tc.opID})
			if res.IsError {
				t.Fatalf("describe_op %s errored: %s", tc.opID, res.Content[0].Text)
			}
			env := toolText(t, res)
			managed, _ := env["server_managed_arguments"].([]any)
			got := map[string]bool{}
			for _, f := range managed {
				got[fmt.Sprint(f)] = true
			}
			if tc.declareChan && (!got["channel_id"] || !got["channel_type"]) {
				t.Errorf("describe_op must mark channel fields server-managed, got %v", managed)
			}
			if got["on_behalf_of"] {
				t.Errorf("Bot-only discovery must not mark OBO as managed, got %v", managed)
			}
		})
	}
}
