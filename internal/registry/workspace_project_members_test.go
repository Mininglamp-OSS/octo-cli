package registry

import (
	"reflect"
	"testing"
)

func TestWorkspaceMemberListUsesOctoProjectContract(t *testing.T) {
	op, ok := MustNew().GetOperation("workspace.member.list")
	if !ok {
		t.Fatal("workspace.member.list is missing")
	}
	if op.Path != "/v1/bot/projects/{workspace_id}/members" {
		t.Fatalf("member-list path = %q, want the Octo Project Bot member endpoint", op.Path)
	}
	if !reflect.DeepEqual(op.AllowedTokenKinds, []string{"user_bot"}) {
		t.Errorf("allowed token kinds = %v, want only user_bot", op.AllowedTokenKinds)
	}
	wantRequired := map[string]bool{"workspace_id": true, "space_id": true, "obo": true, "limit": false, "offset": false}
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
	items := op.ResponseSchema.Properties["items"]
	if items.Type != "array" || items.Items == nil {
		t.Fatal("Project member response must expose items")
	}
	for _, name := range []string{"uid", "name", "role", "robot"} {
		if _, exists := items.Items.Properties[name]; !exists {
			t.Errorf("Project member response is missing %s", name)
		}
	}
	for _, name := range []string{"total", "limit", "offset", "decision_id"} {
		if _, exists := op.ResponseSchema.Properties[name]; !exists {
			t.Errorf("Project member page is missing %s", name)
		}
	}
}
