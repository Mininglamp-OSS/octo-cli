package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Round-17: an explicit JSON `null` walked past both local gates this PR exists to add.
//
// validateObject visited only non-nil children:
//
//	for name := range schema.Properties {
//	    if child, exists := obj[name]; exists && child != nil {
//
// so a property *present with value null* never reached checkEnum or checkUint64Field, and
// was marshalled onto the wire unchanged. checkEnum's own docstring says this case must be
// refused — "An object / array / null where the schema declares a scalar vocabulary cannot
// match any member, so reject rather than forward" — so the function written to catch it was
// simply never called.
//
// Required fields were never affected: validateObject treats a present null as missing and
// reports it that way. The exposure was the *optional* constrained properties, and the
// outcome now agrees across both — a present null is never accepted for a constrained field.
// A required one is reported as missing, an optional one as an invalid value.
//
// Reproduced against a binary built from 2153838 before the fix:
//
//	drive doc mount    --dry-run --data '{…,"source":null}'    -> ok:true, body carried "source":null
//	drive folder create --dry-run --data '{…,"parent_id":null}' -> ok:true, body carried "parent_id":null
//
// while the same fields with an out-of-vocabulary or wrongly-typed value were correctly
// refused (ENUM_NOT_ALLOWED / VALIDATION_ERROR).

// TestNullIsRefusedOnAConstrainedField is the direct behavioural pin, using a synthetic
// schema so every combination of (required, optional) × (enum, uint64) is present whether or
// not a spec happens to declare it today.
func TestNullIsRefusedOnAConstrainedField(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type:     "object",
		Required: []string{"req_enum"},
		Properties: map[string]registry.SchemaInfo{
			"req_enum":  {Type: "string", Enum: []any{"a", "b"}},
			"opt_enum":  {Type: "string", Enum: []any{"a", "b"}},
			"opt_id":    {Type: "integer", Format: "uint64"},
			"free_text": {Type: "string"},
			"nested": {Type: "object", Properties: map[string]registry.SchemaInfo{
				"inner_enum": {Type: "string", Enum: []any{"x"}},
				"inner_id":   {Type: "integer", Format: "uint64"},
			}},
			"items": {Type: "array", Items: &registry.SchemaInfo{
				Type: "object", Properties: map[string]registry.SchemaInfo{
					"kind": {Type: "string", Enum: []any{"k"}},
				},
			}},
		},
	}
	v := bodySchemaValidator{flagFor: map[string]string{"opt_enum": "opt-enum", "opt_id": "opt-id"}}

	for _, tc := range []struct {
		name     string
		body     map[string]any
		wantCode string
		wantText string
	}{
		{
			name:     "optional enum field set to null",
			body:     map[string]any{"req_enum": "a", "opt_enum": nil},
			wantCode: enumNotAllowed,
			wantText: "--opt-enum",
		},
		{
			name:     "optional uint64 field set to null",
			body:     map[string]any{"req_enum": "a", "opt_id": nil},
			wantCode: "VALIDATION_ERROR",
			wantText: "--opt-id",
		},
		{
			name:     "nested enum field set to null",
			body:     map[string]any{"req_enum": "a", "nested": map[string]any{"inner_enum": nil}},
			wantCode: enumNotAllowed,
			wantText: "nested.inner_enum",
		},
		{
			name:     "nested uint64 field set to null",
			body:     map[string]any{"req_enum": "a", "nested": map[string]any{"inner_id": nil}},
			wantCode: "VALIDATION_ERROR",
			wantText: "nested.inner_id",
		},
		{
			name:     "enum inside an array of objects set to null",
			body:     map[string]any{"req_enum": "a", "items": []any{map[string]any{"kind": nil}}},
			wantCode: enumNotAllowed,
			wantText: "items[0].kind",
		},
		{
			// Unchanged behaviour, kept here so the required path is visibly a
			// refusal too rather than left to a different test file.
			name:     "required enum field set to null is still reported as missing",
			body:     map[string]any{"req_enum": nil},
			wantCode: "VALIDATION_ERROR",
			wantText: "missing required field(s): req_enum",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := v.validate(schema, tc.body, "", "")
			if err == nil {
				t.Fatalf("body %#v was accepted — a present null on a constrained field is "+
					"forwarded to the backend, which is the bypass this closes", tc.body)
			}
			if err.Code != tc.wantCode {
				t.Errorf("code = %s, want %s", err.Code, tc.wantCode)
			}
			if !strings.Contains(err.Message, tc.wantText) {
				t.Errorf("message %q should reference %q", err.Message, tc.wantText)
			}
		})
	}

	// The allow direction, and the bound on the change: a null on a property with no
	// enum and no uint64 format is still forwarded. Refusing it too would be a wider
	// contract change than the defect calls for — a backend may accept null to clear an
	// optional field — and nothing in review asked for it.
	for _, body := range []map[string]any{
		{"req_enum": "a", "free_text": nil},
		{"req_enum": "a", "nested": map[string]any{}},
		{"req_enum": "a", "opt_enum": "b", "opt_id": mustJSONNumber("18446744073709551615")},
	} {
		if err := v.validate(schema, body, "", ""); err != nil {
			t.Errorf("body %#v must still be accepted: %v", body, err)
		}
	}
}

