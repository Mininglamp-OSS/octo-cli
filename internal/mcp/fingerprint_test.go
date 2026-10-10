package mcp

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestSchemaFingerprint_Deterministic(t *testing.T) {
	reg := registry.MustNew()
	d1, _ := reg.GetOperation("message.send")
	d2, _ := reg.GetOperation("message.send")
	if got1, got2 := schemaFingerprint(d1), schemaFingerprint(d2); got1 != got2 {
		t.Errorf("fingerprint not deterministic: %q vs %q", got1, got2)
	}
	// Independently re-loaded registry must agree — the value is spec-derived, so
	// two processes reading the same embedded spec produce the same fingerprint.
	reg2 := registry.MustNew()
	d3, _ := reg2.GetOperation("message.send")
	if schemaFingerprint(d1) != schemaFingerprint(d3) {
		t.Errorf("fingerprint differs across registry instances for the same spec")
	}
}

func TestSchemaFingerprint_DiffersByOperation(t *testing.T) {
	reg := registry.MustNew()
	send, _ := reg.GetOperation("message.send")
	create, _ := reg.GetOperation("docs.create")
	if schemaFingerprint(send) == schemaFingerprint(create) {
		t.Errorf("distinct operations must not share a fingerprint")
	}
}

// TestSchemaFingerprint_TracksSchemaChange proves it detects drift: mutating a
// copy of the schema changes the fingerprint. This is the property that lets
// execute reject a stale caller.
func TestSchemaFingerprint_TracksSchemaChange(t *testing.T) {
	reg := registry.MustNew()
	base, _ := reg.GetOperation("message.send")
	before := schemaFingerprint(base)

	mutated := *base
	mutated.Path = base.Path + "/changed"
	if schemaFingerprint(&mutated) == before {
		t.Errorf("a changed path must change the fingerprint (drift detection)")
	}
}

func TestConstraintEnforcement_ReflectsStrictFlag(t *testing.T) {
	loose := &registry.OperationDetail{StrictRequestSchema: false}
	strict := &registry.OperationDetail{StrictRequestSchema: true}

	ceLoose := constraintEnforcement(loose)
	ceStrict := constraintEnforcement(strict)

	// min_items / required / enum are enforced for every service, strict or not.
	for _, k := range []string{"min_items", "required", "enum"} {
		if ceLoose[k] != enforceAllServices || ceStrict[k] != enforceAllServices {
			t.Errorf("%q must be all-services in both modes", k)
		}
	}
	// The extended set is local only for strict operations.
	for _, k := range []string{"max_items", "min_properties", "min_length", "max_length", "closed_object"} {
		if ceLoose[k] != enforceBackendOnly {
			t.Errorf("non-strict op: %q should be backend-only, got %v", k, ceLoose[k])
		}
		if ceStrict[k] != enforceStrictLocal {
			t.Errorf("strict op: %q should be locally enforced, got %v", k, ceStrict[k])
		}
	}
}

// TestConstraintEnforcement_RealStrictOperation checks the projection against a
// real strict-schema operation (Loop Public API) rather than a synthetic one,
// so it stays honest to actual runtime behaviour.
func TestConstraintEnforcement_RealStrictOperation(t *testing.T) {
	reg := registry.MustNew()
	var strictOp *registry.OperationDetail
	for _, op := range reg.EnabledOperations() {
		d, ok := reg.GetOperation(op.ID)
		if ok && d.StrictRequestSchema {
			strictOp = d
			break
		}
	}
	if strictOp == nil {
		t.Skip("no strict-request-schema operation embedded; nothing to assert")
	}
	ce := constraintEnforcement(strictOp)
	if ce["max_items"] != enforceStrictLocal || ce["min_properties"] != enforceStrictLocal {
		t.Errorf("strict op %q must report local enforcement for max_items/min_properties, got %v", strictOp.ID, ce)
	}
}
