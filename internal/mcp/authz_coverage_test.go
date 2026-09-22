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
// guard: every session-bound field (channel_id / channel_type / on_behalf_of /
// space_id) an enabled operation declares must be force-when-configured
// (present in that op's overridableParams table) — the only exception is drive's
// same-named resource space_id (isDriveResourceSpaceID). A new equivalent
// operation, or an op left out of the force table, therefore fails CI here
// instead of silently bypassing over-privilege protection.
func TestAuthzCoverage_EveryEnabledSessionBoundOpIsClassified(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		fields := sessionBoundFieldsOf(reg, op)
		if len(fields) == 0 {
			continue
		}
		table := overridableParams[op.ID]
		for _, f := range fields {
			if f == "space_id" && isDriveResourceSpaceID(op, fields) {
				continue // documented never-force: drive resource id, not Octo space context
			}
			if _, forced := table[f]; !forced {
				t.Errorf("operation %q declares session-bound field %q but does not force it "+
					"(must be force-when-configured, or a documented drive resource space_id)", op.ID, f)
			}
		}
	}
}

// TestAuthzCoverage_ConfiguredForceReachesEverySessionBoundOp is the tightened
// pin (A2): with a fully-configured trusted context, apply must overwrite every
// session-bound field an enabled op declares with the operator value — so no
// operation (the message.search* family included) can silently ignore a
// configured --force-* value. Drive's resource space_id is the sole documented
// exception and must stay model-controlled. Deleting the search-family force
// entries turns this red.
func TestAuthzCoverage_ConfiguredForceReachesEverySessionBoundOp(t *testing.T) {
	reg := registry.MustNew()
	tc := TrustedContext{SpaceID: "S-forced", ChannelID: "C-forced", ChannelType: "1", OnBehalfOf: "O-forced"}
	forcedValue := map[string]string{
		"space_id":     "S-forced",
		"channel_id":   "C-forced",
		"channel_type": "1",
		"on_behalf_of": "O-forced",
	}
	for _, op := range reg.EnabledOperations() {
		fields := sessionBoundFieldsOf(reg, op)
		if len(fields) == 0 {
			continue
		}
		args := map[string]any{}
		for _, f := range fields {
			args[f] = "model-" + f
		}
		tc.apply(op.ID, args)
		for _, f := range fields {
			if f == "space_id" && isDriveResourceSpaceID(op, fields) {
				if args[f] != "model-space_id" {
					t.Errorf("op %q: drive resource space_id must NOT be forced, got %v", op.ID, args[f])
				}
				continue
			}
			if args[f] != forcedValue[f] {
				t.Errorf("op %q: configured --force value for %q must win, got %v (want %q)", op.ID, f, args[f], forcedValue[f])
			}
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
