package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- B1: dry-run must not read or return local file contents (multipart) -----

// TestExecute_MultipartDryRunDoesNotOpenNonexistentPath proves the dry-run
// short-circuit happens before any filesystem access: a path that does not
// exist neither opens it nor fails due to filesystem access, and nothing is
// sent to the backend.
func TestExecute_MultipartDryRunDoesNotOpenNonexistentPath(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"dry_run":      true,
		"arguments":    map[string]any{"file_path": "/no/such/path/definitely-absent-xyz.bin"},
	})
	if res.IsError {
		t.Fatalf("multipart dry-run must not fail on a nonexistent path: %s", res.Content[0].Text)
	}
	out := toolText(t, res)
	if out["status"] != "ok" || out["dry_run"] != true {
		t.Errorf("multipart dry-run status=%v dry_run=%v, want ok/true", out["status"], out["dry_run"])
	}
	if be.path != "" {
		t.Errorf("dry-run must not contact the backend; backend saw path=%q", be.path)
	}
}

// TestExecute_MultipartDryRunDoesNotReadFixtureBytes is the load-bearing B1
// proof: a real sensitive fixture is never opened, so its bytes never appear in
// the dry-run result. Removing the multipart dry-run short-circuit makes this
// fail (the engine's dry-run streams the file into envelope.data.body).
func TestExecute_MultipartDryRunDoesNotReadFixtureBytes(t *testing.T) {
	const secret = "TOP-SECRET-FIXTURE-BYTES-MUST-NOT-LEAK-4c1f"
	dir := t.TempDir()
	fixture := filepath.Join(dir, "secret.bin")
	if err := os.WriteFile(fixture, []byte(secret), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"dry_run":      true,
		"arguments":    map[string]any{"file_path": fixture},
	})
	if res.IsError {
		t.Fatalf("multipart dry-run errored: %s", res.Content[0].Text)
	}
	full := res.Content[0].Text
	if strings.Contains(full, secret) {
		t.Errorf("dry-run result leaked fixture bytes; result must describe the request without reading contents:\n%s", full)
	}
	// The path itself (metadata) may appear; the CONTENTS must not.
	out := toolText(t, res)
	if out["dry_run"] != true {
		t.Errorf("expected dry_run:true, got %v", out["dry_run"])
	}
	if be.path != "" {
		t.Errorf("dry-run must not contact the backend; backend saw path=%q", be.path)
	}
}

// TestExecute_MultipartRealExecutionHonorsUploadRootConfinement proves the
// non-dry path still enforces the inherited #177 upload-root confinement (B2 of
// #177): over HTTP with no configured root, a real multipart upload is refused
// before any request — so the dry-run change is not a confinement bypass.
func TestExecute_MultipartRealExecutionHonorsUploadRootConfinement(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.httpMode = true // untrusted transport
	s.uploadRoot = "" // local upload disabled

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"arguments":    map[string]any{"file_path": "/etc/passwd"},
	})
	if !res.IsError {
		t.Fatalf("a real HTTP multipart upload with no upload root must be refused")
	}
	out := toolText(t, res)
	if out["status"] != "validation_error" {
		t.Errorf("status=%v, want validation_error (upload confinement)", out["status"])
	}
	if be.path != "" {
		t.Errorf("no request may reach the backend when confinement refuses; backend saw path=%q", be.path)
	}
}

// TestExecute_MultipartRealExecutionStdioUploads proves the legitimate
// direction: over stdio (trusted transport) a real multipart upload reads the
// file and reaches the backend — the confinement guards HTTP, not stdio.
func TestExecute_MultipartRealExecutionStdioUploads(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(fixture, []byte("hello upload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	be := &backend{reply: `{"data":{"file_key":"fk-1"}}`}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo) // httpMode false (stdio)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload",
		"arguments":    map[string]any{"file_path": fixture},
	})
	if res.IsError {
		t.Fatalf("stdio real multipart upload errored: %s", res.Content[0].Text)
	}
	if be.path != "/v1/bot/file/upload" {
		t.Errorf("real multipart upload should reach the backend, got path=%q", be.path)
	}
}

// --- B2: constraint_enforcement uses the exact runtime condition -------------

// TestConstraintEnforcement_LoopOperationIsExtendedLocal proves a Loop operation
// (which sets Service==loop, not necessarily StrictRequestSchema) is reported as
// locally enforcing the extended checks — the bug the reviewer flagged.
func TestConstraintEnforcement_LoopOperationIsExtendedLocal(t *testing.T) {
	reg := registry.MustNew()
	d, ok := reg.GetOperation("attachment.upload")
	if !ok {
		t.Skip("attachment.upload not embedded")
	}
	if d.Service != "loop" {
		t.Fatalf("precondition: attachment.upload service = %q, want loop", d.Service)
	}
	ce := constraintEnforcement(d)
	for _, k := range []string{"max_items", "min_properties", "min_length", "max_length", "closed_object"} {
		if ce[k] != enforceStrictLocal {
			t.Errorf("loop op %q: %q = %v, want %s (extended checks run for Service==loop)", d.ID, k, ce[k], enforceStrictLocal)
		}
	}
	if ce["extended_local"] != true {
		t.Errorf("loop op must report extended_local=true, got %v", ce["extended_local"])
	}
	if ce["min_items"] != enforceAllServices {
		t.Errorf("min_items must stay all-services, got %v", ce["min_items"])
	}
}

// TestConstraintEnforcement_NonStrictNonLoopControl is the negative control: an
// op that is neither Loop nor strict must report the extended set as
// backend-only.
func TestConstraintEnforcement_NonStrictNonLoopControl(t *testing.T) {
	reg := registry.MustNew()
	// Find an enabled op that is neither loop nor strict.
	var ctrl *registry.OperationDetail
	for _, op := range reg.EnabledOperations() {
		d, ok := reg.GetOperation(op.ID)
		if ok && d.Service != "loop" && !d.StrictRequestSchema {
			ctrl = d
			break
		}
	}
	if ctrl == nil {
		t.Skip("no non-strict non-loop operation embedded")
	}
	ce := constraintEnforcement(ctrl)
	if ce["max_items"] != enforceBackendOnly || ce["min_properties"] != enforceBackendOnly {
		t.Errorf("control op %q must report extended checks backend-only, got max_items=%v min_properties=%v", ctrl.ID, ce["max_items"], ce["min_properties"])
	}
	if ce["extended_local"] != false {
		t.Errorf("control op must report extended_local=false, got %v", ce["extended_local"])
	}
}

// TestConstraintEnforcement_StrictNonLoopIsExtendedLocal proves a strict
// (non-Loop) operation is also reported as extended-local, so the condition
// covers both arms of Service==loop || StrictRequestSchema.
func TestConstraintEnforcement_StrictNonLoopIsExtendedLocal(t *testing.T) {
	reg := registry.MustNew()
	var strictOp *registry.OperationDetail
	for _, op := range reg.EnabledOperations() {
		d, ok := reg.GetOperation(op.ID)
		if ok && d.StrictRequestSchema && d.Service != "loop" {
			strictOp = d
			break
		}
	}
	if strictOp == nil {
		t.Skip("no strict non-loop operation embedded")
	}
	ce := constraintEnforcement(strictOp)
	if ce["max_items"] != enforceStrictLocal || ce["extended_local"] != true {
		t.Errorf("strict op %q must be extended-local, got max_items=%v extended_local=%v", strictOp.ID, ce["max_items"], ce["extended_local"])
	}
}
