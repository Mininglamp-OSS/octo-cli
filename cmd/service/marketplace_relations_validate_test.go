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

func TestValidateMarketplaceRelationsConnectorPinTypes(t *testing.T) {
	for _, value := range []any{123, true, map[string]any{"v": 1}, []any{"v1"}} {
		body := map[string]any{
			"plugin": map[string]any{"plugin_type": "expert"},
			"relations": []any{
				map[string]any{"relation_type": "expert_connector", "target_plugin_id": "c1", "target_version_id": value},
			},
		}
		if err := validateMarketplaceRelations(body, false); err == nil || !strings.Contains(err.Message, "must be a string") {
			t.Fatalf("connector pin %T must fail the type check: %v", value, err)
		}
	}
}

func TestValidateMarketplaceRelationsInheritedReviewType(t *testing.T) {
	// A content-inheriting first review submission omits manifest/plugin docs;
	// the backend resolves the owner type from the stored draft. The local gate
	// must not reject the replacement relation list just because the type is not
	// present in the body.
	body := map[string]any{
		"plugin_id": "p1",
		"version":   "1.0.0",
		"relations": []any{
			map[string]any{"relation_type": "expert_connector", "target_plugin_id": "c1"},
		},
	}
	if err := validateMarketplaceRelations(body, true); err != nil {
		t.Fatalf("inherited review type must defer the owner matrix: %v", err)
	}
	// The same empty owner type on an upsert remains fail-closed.
	if err := validateMarketplaceRelations(body, false); err == nil || !strings.Contains(err.Message, "plugin_type") {
		t.Fatalf("empty upsert owner type must be rejected: %v", err)
	}
}

func TestValidateMarketplaceRelationsRejectsRelationBearingLeaf(t *testing.T) {
	for _, pluginType := range []string{"skill", "connector", "unknown"} {
		body := map[string]any{
			"plugin": map[string]any{"plugin_type": pluginType},
			"relations": []any{
				map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1"},
			},
		}
		if err := validateMarketplaceRelations(body, false); err == nil || !strings.Contains(err.Message, pluginType) {
			t.Fatalf("plugin_type %q carrying relations must be rejected: %v", pluginType, err)
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

func TestValidateMarketplaceRelationsLegacyPluginType(t *testing.T) {
	// Review submissions carry a free-form manifest_json; the documented legacy
	// spelling must fold onto expert_team before the relation matrix runs.
	body := map[string]any{
		"manifest_json": map[string]any{"plugin_type": "squad"},
		"relations": []any{
			map[string]any{"relation_type": "expert_skill", "target_plugin_id": "s1", "target_version_id": "v1"},
		},
	}
	err := validateMarketplaceRelations(body, true)
	if err == nil || !strings.Contains(err.Message, "invalid for an expert_team") {
		t.Fatalf("legacy squad spelling bypassed the matrix: %v", err)
	}
}
