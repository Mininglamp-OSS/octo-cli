package mcp

import (
	"strings"
	"testing"
)

func TestToolsList_ThreeMetaTools(t *testing.T) {
	defs := toolDefinitions()
	got := make([]string, len(defs))
	for i, d := range defs {
		got[i] = d.Name
	}
	want := []string{"search_ops", "describe_op", "call_op"}
	if len(got) != len(want) {
		t.Fatalf("tools = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tool[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// The whole tools/list schema surface must stay tiny (resident-cheap): the
	// point of the meta-tool design is that per-op schemas are NOT here.
	total := 0
	for _, d := range defs {
		total += len(d.InputSchema)
	}
	if total > 1024 {
		t.Errorf("combined inputSchema = %d bytes, want < 1024 (schemas must not leak into tools/list)", total)
	}
}

func TestSearchOps_KeywordReturnsSkillNavigation(t *testing.T) {
	s := newTestServer(t)
	s.clientResources = true // suppress the degrade hint for this assertion
	res := callTool(t, s, "search_ops", map[string]any{"query": "send message"})
	if res.IsError {
		t.Fatalf("search_ops reported error: %s", res.Content[0].Text)
	}
	payload := toolText(t, res)
	ops, _ := payload["operations"].([]any)
	if len(ops) == 0 {
		t.Fatalf("search_ops returned no operations for 'send message'")
	}
	var found bool
	for _, o := range ops {
		op := o.(map[string]any)
		if op["id"] != "message.send" {
			continue
		}
		found = true
		skill, ok := op["skill"].(map[string]any)
		if !ok {
			t.Fatalf("message.send missing skill navigation block")
		}
		if skill["name"] != "octo-messaging" {
			t.Errorf("skill.name = %v, want octo-messaging", skill["name"])
		}
		if skill["resource_uri"] != "octo://skills/octo-messaging/SKILL.md" {
			t.Errorf("skill.resource_uri = %v", skill["resource_uri"])
		}
		if skill["level"] != levelRequiredBeforeWrite {
			t.Errorf("message.send level = %v, want %s", skill["level"], levelRequiredBeforeWrite)
		}
	}
	if !found {
		t.Errorf("message.send not among search results")
	}
}

func TestSearchOps_ModuleMapExcludesDisabledServices(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "search_ops", map[string]any{})
	payload := toolText(t, res)
	svcs, _ := payload["services"].([]any)
	if len(svcs) == 0 {
		t.Fatalf("module map returned no services")
	}
	for _, sv := range svcs {
		name := sv.(map[string]any)["service"]
		if name == "matter" || name == "summary" {
			t.Errorf("disabled service %v must not appear in the module map", name)
		}
	}
	if payload["mapping_etag"] == "" {
		t.Errorf("module map missing mapping_etag")
	}
}

func TestSearchOps_DisabledDomainReturnsNoteNotOps(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "search_ops", map[string]any{"domain": "matter"})
	payload := toolText(t, res)
	ops, _ := payload["operations"].([]any)
	if len(ops) != 0 {
		t.Errorf("disabled domain matter must expose no operations, got %d", len(ops))
	}
	if _, ok := payload["note"]; !ok {
		t.Errorf("expected a note explaining the domain is unavailable")
	}
}

func TestDescribeOp_ReturnsSchemaPlusSkillAdvice(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "describe_op", map[string]any{"operation_id": "message.send"})
	if res.IsError {
		t.Fatalf("describe_op errored: %s", res.Content[0].Text)
	}
	payload := toolText(t, res)
	// Parameter truth must be present (zero-drift from the spec).
	if payload["request_body"] == nil && payload["parameters"] == nil {
		t.Errorf("describe_op must return the parameter/body schema")
	}
	skill, ok := payload["skill"].(map[string]any)
	if !ok {
		t.Fatalf("describe_op missing skill block")
	}
	advice, _ := skill["advice"].(string)
	if !strings.Contains(advice, "idempotency") {
		t.Errorf("message.send advice should warn about the missing idempotency key, got %q", advice)
	}
	if skill["load_before_call"] != true {
		t.Errorf("required-before-write op must set load_before_call")
	}
}

func TestDescribeOp_UnknownReturnsCandidates(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "describe_op", map[string]any{"operation_id": "message.snd"})
	if !res.IsError {
		t.Fatalf("unknown operation must be an error result")
	}
	payload := toolText(t, res)
	cands, _ := payload["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatalf("unknown op should suggest candidates")
	}
	var hasSend bool
	for _, c := range cands {
		if c == "message.send" {
			hasSend = true
		}
	}
	if !hasSend {
		t.Errorf("candidates for 'message.snd' should include message.send, got %v", cands)
	}
}

func TestSearchOps_NoResourcesClientGetsDegradeHint(t *testing.T) {
	s := newTestServer(t)
	s.clientResources = false // client did not negotiate resources
	res := callTool(t, s, "search_ops", map[string]any{"query": "send message"})
	payload := toolText(t, res)
	ops := payload["operations"].([]any)
	var sawHint bool
	for _, o := range ops {
		if skill, ok := o.(map[string]any)["skill"].(map[string]any); ok {
			if h, _ := skill["hint"].(string); strings.Contains(h, "octo-cli skills") {
				sawHint = true
			}
			if skill["resource_uri"] == "" {
				t.Errorf("resource_uri must still be returned even without resources support")
			}
		}
	}
	if !sawHint {
		t.Errorf("no-resources client must receive the readable fallback hint")
	}
}

func TestSearchOps_ResourcesClientHasNoHint(t *testing.T) {
	s := newTestServer(t)
	s.clientResources = true
	res := callTool(t, s, "search_ops", map[string]any{"query": "send message"})
	payload := toolText(t, res)
	for _, o := range payload["operations"].([]any) {
		if skill, ok := o.(map[string]any)["skill"].(map[string]any); ok {
			if _, has := skill["hint"]; has {
				t.Errorf("resources-capable client must not get a degrade hint")
			}
		}
	}
}
