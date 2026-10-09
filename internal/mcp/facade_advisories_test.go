package mcp

import (
	"encoding/json"
	"testing"
)

func TestExecute_InputSchemaClosed(t *testing.T) {
	for _, tool := range twoToolDefinitions() {
		if tool.Name != "execute" {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
			t.Fatal(err)
		}
		if schema["additionalProperties"] != false {
			t.Fatal("execute must advertise the runtime's closed argument object")
		}
		return
	}
	t.Fatal("missing execute tool")
}

func TestExecute_MultipartDryRunRequiresNonemptyStringBinding(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	s.allowLocalUpload = true
	for _, value := range []any{nil, "", "   ", 12, true, map[string]any{}, []any{}} {
		res := callTool(t, s, "execute", map[string]any{"operation_id": "file.upload", "dry_run": true,
			"arguments": map[string]any{"file_path": value}})
		if !res.IsError || toolText(t, res)["status"] != "validation_error" {
			t.Errorf("invalid file binding %v must fail: %s", value, res.Content[0].Text)
		}
	}
	res := callTool(t, s, "execute", map[string]any{"operation_id": "file.upload", "dry_run": true,
		"arguments": map[string]any{"file_path": "/missing-but-valid.bin"}})
	if res.IsError {
		t.Fatalf("valid binding must plan without a file read: %s", res.Content[0].Text)
	}
}

func TestGetSkill_DescribeDepthEnum(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	for _, depth := range []string{"sumary", "FULL", "invalid"} {
		res := callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": "message.send", "depth": depth})
		if !res.IsError || toolText(t, res)["status"] != "validation_error" {
			t.Errorf("invalid depth %q must fail: %s", depth, res.Content[0].Text)
		}
	}
	for _, depth := range []string{"", "summary", "full"} {
		res := callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": "message.send", "depth": depth})
		if res.IsError {
			t.Fatalf("valid depth %q failed: %s", depth, res.Content[0].Text)
		}
	}
}
