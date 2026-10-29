package registry

import "testing"

func TestLoopProjectOperations(t *testing.T) {
	r := MustNew()
	for _, tt := range []struct{ id, method, path string }{
		{"project.list", "GET", "/fleet/api/v1/projects"},
		{"project.search", "GET", "/fleet/api/v1/projects/_search"},
		{"project.get", "GET", "/fleet/api/v1/projects/{project_id}"},
		{"project.create", "POST", "/fleet/api/v1/projects"},
		{"project.update", "PATCH", "/fleet/api/v1/projects/{project_id}"},
		{"project.delete", "DELETE", "/fleet/api/v1/projects/{project_id}"},
		{"project.resource.list", "GET", "/fleet/api/v1/projects/{project_id}/resources"},
		{"project.resource.create", "POST", "/fleet/api/v1/projects/{project_id}/resources"},
		{"project.resource.update", "PATCH", "/fleet/api/v1/projects/{project_id}/resources/{project_resource_id}"},
		{"project.resource.delete", "DELETE", "/fleet/api/v1/projects/{project_id}/resources/{project_resource_id}"},
	} {
		t.Run(tt.id, func(t *testing.T) {
			op, ok := r.GetOperation(tt.id)
			if !ok {
				t.Fatalf("missing operation %s", tt.id)
			}
			if op.Method != tt.method || op.Path != tt.path {
				t.Fatalf("route = %s %s, want %s %s", op.Method, op.Path, tt.method, tt.path)
			}
			found := false
			for _, p := range op.Parameters {
				if p.Name == "X-Workspace-ID" && p.In == "header" && p.Required {
					found = true
				}
			}
			if !found {
				t.Fatal("project operation must require explicit workspace scope")
			}
		})
	}
}

func TestLoopProjectAssociationSchemas(t *testing.T) {
	r := MustNew()
	for _, tt := range []struct {
		id       string
		nullable bool
	}{
		{"task.create", false}, {"task.update", true}, {"task.quick_create", false},
		{"autopilot.create", true}, {"autopilot.update", true},
	} {
		t.Run(tt.id, func(t *testing.T) {
			op, ok := r.GetOperation(tt.id)
			if !ok || op.RequestBody == nil {
				t.Fatal("request body missing")
			}
			field, ok := op.RequestBody.Properties["project_id"]
			if !ok || field.Type != "string" || field.Format != "uuid" || field.Nullable != tt.nullable {
				t.Fatalf("project_id = %+v (present=%v), want UUID with nullable=%v", field, ok, tt.nullable)
			}
		})
	}
}

func TestLoopProjectTypedBodies(t *testing.T) {
	r := MustNew()
	for _, tt := range []struct {
		id     string
		fields []string
	}{
		{"project.create", []string{"title", "description", "icon", "status", "priority", "lead_type", "lead_id", "lead_octo_uid", "resources"}},
		{"project.update", []string{"title", "description", "icon", "status", "priority", "lead_type", "lead_id", "lead_octo_uid"}},
		{"project.resource.create", []string{"resource_type", "resource_ref", "label", "position"}},
		{"project.resource.update", []string{"resource_ref", "label", "position"}},
	} {
		t.Run(tt.id, func(t *testing.T) {
			op, ok := r.GetOperation(tt.id)
			if !ok || op.RequestBody == nil {
				t.Fatal("request body missing")
			}
			for _, name := range tt.fields {
				if _, ok := op.RequestBody.Properties[name]; !ok {
					t.Errorf("missing body field %s", name)
				}
			}
		})
	}
}
