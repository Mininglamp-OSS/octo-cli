package mcp

import (
	"sort"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- per-operation authorization coverage guard ---

// sessionBoundFieldsOf returns the session-bound argument names an operation
// declares (query/body), across params and request body.
func sessionBoundFieldsOf(reg *registry.Registry, op registry.OperationInfo) []string {
	d, ok := reg.GetOperation(op.ID)
	if !ok {
		return nil
	}
	found := map[string]bool{}
	for i := range d.Parameters {
		if sessionBoundFields[d.Parameters[i].Name] {
			found[d.Parameters[i].Name] = true
		}
	}
	if d.RequestBody != nil {
		for name := range d.RequestBody.Properties {
			if sessionBoundFields[name] {
				found[name] = true
			}
		}
	}
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestAuthzCoverage_EveryEnabledSessionBoundOpIsClassified is the spec-driven
// guard: any enabled operation declaring channel_id / channel_type /
// on_behalf_of must be either forced (overridableParams) or carry a documented
// exclusion (authzExclusions). A newly added equivalent operation therefore
// fails CI here instead of silently bypassing over-privilege protection.
func TestAuthzCoverage_EveryEnabledSessionBoundOpIsClassified(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		fields := sessionBoundFieldsOf(reg, op)
		if len(fields) == 0 {
			continue
		}
		_, forced := overridableParams[op.ID]
		_, excluded := authzExclusions[op.ID]
		driveResource := isDriveResourceSpaceID(op, fields)
		if !forced && !excluded && !driveResource {
			t.Errorf("operation %q declares session-bound fields %v but is neither forced, documented-excluded, nor a drive resource space_id", op.ID, fields)
		}
		if forced && excluded {
			t.Errorf("operation %q is both forced and excluded; pick one", op.ID)
		}
	}
}

// TestAuthzCoverage_SpaceIDIsClassified pins the specific space_id fixes: the
// header-suppressed group ops are forced, and drive's same-named resource id is
// the documented exclusion (never force-injected).
func TestAuthzCoverage_SpaceIDIsClassified(t *testing.T) {
	if !sessionBoundFields["space_id"] {
		t.Fatal("space_id must be a session-bound field so the guard cannot be blind to it")
	}
	for _, opID := range []string{"group.create", "group.list", "bot.space-members"} {
		if _, ok := overridableParams[opID]["space_id"]; !ok {
			t.Errorf("%q must force space_id", opID)
		}
	}
	reg := registry.MustNew()
	// A drive op carrying space_id must be classified only via the documented
	// drive-resource exclusion, never force-injected.
	d, _ := reg.GetOperation("drive.folder.create")
	if _, forced := overridableParams["drive.folder.create"]; forced {
		t.Error("drive.folder.create space_id must NOT be force-injected (it is a drive resource id)")
	}
	if !isDriveResourceSpaceID(d.OperationInfo, sessionBoundFieldsOf(reg, d.OperationInfo)) {
		t.Error("drive.folder.create should be classified as a drive resource space_id")
	}
}

// TestAuthzCoverage_ForcedOpsForceEveryDeclaredSessionField ensures a forced op
// does not half-cover: every session-bound field it declares must be in its
// force table (so a partially-covered op can't leak channel/OBO).
func TestAuthzCoverage_ForcedOpsForceEveryDeclaredSessionField(t *testing.T) {
	reg := registry.MustNew()
	for opID, table := range overridableParams {
		d, ok := reg.GetOperation(opID)
		if !ok {
			t.Errorf("overridableParams references unknown op %q", opID)
			continue
		}
		for _, f := range sessionBoundFieldsOf(reg, d.OperationInfo) {
			if _, ok := table[f]; !ok {
				t.Errorf("op %q declares session-bound field %q but does not force it", opID, f)
			}
		}
	}
}

// TestAuthzCoverage_ResolvesReviewerNamedOps pins the three ops the reviewer
// named as forced with their declared channel fields.
func TestAuthzCoverage_ResolvesReviewerNamedOps(t *testing.T) {
	for _, opID := range []string{"message.edit", "message.read-receipt", "bot.typing"} {
		table, ok := overridableParams[opID]
		if !ok {
			t.Errorf("%q must be forced", opID)
			continue
		}
		if _, ok := table["channel_id"]; !ok {
			t.Errorf("%q must force channel_id", opID)
		}
		if _, ok := table["channel_type"]; !ok {
			t.Errorf("%q must force channel_type", opID)
		}
	}
	if _, ok := overridableParams["bot.typing"]["on_behalf_of"]; !ok {
		t.Errorf("bot.typing must also force on_behalf_of")
	}
}
