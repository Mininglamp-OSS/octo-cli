package registry

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestXIN2607SensitivityPaths(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal([]byte(`{"components":{"schemas":{"Private":{"type":"string","writeOnly":true}}},"paths":{}}`), &doc); err != nil {
		t.Fatal(err)
	}
	op := map[string]any{}
	if err := json.Unmarshal([]byte(`{"requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"array":{"type":"array","items":{"$ref":"#/components/schemas/Private"}},"map":{"type":"object","additionalProperties":{"anyOf":[{"$ref":"#/components/schemas/Private"}]}},"recursive":{"allOf":[{"properties":{"password":{"writeOnly":true},"token":{"x-octo-secret":true}}}]}}}}}}}`), &op); err != nil {
		t.Fatal(err)
	}
	got := requestSecretPaths(doc, op)
	want := []string{"array[]", "map.*", "recursive.password", "recursive.token"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sensitivity paths = %v, want %v", got, want)
	}
	r := MustNew()
	if len(r.specs) != 14 {
		t.Fatalf("spec census changed: %d", len(r.specs))
	}
	for name, doc := range r.specs {
		count := 0
		walkOperations(doc, func(_ string, _ string, op map[string]any) { count++; _ = requestSecretPaths(doc, op) })
		t.Logf("%s: %d operation schemas walked", name, count)
	}
}
