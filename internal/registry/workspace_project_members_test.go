package registry

import (
	"reflect"
	"testing"
)

func TestWorkspaceMemberListUsesFleetProjectContract(t *testing.T) {
	op, ok := MustNew().GetOperation("workspace.member.list")
	if !ok {
		t.Fatal("workspace.member.list is missing")
	}
	if op.Path != "/fleet/api/v1/workspaces/{workspace_id}/members" {
		t.Fatalf("member-list path = %q, want the authorized Fleet member endpoint", op.Path)
	}
	if !reflect.DeepEqual(op.AllowedTokenKinds, []string(nil)) {
		t.Errorf("allowed token kinds = %v, want Fleet credential policy", op.AllowedTokenKinds)
	}
	wantRequired := map[string]bool{"workspace_id": true, "X-Workspace-ID": true, "page": false, "page_size": false, "limit": false, "offset": false}
	for _, parameter := range op.Parameters {
		want, exists := wantRequired[parameter.Name]
		if !exists {
			t.Errorf("unexpected Project member parameter %q", parameter.Name)
			continue
		}
		if parameter.Required != want {
			t.Errorf("%s required = %v, want %v", parameter.Name, parameter.Required, want)
		}
		delete(wantRequired, parameter.Name)
	}
	if len(wantRequired) > 0 {
		t.Errorf("missing Project member parameters: %v", wantRequired)
	}
	if op.ResponseSchema == nil {
		t.Fatal("Project member response schema is missing")
	}
	if op.ResponseSchema.Type != "array" || op.ResponseSchema.Items == nil {
		t.Fatal("Project member response must be the forwarded member array")
	}
	for _, name := range []string{"uid", "name", "role", "robot"} {
		if _, exists := op.ResponseSchema.Items.Properties[name]; !exists {
			t.Errorf("Project member response is missing %s", name)
		}
	}
}
