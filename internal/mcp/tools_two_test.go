package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// facadeServer builds a test server pinned to a facade so tool gating and the
// two-tool handlers are exercised exactly as a real connection would see them.
func facadeServer(t *testing.T, f facadeMode) *Server {
	t.Helper()
	s := newTestServer(t)
	s.facade = f
	s.clientResources = true // suppress the degrade hint in navigation assertions
	return s
}

func TestToolList_FacadeSelectsSurface(t *testing.T) {
	cases := []struct {
		facade facadeMode
		want   []string
	}{
		{facadeThree, []string{"search_ops", "describe_op", "call_op"}},
		{facadeTwo, []string{"get_skill", "execute"}},
		{facadeBoth, []string{"search_ops", "describe_op", "call_op", "get_skill", "execute"}},
	}
	for _, c := range cases {
		s := facadeServer(t, c.facade)
		defs := s.toolList()
		got := make([]string, len(defs))
		for i, d := range defs {
			got[i] = d.Name
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("facade %q tools = %v, want %v", c.facade, got, c.want)
		}
	}
}

func TestTwoToolSchemas_StayResidentCheap(t *testing.T) {
	total := 0
	for _, d := range twoToolDefinitions() {
		total += len(d.InputSchema)
	}
	// The whole point of the meta/facade design: per-op schemas never leak into
	// tools/list. Two verbs must stay well under the same budget the three do.
	if total > 1024 {
		t.Errorf("two-tool inputSchema = %d bytes, want < 1024 (schemas must not leak into tools/list)", total)
	}
}

// TestFacadeGating_CrossFacadeToolIsUnknown proves that under the two-tool
// facade a three-tool name is rejected as unknown (no silent cross-facade
// dispatch, no alias ambiguity) and vice versa.
func TestFacadeGating_CrossFacadeToolIsUnknown(t *testing.T) {
	two := facadeServer(t, facadeTwo)
	if two.toolEnabled("call_op") {
		t.Errorf("call_op must be unavailable under the two-tool facade")
	}
	if !two.toolEnabled("execute") {
		t.Errorf("execute must be available under the two-tool facade")
	}
	three := facadeServer(t, facadeThree)
	if three.toolEnabled("get_skill") {
		t.Errorf("get_skill must be unavailable under the default three-tool facade")
	}
	both := facadeServer(t, facadeBoth)
	if !both.toolEnabled("call_op") || !both.toolEnabled("execute") {
		t.Errorf("both facades must be available under facade=both")
	}
}

// --- get_skill: search parity ---------------------------------------------

// TestGetSkillSearch_MatchesSearchOps is a contract test: get_skill intent=search
// returns the same operations + skill navigation as the three-tool search_ops,
// because it calls the identical discovery backend.
func TestGetSkillSearch_MatchesSearchOps(t *testing.T) {
	s := facadeServer(t, facadeBoth)
	viaFacade := toolText(t, s.getSkill("search", "", "send message", "", ""))
	viaThree := toolText(t, s.searchOps("", "send message"))

	opsA := idsOf(viaFacade)
	opsB := idsOf(viaThree)
	if strings.Join(opsA, ",") != strings.Join(opsB, ",") {
		t.Errorf("get_skill search ops = %v, search_ops = %v (must be identical)", opsA, opsB)
	}
	if len(opsA) == 0 || !contains(opsA, "message.send") {
		t.Errorf("search for 'send message' should include message.send, got %v", opsA)
	}
}

func TestGetSkillSearch_ModuleMapWithNoArgs(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	payload := toolText(t, s.getSkill("search", "", "", "", ""))
	if _, ok := payload["services"]; !ok {
		t.Errorf("get_skill search with no domain/query must return the module map, got %v", payload)
	}
}

// --- get_skill: describe ----------------------------------------------------

