package service

import (
	"strings"
	"testing"
)

func validateExpert(relations ...any) *string {
	body := map[string]any{
		"plugin":    map[string]any{"plugin_type": "expert"},
		"relations": relations,
	}
	err := validateMarketplaceRelations(body, false)
	if err == nil {
		return nil
	}
	return &err.Message
}

func TestValidateMarketplaceRelations(t *testing.T) {
	ok := map[string]any{
		"plugin": map[string]any{"plugin_type": "expert"},
		"relations": []any{
			map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1", "target_version_id": "v1"},
		},
	}
	if err := validateMarketplaceRelations(ok, false); err != nil {
		t.Fatalf("valid pinned skill relation rejected: %v", err)
	}
	for _, value := range []any{"v1", ""} {
		connector := map[string]any{
			"plugin": map[string]any{"plugin_type": "expert"},
			"relations": []any{
				map[string]any{"relation_type": "expert_connector", "target_plugin_id": "c1", "target_version_id": value},
			},
		}
		err := validateMarketplaceRelations(connector, false)
		if value == "" && err != nil {
			t.Fatalf("empty connector pin must be treated as absent: %v", err)
		}
		if value != "" && err == nil {
			t.Fatal("non-empty connector pin must be rejected")
		}
	}
	// null values are the wire spelling of "no pin" and must pass.
	nullPin := map[string]any{
		"plugin": map[string]any{"plugin_type": "expert"},
		"relations": []any{
			map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1", "target_version_id": nil},
			map[string]any{"relation_type": "expert_connector", "target_plugin_id": "c1", "target_version_id": nil},
		},
	}
	if err := validateMarketplaceRelations(nullPin, false); err != nil {
		t.Fatalf("null pin must be treated as absent: %v", err)
	}
	if msg := validateExpert(
		map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1"},
		map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1"},
	); msg == nil {
		t.Fatal("duplicate target must be rejected")
	}
	// Present-null relations and an absent relations key both mean "no override".
	for _, body := range []map[string]any{
		{"plugin": map[string]any{"plugin_type": "expert"}, "relations": nil},
		{"plugin": map[string]any{"plugin_type": "expert"}},
	} {
		if err := validateMarketplaceRelations(body, false); err != nil {
			t.Fatalf("absent/null relations must pass: %v", err)
		}
	}
}

func TestValidateMarketplaceRelationsMatrix(t *testing.T) {
	teamSkill := map[string]any{
		"plugin": map[string]any{"plugin_type": "expert_team"},
		"relations": []any{
			map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1"},
		},
	}
	err := validateMarketplaceRelations(teamSkill, false)
	if err == nil || !strings.Contains(err.Message, "expert_team") {
		t.Fatalf("team carrying a skill edge must name the matrix: %v", err)
	}
	expertTeamEdge := map[string]any{
		"plugin": map[string]any{"plugin_type": "expert"},
		"relations": []any{
			map[string]any{"relation_type": "expert_team_expert", "target_plugin_id": "e1"},
		},
	}
	if err := validateMarketplaceRelations(expertTeamEdge, false); err == nil {
		t.Fatal("expert carrying a team-member edge must be rejected")
	}
}

func TestValidateMarketplaceRelationsTeam(t *testing.T) {
	team := func(relations ...any) map[string]any {
		return map[string]any{
			"plugin":    map[string]any{"plugin_type": "expert_team"},
			"relations": relations,
		}
	}
	// Existing teams may carry any member count and legacy wiring; the gate
	// refuses only the declared matrix and data shape.
	for _, rel := range []any{
		map[string]any{"relation_type": "expert_team_expert", "target_plugin_id": "e1", "data": map[string]any{"role": "leader"}},
		map[string]any{"relation_type": "expert_team_expert", "target_plugin_id": "e2"},
	} {
		if err := validateMarketplaceRelations(team(rel), false); err != nil {
			t.Fatalf("legacy member relation rejected: %v", err)
		}
	}
	if err := validateMarketplaceRelations(team(), false); err != nil {
		t.Fatalf("explicit empty relations must stay valid: %v", err)
	}
	badData := team(map[string]any{
		"relation_type": "expert_team_expert", "target_plugin_id": "e1",
		"data": "not-an-object",
	})
	if err := validateMarketplaceRelations(badData, false); err == nil || !strings.Contains(err.Message, "JSON object") {
		t.Fatalf("non-object data must name the field: %v", err)
	}
}

func TestValidateMarketplaceRelationsReviewBody(t *testing.T) {
	body := map[string]any{
		"manifest_json": map[string]any{"plugin_type": "expert_team"},
		"relations": []any{
			map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1"},
		},
	}
	err := validateMarketplaceRelations(body, true)
	if err == nil {
		t.Fatal("review body with a team/skill mismatch must be rejected")
	}
	if !strings.Contains(err.Hint, "inherit the live graph") {
		t.Fatalf("review hint must describe inherit semantics: %q", err.Hint)
	}
}
