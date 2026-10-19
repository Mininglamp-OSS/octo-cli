package registry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDeclaredDefaultsStayInternalUntilUniformMaterialization(test *testing.T) {
	for _, schema := range []SchemaInfo{
		{Type: "boolean", Default: true},
		{Type: "boolean", Default: false},
		{Type: "string", Default: "value"},
		{Type: "integer", Default: 20},
	} {
		encoded, err := json.Marshal(schema)
		if err != nil {
			test.Fatal(err)
		}
		if strings.Contains(string(encoded), `"default"`) {
			test.Fatalf("schema advertises a default without uniform wire behavior: %s", encoded)
		}
	}
}

func TestBodyBooleanDefaultsSurviveSchemaResolution(test *testing.T) {
	for _, declared := range []bool{false, true} {
		schema := resolveSchema(nil, map[string]any{"allOf": []any{map[string]any{"type": "boolean", "default": declared}}})
		if schema.Default != declared {
			test.Errorf("default=%#v; want %v", schema.Default, declared)
		}
	}
	schema := resolveSchema(nil, map[string]any{"default": false, "allOf": []any{map[string]any{"type": "boolean", "default": true}}})
	if schema.Default != false {
		test.Fatal("allOf must not replace an explicit false default")
	}
}

func TestSheetCleaningSafetyMetadata(test *testing.T) {
	registry := MustNew()
	for _, name := range []string{"docs.sheet.split", "docs.sheet.deduplicate"} {
		operation, found := registry.GetOperation(name)
		if !found || !operation.StrictRequestSchema {
			test.Fatalf("%s must opt into strict validation", name)
		}
		if operation.RequestBody.Properties["preview"].Default != true {
			test.Errorf("%s must declare preview=true", name)
		}
	}
}

func TestSheetProductivitySchemaContract(test *testing.T) {
	registry := MustNew()
	paths := registry.GetSpec("docs")["paths"].(map[string]any)
	for _, name := range []string{"split", "deduplicate"} {
		operation := paths["/v1/bot/docs/{docId}/sheet/"+name].(map[string]any)["post"].(map[string]any)
		request := operation["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
		rangeDescription := request["range"].(map[string]any)["description"].(string)
		if name == "deduplicate" && (!strings.Contains(rangeDescription, "every column of each record") || strings.Contains(rangeDescription, "exactly one column")) {
			test.Fatal("deduplication must explain multi-column record alignment")
		}
		response := operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
		if !strings.Contains(response["cells"].(map[string]any)["description"].(string), "logicalId!row:column") {
			test.Errorf("%s preview cells must document their keys", name)
		}
		if strings.Contains(response["range"].(map[string]any)["description"].(string), "--data") {
			test.Errorf("%s response range must not contain request instructions", name)
		}
	}
	sheet := paths["/v1/bot/docs/{docId}/sheet"].(map[string]any)
	getDescription := sheet["get"].(map[string]any)["description"].(string)
	if !strings.Contains(getDescription, "sheetDataValidations, and sheetConditionalFormats are returned on the first page only") {
		test.Fatal("paged read enumeration must include conditional formats")
	}
	patch := sheet["patch"].(map[string]any)["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	valueSchema := patch["conditionalFormats"].(map[string]any)["additionalProperties"].(map[string]any)
	types, valid := valueSchema["type"].([]any)
	if !valid || len(types) != 2 || types[0] != "object" || types[1] != "null" {
		test.Fatalf("conditional-format deletion must use an OpenAPI 3.1 type union: %#v", valueSchema)
	}
	if _, exists := valueSchema["nullable"]; exists {
		test.Fatal("OpenAPI 3.0 nullable is not the deletion contract")
	}
}
