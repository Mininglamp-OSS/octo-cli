package mcp

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// A schema-only scope audit misses operations with no channel selector at all.
// Keep the entire message-search family classified when its spec changes.
func TestRound20SearchFamilyScopeClassification(t *testing.T) {
	reg := registry.MustNew()
	want := map[string]bool{
		"message.search": true, "message.search.all": true,
		"message.search.files": true, "message.search.media": true,
		"message.search.around": true, "message.search.groups": false,
	}
	for _, op := range reg.EnabledOperations() {
		if op.ID != "message.search" && !strings.HasPrefix(op.ID, "message.search.") {
			continue
		}
		scoped, known := want[op.ID]
		if !known {
			t.Errorf("new message-search operation %s needs an explicit channel-confinement decision", op.ID)
			continue
		}
		detail, ok := reg.GetOperation(op.ID)
		if !ok || detail.RequestBody == nil {
			t.Fatalf("missing request schema for %s", op.ID)
		}
		_, hasID := detail.RequestBody.Properties["channel_id"]
		_, hasType := detail.RequestBody.Properties["channel_type"]
		if hasID != scoped || hasType != scoped {
			t.Errorf("%s channel selectors id=%t type=%t; classified scoped=%t", op.ID, hasID, hasType, scoped)
		}
		delete(want, op.ID)
	}
	if len(want) != 0 {
		t.Errorf("expected message-search operations missing from registry: %v", want)
	}
}

func TestRound20RejectBlankForcedChannel(t *testing.T) {
	for _, id := range []string{" ", "\t\n"} {
		if err := (&TrustedContext{ChannelID: id, ChannelType: "2"}).ValidateBinding(); err == nil {
			t.Errorf("blank forced channel %q can route a scoped search to the global endpoint", id)
		}
	}
}

func TestRound20GlobalGroupSearchChannelBinding(t *testing.T) {
	be := &backend{reply: `{"items":[]}`}
	remote := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(remote.URL, "bf_search")

	for _, bound := range []TrustedContext{
		{ChannelID: "C-FORCED", ChannelType: "2"},
		{ChannelID: "C-FORCED", ChannelType: "2", OnBehalfOf: "O-FORCED"},
	} {
		s.trusted = bound
		be.path = ""
		res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.search.groups", "arguments": map[string]any{"keyword": "fixture"}})
		if !res.IsError || be.path != "" || !strings.Contains(res.Content[0].Text, "global group search") {
			t.Fatalf("global group search escaped forced channel %+v: result=%v path=%q", bound, res, be.path)
		}
	}

	for _, bound := range []TrustedContext{{}, {OnBehalfOf: "O-FORCED"}} {
		s.trusted = bound
		be.path = ""
		res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.search.groups", "arguments": map[string]any{"keyword": "fixture", "on_behalf_of": "O-MODEL"}})
		if res.IsError || !strings.HasSuffix(be.path, "/_search_global_groups") {
			t.Fatalf("legitimate global search refused for %+v: result=%v path=%q", bound, res, be.path)
		}
		wantOBO := "O-MODEL"
		if bound.OnBehalfOf != "" {
			wantOBO = bound.OnBehalfOf
		}
		if be.body["on_behalf_of"] != wantOBO {
			t.Errorf("global search OBO=%v, want %s", be.body["on_behalf_of"], wantOBO)
		}
	}

	s.trusted = TrustedContext{ChannelID: "C-FORCED", ChannelType: "2"}
	be.path = ""
	res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.search", "arguments": map[string]any{"keyword": "fixture"}})
	if res.IsError || be.path == "" || strings.Contains(be.path, "_search_global") || be.body["channel_id"] != "C-FORCED" {
		t.Fatalf("scoped search regressed: result=%v path=%q body=%v", res, be.path, be.body)
	}
}
