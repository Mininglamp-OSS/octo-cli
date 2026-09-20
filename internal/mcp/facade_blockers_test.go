package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// disabledServiceOp returns one enabled-registry operation id whose service is
// disabled (withheld), for exercising the withhold path. Fails if none exists.
func disabledServiceOp(t *testing.T) string {
	t.Helper()
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		if !reg.ServiceDisabled(svc) {
			continue
		}
		for _, op := range reg.ListOperations(svc) {
			return op.ID
		}
	}
	t.Skip("no disabled-service operation embedded")
	return ""
}

// --- Blocker 1: metadata-only multipart dry-run VALIDATION -------------------

// TestExecute_MultipartDryRunMissingPathIsValidationError proves a multipart
// dry-run missing a required path field (html.asset.add needs doc_id) is a
// validation_error, not a false "ok", and contacts no backend.
func TestExecute_MultipartDryRunMissingPathIsValidationError(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "html.asset.add",
		"dry_run":      true,
		"arguments":    map[string]any{"file_path": "/tmp/whatever.png"}, // doc_id missing
	})
	if !res.IsError {
		t.Fatalf("missing required path field in a multipart dry-run must be a validation_error")
	}
	out := toolText(t, res)
	if out["status"] != "validation_error" {
		t.Errorf("status = %v, want validation_error", out["status"])
	}
	if be.path != "" {
		t.Errorf("dry-run validation must not contact the backend; saw %q", be.path)
	}
}

// TestExecute_MultipartDryRunMissingFileBindingIsValidationError proves an
// absent multipart file binding (no file_path) is a validation_error.
func TestExecute_MultipartDryRunMissingFileBindingIsValidationError(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"dry_run":      true,
		"arguments":    map[string]any{}, // no file_path
	})
	if !res.IsError {
		t.Fatalf("missing multipart file binding must be a validation_error")
	}
	out := toolText(t, res)
	if out["status"] != "validation_error" {
		t.Errorf("status = %v, want validation_error", out["status"])
	}
	env, _ := out["envelope"].(map[string]any)
	errObj, _ := env["error"].(map[string]any)
	if msg, _ := errObj["message"].(string); !strings.Contains(msg, "file binding") {
		t.Errorf("error should name the missing file binding, got %v", errObj["message"])
	}
}

// TestExecute_MultipartDryRunMissingRequiredHeaderIsValidationError proves a
// required header (attachment.upload needs X-Workspace-ID) is validated.
func TestExecute_MultipartDryRunMissingRequiredHeaderIsValidationError(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "attachment.upload",
		"dry_run":      true,
		"arguments":    map[string]any{"file_path": "/tmp/a.bin"}, // X-Workspace-ID missing
	})
	if !res.IsError {
		t.Fatalf("missing required header in a multipart dry-run must be a validation_error")
	}
	if toolText(t, res)["status"] != "validation_error" {
		t.Errorf("status = %v, want validation_error", toolText(t, res)["status"])
	}
}

// TestExecute_MultipartDryRunValidReturnsPlan proves a valid multipart dry-run
// (all required fields present) returns an honest ok plan with the file field
// bound and no backend contact.
func TestExecute_MultipartDryRunValidReturnsPlan(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "html.asset.add",
		"dry_run":      true,
		"arguments":    map[string]any{"doc_id": "D1", "file_path": "/tmp/logo.png"},
	})
	if res.IsError {
		t.Fatalf("valid multipart dry-run should be ok: %s", res.Content[0].Text)
	}
	out := toolText(t, res)
	if out["status"] != "ok" || out["dry_run"] != true {
		t.Errorf("valid dry-run status=%v dry_run=%v", out["status"], out["dry_run"])
	}
	env, _ := out["envelope"].(map[string]any)
	data, _ := env["data"].(map[string]any)
	if data["file_field"] != "file" || data["file_path"] != "/tmp/logo.png" {
		t.Errorf("plan must bind file_field/file_path, got %v", data)
	}
	if !strings.Contains(data["path"].(string), "D1") {
		t.Errorf("plan path must substitute doc_id, got %v", data["path"])
	}
	if be.path != "" {
		t.Errorf("valid dry-run must not contact the backend; saw %q", be.path)
	}
}

// --- Blocker 2: facade-aware recovery hints ---------------------------------

