package registry

import "testing"

func TestWorksheetDirectoryAndSelectors(test *testing.T) {
	registry := MustNew()
	list, exists := registry.GetOperation("docs.sheet.list")
	if !exists || list.Method != "GET" || list.Path != "/v1/bot/docs/{docId}/sheets" || list.Risk != "read" {
		test.Fatalf("missing directory operation: %+v", list)
	}
	if list.ResponseSchema == nil || list.ResponseSchema.Properties["items"].Type != "array" {
		test.Fatal("directory items schema missing")
	}
	read, exists := registry.GetOperation("docs.sheet.get")
	if !exists || read.ResponseSchema.Properties["sheetId"].Type != "string" {
		test.Fatal("scoped response identity missing")
	}
}
