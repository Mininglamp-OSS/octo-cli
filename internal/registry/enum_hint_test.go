package registry

import "testing"

func TestEnumHintAllOfInheritance(t *testing.T) {
	hinted := map[string]any{"type": "string", "enum": []any{"signal"}, "x-octo-enum-hint": "Use the current gallery."}
	for name, schema := range map[string]map[string]any{
		"direct":    {"allOf": []any{hinted}},
		"nested":    {"allOf": []any{map[string]any{"allOf": []any{hinted}}}},
		"reference": {"allOf": []any{map[string]any{"$ref": "#/components/schemas/Template"}}},
	} {
		t.Run(name, func(t *testing.T) {
			doc := map[string]any{"components": map[string]any{"schemas": map[string]any{"Template": hinted}}}
			got := resolveSchema(doc, schema)
			if got.EnumHint != "Use the current gallery." || len(got.Enum) != 1 {
				t.Fatalf("allOf lost enum or hint: %#v", got)
			}
		})
	}
	for name, schema := range map[string]map[string]any{
		"outer wins": {"x-octo-enum-hint": "Outer", "allOf": []any{hinted}},
		"first wins": {"allOf": []any{map[string]any{"x-octo-enum-hint": "Outer"}, hinted}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := resolveSchema(nil, schema).EnumHint; got != "Outer" {
				t.Fatalf("hint precedence = %q, want Outer", got)
			}
		})
	}
}