// TestExecute_UnknownOpHintIsFacadeAware proves that under --facade two the
// unknown-operation hint routes to get_skill, and under three to search_ops.
func TestExecute_UnknownOpHintIsFacadeAware(t *testing.T) {
	two := facadeServer(t, facadeTwo)
	out := toolText(t, two.execute(t.Context(), "message.snd", map[string]any{}, false, ""))
	errObj, _ := out["error"].(map[string]any)
	if h, _ := errObj["hint"].(string); !strings.Contains(h, "get_skill") || strings.Contains(h, "search_ops") {
		t.Errorf("facade two unknown-op hint must route to get_skill, got %q", h)
	}

	three := facadeServer(t, facadeThree)
	p3 := unknownOperationPayload(three.reg, "message.snd", three.hintDiscover())
	e3, _ := p3["error"].(map[string]any)
	if h, _ := e3["hint"].(string); !strings.Contains(h, "search_ops") {
		t.Errorf("facade three unknown-op hint must keep search_ops wording, got %q", h)
	}
}

// TestExecute_DisabledServiceHintIsFacadeAware proves a disabled-service refusal
// under --facade two points at get_skill, not the disabled search_ops.
func TestExecute_DisabledServiceHintIsFacadeAware(t *testing.T) {
	opID := disabledServiceOp(t)
	s := facadeServer(t, facadeTwo)
	out := toolText(t, s.execute(t.Context(), opID, map[string]any{}, false, ""))
	if out["status"] != "validation_error" {
		t.Fatalf("disabled service must be refused, got %v", out["status"])
	}
	env, _ := out["envelope"].(map[string]any)
	errObj, _ := env["error"].(map[string]any)
	hint, _ := errObj["hint"].(string)
	if !strings.Contains(hint, "get_skill") || strings.Contains(hint, "search_ops") {
		t.Errorf("facade two disabled hint must name get_skill, got %q", hint)
	}
}

// TestGetSkill_NoResourcesDegradeHintIsFacadeAware proves the degradation hint
// under --facade two tells the caller to use get_skill intent=describe, not the
// disabled describe_op.
func TestGetSkill_NoResourcesDegradeHintIsFacadeAware(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	s.clientResources = false // client did not negotiate resources
	payload := toolText(t, s.getSkill("search", "", "send message", "", ""))
	ops, _ := payload["operations"].([]any)
	var sawFacadeHint bool
	for _, o := range ops {
		if skill, ok := o.(map[string]any)["skill"].(map[string]any); ok {
			if h, _ := skill["hint"].(string); h != "" {
				if strings.Contains(h, "get_skill intent=describe") && !strings.Contains(h, "describe_op") {
					sawFacadeHint = true
				}
				if strings.Contains(h, "describe_op") {
					t.Errorf("facade two degrade hint must not mention describe_op, got %q", h)
				}
			}
		}
	}
	if !sawFacadeHint {
		t.Errorf("facade two no-resources client must get a get_skill-routed degrade hint")
	}
}

// --- Blocker 4: auth_error distinct + non-mutating read stays execution_error -

// TestExecute_AuthErrorIsDistinct proves a 401 is surfaced as auth_error (not a
// generic retryable execution_error), with retryable=false.
func TestExecute_AuthErrorIsDistinct(t *testing.T) {
	be := &backend{status: http.StatusUnauthorized, reply: `unauthorized`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("a 401 must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "auth_error" {
		t.Errorf("status = %v, want auth_error", out["status"])
	}
	if out["retryable"] != false {
		t.Errorf("auth_error must be retryable=false, got %v", out["retryable"])
	}
}

// TestExecute_ReadServerErrorIsExecutionError proves a 5xx on a NON-mutating
// read (GET event.list) stays execution_error: a safe method leaves no
// ambiguous "may have applied" outcome.
func TestExecute_ReadServerErrorIsExecutionError(t *testing.T) {
	be := &backend{status: http.StatusBadGateway, reply: `{"code":"UPSTREAM","message":"down"}`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{"operation_id": "html.list", "arguments": map[string]any{}})
	if !res.IsError {
		t.Fatalf("a 5xx must be an error result")
	}
	out := toolText(t, res)
	if out["status"] != "execution_error" {
		t.Errorf("status = %v, want execution_error (GET is non-mutating, not result_unknown)", out["status"])
	}
}