func TestGetSkillDescribe_ReturnsSchemaFingerprintAndConstraints(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	payload := toolText(t, s.getSkill("describe", "", "", "message.send", ""))

	if payload["request_body"] == nil && payload["parameters"] == nil {
		t.Errorf("describe must return the parameter/body schema")
	}
	fp, _ := payload["schema_fingerprint"].(string)
	if !strings.HasPrefix(fp, "sha256:") {
		t.Errorf("describe must return a schema_fingerprint, got %q", fp)
	}
	ce, ok := payload["constraint_enforcement"].(map[string]any)
	if !ok {
		t.Fatalf("describe must return constraint_enforcement")
	}
	// Item 7: min_items enforced locally for every service; max_items and
	// min_properties present.
	if ce["min_items"] != enforceAllServices {
		t.Errorf("min_items must be enforced for all services, got %v", ce["min_items"])
	}
	for _, k := range []string{"max_items", "min_properties"} {
		if _, has := ce[k]; !has {
			t.Errorf("constraint_enforcement must include %q", k)
		}
	}
	skill, ok := payload["skill"].(map[string]any)
	if !ok || skill["name"] != "octo-messaging" {
		t.Errorf("describe must carry the operation's skill block, got %v", payload["skill"])
	}
	if payload["depth"] != "full" {
		t.Errorf("default depth must be full, got %v", payload["depth"])
	}
}

// TestGetSkillDescribe_FullFingerprintEqualsDescribeOpSchema is a contract test:
// the full-depth describe object embeds the same OperationDetail (parameter
// truth) as three-tool describe_op — only augmented, never a second copy.
func TestGetSkillDescribe_MatchesDescribeOpParameterTruth(t *testing.T) {
	s := facadeServer(t, facadeBoth)
	facade := toolText(t, s.describeSkill("docs.create", "full"))
	three := toolText(t, s.describeOp("docs.create"))
	// The spec-derived fields must be byte-identical between the two paths.
	for _, k := range []string{"id", "service", "method", "path", "request_body", "parameters"} {
		if !jsonEqual(facade[k], three[k]) {
			t.Errorf("describe field %q diverges between facades:\n two-tool: %v\n three-tool: %v", k, facade[k], three[k])
		}
	}
}

func TestGetSkillDescribe_SummaryDepthIsShallow(t *testing.T) {
	full := toolText(t, facadeServer(t, facadeTwo).describeSkill("docs.create", "full"))
	summary := toolText(t, facadeServer(t, facadeTwo).describeSkill("docs.create", "summary"))
	if summary["depth"] != "summary" {
		t.Errorf("summary depth label wrong: %v", summary["depth"])
	}
	// A summary body must be an outline (property_names), not the full nested
	// property tree the full view carries.
	sb, _ := summary["request_body"].(map[string]any)
	if sb == nil || sb["property_names"] == nil {
		t.Errorf("summary request_body must expose property_names outline, got %v", summary["request_body"])
	}
	if sb["properties"] != nil {
		t.Errorf("summary request_body must NOT carry the full properties tree")
	}
	fb, _ := full["request_body"].(map[string]any)
	if fb == nil || fb["properties"] == nil {
		t.Errorf("full request_body must carry the properties tree, got %v", full["request_body"])
	}
}

func TestGetSkillDescribe_UnknownIsAmbiguousWithCandidates(t *testing.T) {
	res := facadeServer(t, facadeTwo).describeSkill("message.snd", "full")
	if !res.IsError {
		t.Fatalf("unknown operation must be an error result")
	}
	payload := toolText(t, res)
	if payload["status"] != "ambiguous" {
		t.Errorf("unknown describe target status = %v, want ambiguous", payload["status"])
	}
	if !contains(stringSlice(payload["candidates"]), "message.send") {
		t.Errorf("candidates should include message.send, got %v", payload["candidates"])
	}
}

func TestGetSkill_InvalidIntentIsValidationError(t *testing.T) {
	res := facadeServer(t, facadeTwo).getSkill("frobnicate", "", "", "", "")
	if !res.IsError {
		t.Fatalf("invalid intent must be an error result")
	}
	if toolText(t, res)["status"] != "validation_error" {
		t.Errorf("invalid intent must be a validation_error")
	}
}

