package mcp

import (
	"testing"
)

// These tests exercise the PRODUCTION makeFactory path (no factoryFn override),
// so credential + config resolve exactly as they do at runtime. They are the
// regression guard for the stdio forced-space defect: build() resets
// f.Globals.Space, and executeOperation must re-apply it or X-Space-Id never
// reaches the backend. fakeBackendFactory (used by the other integration tests)
// injects the credential directly and therefore cannot catch this.

// realStdioServer wires a Server on the real makeFactory path, pointing the
// engine's env-resolved credential/config at be's fake backend.
func realStdioServer(t *testing.T, be *backend, token string) *Server {
	t.Helper()
	srv := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	t.Setenv("OCTO_TOKEN", token)
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir()) // no stored profiles
	s := newTestServer(t)                    // uses real s.makeFactory (factoryFn nil)
	return s
}

// TestCallOp_StdioForcedSpaceReachesBackend_RealMakeFactory proves the fix: a
// connection-forced space set via TrustedContext survives build() and reaches
// the backend as X-Space-Id on the stdio production path.
func TestCallOp_StdioForcedSpaceReachesBackend_RealMakeFactory(t *testing.T) {
	be := &backend{}
	s := realStdioServer(t, be, "bf_test")
	s.trusted = TrustedContext{SpaceID: "space-forced"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.space != "space-forced" {
		t.Errorf("X-Space-Id = %q, want space-forced (forced space must survive build() on the real path)", be.space)
	}
}

// TestCallOp_NoForcedSpaceOnRealPathSendsNoSpace confirms the negative
// direction: with no forced space and no env space, the stdio path sends no
// X-Space-Id (the guard adds nothing spurious).
func TestCallOp_NoForcedSpaceOnRealPathSendsNoSpace(t *testing.T) {
	be := &backend{}
	s := realStdioServer(t, be, "bf_test") // no OCTO_SPACE_ID, no trusted space

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.space != "" {
		t.Errorf("X-Space-Id = %q, want empty when no space is configured", be.space)
	}
}

// TestCallOp_ForcedSpaceWinsOverEnvSpace_RealMakeFactory proves precedence: when
// the environment supplies OCTO_SPACE_ID but the connection forces a different
// space, the connection-forced value wins on the real credential path.
func TestCallOp_ForcedSpaceWinsOverEnvSpace_RealMakeFactory(t *testing.T) {
	be := &backend{}
	s := realStdioServer(t, be, "bf_test")
	t.Setenv("OCTO_SPACE_ID", "env-space")
	s.trusted = TrustedContext{SpaceID: "forced-space"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.space != "forced-space" {
		t.Errorf("X-Space-Id = %q, want forced-space to win over env-space", be.space)
	}
}

// TestCallOp_EnvSpaceFlowsWhenNoForcedSpace_RealMakeFactory is the other side of
// precedence: with no forced space, the env-supplied space still reaches the
// backend, so the fix does not suppress the legitimate env path.
func TestCallOp_EnvSpaceFlowsWhenNoForcedSpace_RealMakeFactory(t *testing.T) {
	be := &backend{}
	s := realStdioServer(t, be, "bf_test")
	t.Setenv("OCTO_SPACE_ID", "env-space") // no trusted space set

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.space != "env-space" {
		t.Errorf("X-Space-Id = %q, want env-space to flow when nothing is forced", be.space)
	}
}

// TestCallOp_ForcedSpaceOverridesModelSuppliedSpaceArg_RealMakeFactory proves a
// model-supplied space argument cannot override the connection-forced value:
// bot.space-members takes a query `space_id`; the model sets an attacker value,
// the connection forces its own, and the forced value is what reaches the wire
// (both the query param and X-Space-Id).
func TestCallOp_ForcedSpaceOverridesModelSuppliedSpaceArg_RealMakeFactory(t *testing.T) {
	be := &backend{reply: `{"members":[]}`}
	s := realStdioServer(t, be, "bf_test")
	s.trusted = TrustedContext{SpaceID: "forced-space"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "bot.space-members",
		"arguments":    map[string]any{"space_id": "attacker-space"},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.query.Get("space_id") != "forced-space" {
		t.Errorf("query space_id = %q, want forced-space (model value must be overridden)", be.query.Get("space_id"))
	}
	// bot.space-members sets x-octo-space-header:false (space travels as the
	// explicit space_id query param, so no X-Space-Id header is expected here);
	// the query-param override above is the authoritative proof for this op.
}
