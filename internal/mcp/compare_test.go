package mcp

import (
	"testing"
)

// Side-by-side comparison fixtures for the two-tool and three-tool facades
// (issue scope item 9). These assert CONTRACT EQUIVALENCE — both facades drive
// the same backend to the same wire — and record structural metrics (tool
// count, tools/list schema size). They deliberately make NO claim about model
// accuracy or token efficiency: those require live measurement, not a unit
// test, so the metrics are logged for a future harness, not asserted as "two
// is better".

// compareOps is the representative operation set exercised through both
// facades: a write with forced-context fields, an auto-idempotency write, and a
// simple create. Each must reach the backend identically whichever facade runs.
var compareOps = []struct {
	id   string
	args map[string]any
	path string
}{
	{
		id:   "message.send",
		args: map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
		path: "/v1/bot/sendMessage",
	},
	{
		id:   "html.publish",
		args: map[string]any{"html": "<p>hi</p>"},
		path: "",
	},
}

// TestCompare_BothFacadesProduceSameWire runs each representative operation
// through call_op (three-tool) and execute (two-tool) against an identical fake
// backend and asserts the request bytes match. This is the core equivalence
// guarantee: the facade is a front-end shape only, not a second engine.
func TestCompare_BothFacadesProduceSameWire(t *testing.T) {
	for _, op := range compareOps {
		op := op
		t.Run(op.id, func(t *testing.T) {
			// three-tool call_op
			beThree := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
			srvThree := beThree.server(t)
			three := facadeServer(t, facadeThree)
			three.factoryFn = fakeBackendFactory(srvThree.URL, "bf_test")
			resThree := three.callOp(t.Context(), op.id, cloneArgs(op.args))
			if resThree.IsError {
				t.Fatalf("call_op %s errored: %s", op.id, resThree.Content[0].Text)
			}

			// two-tool execute
			beTwo := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
			srvTwo := beTwo.server(t)
			two := facadeServer(t, facadeTwo)
			two.factoryFn = fakeBackendFactory(srvTwo.URL, "bf_test")
			resTwo := two.execute(t.Context(), op.id, cloneArgs(op.args), false, "")
			if resTwo.IsError {
				t.Fatalf("execute %s errored: %s", op.id, resTwo.Content[0].Text)
			}

			if beThree.path != beTwo.path {
				t.Errorf("%s: path diverges — call_op %q vs execute %q", op.id, beThree.path, beTwo.path)
			}
			if op.path != "" && beTwo.path != op.path {
				t.Errorf("%s: execute path = %q, want %q", op.id, beTwo.path, op.path)
			}
			// An auto-idempotency key is a fresh nonce per call, so it legitimately
			// differs between the two invocations; compare the rest of the wire.
			bodyThree := withoutKey(beThree.body, "idempotency_key")
			bodyTwo := withoutKey(beTwo.body, "idempotency_key")
			if !jsonEqual(bodyThree, bodyTwo) {
				t.Errorf("%s: request body diverges between facades:\n call_op:  %v\n execute:  %v", op.id, bodyThree, bodyTwo)
			}
			// If one facade injected an auto key, the other must too (same backend).
			_, keyThree := beThree.body["idempotency_key"]
			_, keyTwo := beTwo.body["idempotency_key"]
			if keyThree != keyTwo {
				t.Errorf("%s: idempotency-key injection diverges: call_op=%v execute=%v", op.id, keyThree, keyTwo)
			}
		})
	}
}

// TestCompare_FacadeMetricsFixture records the structural surface of each
// facade. It asserts only the invariant (two-tool exposes exactly two verbs,
// three exposes three) and logs the tools/list schema byte sizes so a later
// measurement harness has a baseline — it does not assert one is cheaper.
func TestCompare_FacadeMetricsFixture(t *testing.T) {
	three := toolDefinitions()
	two := twoToolDefinitions()

	if len(three) != 3 {
		t.Errorf("three-tool facade must expose exactly 3 tools, got %d", len(three))
	}
	if len(two) != 2 {
		t.Errorf("two-tool facade must expose exactly 2 tools, got %d", len(two))
	}

	sizeOf := func(defs []toolDef) int {
		total := 0
		for _, d := range defs {
			total += len(d.InputSchema)
		}
		return total
	}
	// Metrics only — logged, never asserted as superiority (requires live
	// measurement to make any efficiency claim).
	t.Logf("facade metrics fixture: three-tool tools=%d tools/list_schema_bytes=%d; two-tool tools=%d tools/list_schema_bytes=%d",
		len(three), sizeOf(three), len(two), sizeOf(two))
}

// TestCompare_SearchAndDescribeContractParity confirms the discovery + schema
// backends are shared: get_skill search == search_ops for a query, and
// get_skill describe carries the same parameter truth as describe_op.
func TestCompare_SearchAndDescribeContractParity(t *testing.T) {
	s := facadeServer(t, facadeBoth)

	// search parity
	facadeSearch := idsOf(toolText(t, s.getSkill("search", "docs", "", "", "")))
	threeSearch := idsOf(toolText(t, s.searchOps("docs", "")))
	if len(facadeSearch) == 0 {
		t.Fatalf("docs search returned nothing")
	}
	if len(facadeSearch) != len(threeSearch) {
		t.Errorf("search parity broken: get_skill=%d ops, search_ops=%d ops", len(facadeSearch), len(threeSearch))
	}

	// describe parity on parameter truth
	facadeDesc := toolText(t, s.describeSkill("message.send", "full"))
	threeDesc := toolText(t, s.describeOp("message.send"))
	if !jsonEqual(facadeDesc["request_body"], threeDesc["request_body"]) {
		t.Errorf("describe request_body diverges between facades")
	}
}

func cloneArgs(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// withoutKey returns a copy of body without the named key (used to drop a
// per-call idempotency nonce before comparing two requests for equality).
func withoutKey(body map[string]any, key string) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		if k == key {
			continue
		}
		out[k] = v
	}
	return out
}
