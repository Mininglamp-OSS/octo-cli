package mcp

import (
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// XIN-2438: execute's `ambiguous` status is real, but it is reserved for an
// operation_id that does not resolve (unknown, or withheld so that it does not
// exist for MCP). It is never a request outcome; a sent mutating request whose
// outcome is uncertain is result_unknown. These tests pin that wire contract
// and the documentation that promises it.

var executeStatuses = []string{"ok", "validation_error", "ambiguous", "auth_error", "result_unknown", "execution_error", "schema_drift"}

func TestXIN2438ExecuteAmbiguousIsUnresolvedOperationOnly(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	cases := []struct {
		op, candidate string
	}{
		{"message.snd", "message.send"},
		// A hidden Loop management operation is not registered, so it is
		// unresolved too, and it is never offered back as a candidate.
		{"workspace.create", ""},
	}
	for _, c := range cases {
		for _, dryRun := range []bool{false, true} {
			res := callTool(t, s, "execute", map[string]any{"operation_id": c.op, "arguments": map[string]any{}, "dry_run": dryRun})
			if !res.IsError {
				t.Fatalf("%s: an unresolved operation must be an error result", c.op)
			}
			out := toolText(t, res)
			if out["status"] != "ambiguous" {
				t.Errorf("%s dry_run=%v: status = %v, want ambiguous", c.op, dryRun, out["status"])
			}
			if out["ok"] != false {
				t.Errorf("%s: ok = %v, want false", c.op, out["ok"])
			}
			errObj, _ := out["error"].(map[string]any)
			if errObj["code"] != "UNKNOWN_OPERATION" || errObj["type"] != "validation" {
				t.Errorf("%s: error = %v, want the shared UNKNOWN_OPERATION validation error", c.op, errObj)
			}
			hint, _ := errObj["hint"].(string)
			if !strings.Contains(hint, "get_skill") || strings.Contains(hint, "search_ops") {
				t.Errorf("%s: hint %q must route to get_skill under --facade two", c.op, hint)
			}
			if _, has := out["envelope"]; has {
				t.Errorf("%s: an unresolved operation has no engine envelope, got %v", c.op, out["envelope"])
			}
			cands := stringSlice(out["candidates"])
			if c.candidate != "" && !contains(cands, c.candidate) {
				t.Errorf("%s: candidates %v must include %s", c.op, cands, c.candidate)
			}
			for _, hidden := range xin2432HiddenLoopOps {
				if contains(cands, hidden) {
					t.Errorf("%s: candidates must not offer hidden operation %s: %v", c.op, hidden, cands)
				}
			}
		}
	}
	if be.path != "" {
		t.Errorf("an unresolved operation must not reach the backend, path=%q", be.path)
	}
}

// TestXIN2438ClassifyFailureNeverAmbiguous checks the failure classifier
// directly: no engine failure, on any method, is reported as ambiguous.
func TestXIN2438ClassifyFailureNeverAmbiguous(t *testing.T) {
	errs := []*output.ExitError{
		nil,
		{Type: "auth_error"},
		{Type: "validation"},
		{Type: "config"},
		{Type: "network"},
		{Type: "api_error", HTTPStatus: 403},
		{Type: "api_error", HTTPStatus: 409},
		{Type: "api_error", HTTPStatus: 429},
		{Type: "api_error", HTTPStatus: 500},
		{Type: "api_error", HTTPStatus: 503},
	}
	envs := []map[string]any{
		nil,
		{"ok": false, "error": map[string]any{"type": "auth_error"}},
		{"ok": false, "error": map[string]any{"type": "validation"}},
		{"ok": false, "error": map[string]any{"type": "network"}},
		{"ok": false, "error": map[string]any{"type": "api_error"}},
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"} {
		for _, e := range errs {
			for _, env := range envs {
				got := classifyFailure(method, env, e)
				if got == "ambiguous" || got == "ok" || !contains(executeStatuses, got) {
					t.Errorf("classifyFailure(%s, %v, %+v) = %q, want a non-ambiguous failure status", method, env, e, got)
				}
			}
		}
	}
}

// TestXIN2438ExecuteStatusContractDocumented keeps the model-facing tool
// description and the CHANGELOG in step with the wire contract above.
func TestXIN2438ExecuteStatusContractDocumented(t *testing.T) {
	var desc string
	for _, d := range twoToolDefinitions() {
		if d.Name == "execute" {
			desc = d.Description
		}
	}
	for _, st := range executeStatuses {
		if !strings.Contains(desc, st) {
			t.Errorf("execute description does not list status %q", st)
		}
	}
	if !strings.Contains(desc, "ambiguous only means operation_id did not resolve to an operation: nothing was sent") {
		t.Errorf("execute description must say what ambiguous means: %s", desc)
	}

	raw, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	log := string(raw)
	start := strings.Index(log, "**`octo-cli mcp serve --facade two|both`")
	end := strings.Index(log, "**`octo-cli mcp serve` (MCP server, v2.3 initial)**")
	if start < 0 || end <= start {
		t.Fatalf("two-tool facade CHANGELOG entry not found")
	}
	entry := log[start:end]
	for _, st := range executeStatuses {
		if !strings.Contains(entry, "`"+st+"`") {
			t.Errorf("CHANGELOG two-tool entry does not list status %q", st)
		}
	}
	if !strings.Contains(entry, "`ambiguous` is returned only when `operation_id` does not resolve") {
		t.Errorf("CHANGELOG two-tool entry must say what ambiguous means")
	}
	for _, r := range entry {
		if unicode.Is(unicode.Han, r) {
			t.Errorf("CHANGELOG two-tool entry must be English only, found %q", r)
			break
		}
	}
}
