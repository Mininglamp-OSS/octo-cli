package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLegacyProjectRemovedFromRegistry(t *testing.T) {
	r := MustNew()
	for _, id := range []string{
		"project.list", "project.search", "project.get", "project.create", "project.update", "project.delete",
		"project.resource.list", "project.resource.create", "project.resource.update", "project.resource.delete",
	} {
		if _, ok := r.GetOperation(id); ok {
			t.Errorf("retired operation %s is still discoverable", id)
		}
	}
	for _, op := range r.ListAllOperations() {
		if op.Service == "loop" && strings.HasPrefix(op.ID, "project.") {
			t.Errorf("retired operation %s is still listed", op.ID)
		}
	}
	// Inspect the raw spec as well: hiding commands must not leave stale
	// routes or project_id fields available to schema consumers.
	raw, err := json.Marshal(r.GetSpec("loop"))
	if err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{`/fleet/api/v1/projects`, `"project_id"`, `"project_resource_id"`, `"name":"projects"`} {
		if strings.Contains(string(raw), retired) {
			t.Errorf("raw Loop spec still contains %s", retired)
		}
	}
}

func TestLegacyProjectRemovalPreservesWorkspaceScope(t *testing.T) {
	r := MustNew()
	for _, id := range []string{"task.get", "task.quick_create", "autopilot.create", "autopilot.update", "workspace.get", "workspace.member.list"} {
		op, ok := r.GetOperation(id)
		if !ok {
			t.Fatalf("retained operation %s missing", id)
		}
		found := false
		for _, p := range op.Parameters {
			if p.In == "header" && p.Name == "X-Workspace-ID" && p.Required {
				found = true
			}
		}
		if !found {
			t.Errorf("%s lost its required Workspace header", id)
		}
	}
}