func TestGetSkillDescribe_MissingOperationIDIsValidationError(t *testing.T) {
	res := facadeServer(t, facadeTwo).getSkill("describe", "", "", "", "")
	if !res.IsError || toolText(t, res)["status"] != "validation_error" {
		t.Errorf("describe without operation_id must be a validation_error, got %+v", res)
	}
}

// --- execute ----------------------------------------------------------------

func TestExecute_SuccessReusesEngineTransport(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("execute errored: %s", res.Content[0].Text)
	}
	out := toolText(t, res)
	if out["status"] != "ok" {
		t.Errorf("status = %v, want ok", out["status"])
	}
	if be.path != "/v1/bot/sendMessage" {
		t.Errorf("backend path = %q, want /v1/bot/sendMessage (execute must reuse the engine transport)", be.path)
	}
	env, _ := out["envelope"].(map[string]any)
	if env == nil || env["ok"] != true {
		t.Errorf("execute must embed the engine envelope with ok:true, got %v", out["envelope"])
	}
}

func TestExecute_MissingRequiredIsValidationError(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_type": 1, "payload": map[string]any{"text": "x"}}, // channel_id missing
	})
	if !res.IsError {
		t.Fatalf("missing required field must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "validation_error" {
		t.Errorf("status = %v, want validation_error", out["status"])
	}
	if be.path != "" {
		t.Errorf("no request may reach the backend on a pre-flight validation failure, path=%q", be.path)
	}
}

func TestExecute_UnknownOperationIsAmbiguous(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	res := callTool(t, s, "execute", map[string]any{"operation_id": "message.snd", "arguments": map[string]any{}})
	if !res.IsError {
		t.Fatalf("unknown op must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "ambiguous" {
		t.Errorf("status = %v, want ambiguous", out["status"])
	}
	if !contains(stringSlice(out["candidates"]), "message.send") {
		t.Errorf("ambiguous result should suggest candidates, got %v", out["candidates"])
	}
}

// TestExecute_MutatingServerErrorIsResultUnknown proves the Blocker-4 outcome
// semantics: a 5xx on a MUTATING op (POST message.send) may have been applied,
// so it is result_unknown (do-not-auto-retry), not a plain execution_error.
func TestExecute_MutatingServerErrorIsResultUnknown(t *testing.T) {
	be := &backend{status: http.StatusInternalServerError, reply: `{"code":"BOOM","message":"backend exploded"}`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("a 5xx backend response must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "result_unknown" {
		t.Errorf("status = %v, want result_unknown (mutating op + 5xx may have applied)", out["status"])
	}
	if out["code"] != "RESULT_UNKNOWN" || out["retryable"] != false {
		t.Errorf("result_unknown must carry code=RESULT_UNKNOWN and retryable=false, got code=%v retryable=%v", out["code"], out["retryable"])
	}
	env, _ := out["envelope"].(map[string]any)
	if env == nil || env["ok"] != false {
		t.Errorf("result_unknown must carry the ok:false engine envelope, got %v", out["envelope"])
	}
}

// TestExecute_DefiniteRefusalIsExecutionError proves a definite server refusal
// (403 permission — in 4xx, never ambiguous) stays execution_error, not
// result_unknown, even on a mutating op.
func TestExecute_DefiniteRefusalIsExecutionError(t *testing.T) {
	be := &backend{status: http.StatusForbidden, reply: `{"code":"FORBIDDEN","message":"nope"}`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("a 403 must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "execution_error" {
		t.Errorf("status = %v, want execution_error (403 is a definite refusal, not ambiguous)", out["status"])
	}
}

// TestExecute_DryRunDoesNotHitBackend proves the honest dry-run boundary: the
// request is constructed and locally validated, no HTTP is sent, and the result
// says so.
func TestExecute_DryRunDoesNotHitBackend(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
		"dry_run":      true,
	})
	if res.IsError {
		t.Fatalf("dry-run should succeed locally: %s", res.Content[0].Text)
	}
	out := toolText(t, res)
	if out["dry_run"] != true {
		t.Errorf("dry-run result must be marked dry_run:true, got %v", out["dry_run"])
	}
	if be.path != "" {
		t.Errorf("dry-run must NOT send a request; backend saw path=%q", be.path)
	}
	// The engine's dry-run envelope describes the request it would have sent.
	env, _ := out["envelope"].(map[string]any)
	data, _ := env["data"].(map[string]any)
	if data == nil || data["dry_run"] != true {
		t.Errorf("dry-run envelope must describe the constructed request, got %v", env)
	}
}

// TestExecute_DryRunStillValidatesLocally proves dry-run is not a validation
// bypass: a missing required field is still refused before the (skipped) send.
func TestExecute_DryRunStillValidatesLocally(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_type": 1, "payload": map[string]any{"text": "x"}}, // channel_id missing
		"dry_run":      true,
	})
	if !res.IsError || toolText(t, res)["status"] != "validation_error" {
		t.Errorf("dry-run must still fail local validation for a missing required field, got %+v", toolText(t, res))
	}
	if be.path != "" {
		t.Errorf("no request should reach the backend, path=%q", be.path)
	}
}