// TestNullIsRefusedOnEveryConstrainedSpecField is the derived form of the same property, and
// the reason it is derived rather than a list: review named six reachable fields, and a list
// of six goes stale the first time a spec gains a seventh.
//
// It walks every operation in every embedded spec, finds each request-body property that
// carries an enum or `format: uint64` at any depth, builds the smallest body that reaches
// that property with an explicit null, and asserts the validator refuses it.
//
// This doubles as the census the round asked for: the reachable set is *reported* by the test
// rather than asserted to be a particular size, so a new constrained field is covered the
// moment it is declared.
func TestNullIsRefusedOnEveryConstrainedSpecField(t *testing.T) {
	reg := registry.MustNew()
	var checked int

	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok || d.RequestBody == nil {
				continue
			}
			for _, field := range constrainedLeaves(d.RequestBody, nil) {
				checked++
				body := bodyWithNullAt(d.RequestBody, field)
				v := bodySchemaValidator{}
				err := v.validate(d.RequestBody, body, "", "")
				if err == nil {
					t.Errorf("%s: %s set to an explicit null was accepted and would be sent "+
						"upstream, skipping the %s gate", info.ID, strings.Join(field, "."),
						constraintName(d.RequestBody, field))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no constrained body field was found in any spec, so this census asserted nothing")
	}
	t.Logf("census covered %d constrained request-body leaves across all embedded specs", checked)
}

// constrainedLeaves returns the path to every request-body property that carries an enum or
// a uint64 format, descending through objects and through an array's item schema.
//
// An array item is addressed by the array's own path: bodyWithNullAt wraps the value in a
// one-element slice, which is the smallest body that reaches an item schema.
func constrainedLeaves(schema *registry.SchemaInfo, at []string) [][]string {
	var out [][]string
	for name := range schema.Properties {
		prop := schema.Properties[name]
		path := append(append([]string{}, at...), name)
		if len(prop.Enum) > 0 || prop.Format == uint64Format {
			out = append(out, path)
			continue
		}
		if prop.Type == "object" || prop.Items != nil {
			out = append(out, constrainedLeaves(&prop, path)...)
		}
	}
	if schema.Items != nil {
		out = append(out, constrainedLeaves(schema.Items, at)...)
	}
	return out
}

// constraintName says which gate the field would have skipped, for a failure message that
// names the actual hole rather than "validation".
func constraintName(schema *registry.SchemaInfo, field []string) string {
	cur := schema
	for _, name := range field {
		if cur.Items != nil && cur.Properties == nil {
			cur = cur.Items
		}
		next, ok := cur.Properties[name]
		if !ok {
			return "enum/uint64"
		}
		cur = &next
	}
	if cur.Format == uint64Format {
		return "uint64"
	}
	return "enum"
}

// bodyWithNullAt builds the smallest body that places an explicit null at field, filling in
// every required property along the way so the walk reaches the leaf instead of stopping at
// a missing-required-field error.
func bodyWithNullAt(schema *registry.SchemaInfo, field []string) map[string]any {
	body := fillRequired(schema, field[0])
	if len(field) == 1 {
		body[field[0]] = nil
		return body
	}
	child := schema.Properties[field[0]]
	inner := bodyWithNullAt(childObjectSchema(&child), field[1:])
	if child.Type == "array" || (child.Items != nil && child.Type != "object") {
		body[field[0]] = []any{inner}
		return body
	}
	body[field[0]] = inner
	return body
}

// childObjectSchema unwraps an array schema to the object schema its items carry.
func childObjectSchema(schema *registry.SchemaInfo) *registry.SchemaInfo {
	if schema.Type != "object" && schema.Items != nil {
		return schema.Items
	}
	return schema
}

// fillRequired supplies a schema-satisfying placeholder for every required property except
// the one under test, so the missing-required-field check does not mask the constraint being
// exercised.
func fillRequired(schema *registry.SchemaInfo, except string) map[string]any {
	body := map[string]any{}
	for _, name := range schema.Required {
		if name == except {
			continue
		}
		prop := schema.Properties[name]
		body[name] = placeholderFor(&prop)
	}
	return body
}

// placeholderFor returns a value the validator accepts for a schema, so a required field can
// be filled without tripping the very gates under test.
func placeholderFor(schema *registry.SchemaInfo) any {
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}
	if schema.Format == uint64Format {
		return mustJSONNumber("1")
	}
	switch schema.Type {
	case "object":
		return fillRequired(schema, "")
	case "array":
		return []any{}
	case "integer", "number":
		return mustJSONNumber("1")
	case "boolean":
		return true
	}
	return "x"
}

// mustJSONNumber mirrors how --data reaches the validator: decoded with UseNumber, so every
// number is a json.Number rather than a float64. The validator distinguishes the two, so a
// placeholder of the wrong type would make the census fail for the wrong reason.
func mustJSONNumber(digits string) any {
	return json.Number(digits)
}