// TestExecute_StaleFingerprintIsSchemaDrift proves schema-drift detection: a
// fingerprint that does not match the current schema stops the call.
func TestExecute_StaleFingerprintIsSchemaDrift(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id":       "message.send",
		"arguments":          map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
		"schema_fingerprint": "sha256:deadbeefdeadbeef",
	})
	if !res.IsError {
		t.Fatalf("a stale fingerprint must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "schema_drift" {
		t.Errorf("status = %v, want schema_drift", out["status"])
	}
	if be.path != "" {
		t.Errorf("schema drift must stop the call before any request; backend saw path=%q", be.path)
	}
}

// TestExecute_MatchingFingerprintProceeds proves a fingerprint that DOES match
// the current schema is accepted and the call goes through — the legitimate
// direction of the drift guard.
func TestExecute_MatchingFingerprintProceeds(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	detail, _ := s.reg.GetOperation("message.send")
	fp := schemaFingerprint(detail)

	res := callTool(t, s, "execute", map[string]any{
		"operation_id":       "message.send",
		"arguments":          map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
		"schema_fingerprint": fp,
	})
	if res.IsError {
		t.Fatalf("a current fingerprint must be accepted: %s", res.Content[0].Text)
	}
	if toolText(t, res)["status"] != "ok" {
		t.Errorf("matching fingerprint must proceed to a successful call")
	}
	if be.path != "/v1/bot/sendMessage" {
		t.Errorf("the call should have reached the backend, path=%q", be.path)
	}
}

// TestExecute_TrustedContextOverridesModelChannel proves execute reuses the
// same over-privilege防护 authz backend as call_op: the connection's forced
// channel wins over a model-supplied one.
func TestExecute_TrustedContextOverridesModelChannel(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "trusted-chan", SpaceID: "space-forced"}

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "attacker-chan", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if res.IsError {
		t.Fatalf("execute errored: %s", res.Content[0].Text)
	}
	if be.body["channel_id"] != "trusted-chan" {
		t.Errorf("backend channel_id = %v, want the forced trusted-chan", be.body["channel_id"])
	}
	if be.space != "space-forced" {
		t.Errorf("X-Space-Id = %q, want space-forced", be.space)
	}
}

// --- small helpers ----------------------------------------------------------

func idsOf(payload map[string]any) []string {
	ops, _ := payload["operations"].([]any)
	out := make([]string, 0, len(ops))
	for _, o := range ops {
		if m, ok := o.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}

func stringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// jsonEqual reports whether two decoded JSON values are structurally equal.
func jsonEqual(a, b any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
