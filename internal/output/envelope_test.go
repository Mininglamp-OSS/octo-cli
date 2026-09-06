package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestWriteSuccess_ScalarData(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, json.RawMessage(`{"id":"t1","title":"hi"}`), EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["ok"] != true {
		t.Errorf("ok = %v, want true", got["ok"])
	}
	// identity is always an object with a consistent shape; here just {type}.
	if id, ok := got["identity"].(map[string]any); !ok || id["type"] != "bot" {
		t.Errorf("identity = %v, want {type:bot}", got["identity"])
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want object", got["data"])
	}
	if data["id"] != "t1" {
		t.Errorf("data.id = %v, want t1", data["id"])
	}
	if _, ok := got["_pagination"]; ok {
		t.Errorf("_pagination should not be present on non-paginated response")
	}
}

func TestWriteSuccess_FlattensPagination(t *testing.T) {
	raw := json.RawMessage(`{"data":[{"id":"a"},{"id":"b"}],"pagination":{"has_more":true,"next_cursor":"cur-1"}}`)
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	items, ok := got["data"].([]any)
	if !ok {
		t.Fatalf("data is %T, want array", got["data"])
	}
	if len(items) != 2 {
		t.Errorf("data len = %d, want 2", len(items))
	}
	pag, ok := got["_pagination"].(map[string]any)
	if !ok {
		t.Fatalf("_pagination is %T, want object", got["_pagination"])
	}
	if pag["next_cursor"] != "cur-1" {
		t.Errorf("next_cursor = %v", pag["next_cursor"])
	}
	if pag["has_more"] != true {
		t.Errorf("has_more = %v", pag["has_more"])
	}
}

func TestWriteSuccess_PreservesResourceEnvelopeByDefault(t *testing.T) {
	raw := json.RawMessage(`{"data":{"id":"marketplace-1"}}`)
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want original envelope", got["data"])
	}
	if _, nested := data["data"]; !nested {
		t.Fatalf("non-Loop resource envelope changed: %#v", data)
	}
}

func TestWriteSuccess_FlattensResourceEnvelope(t *testing.T) {
	raw := json.RawMessage(`{"data":{"project_id":"project-1","title":"test"}}`)
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, EnvelopeMeta{UnwrapResource: true}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := got["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is %T, want object", got["data"])
	}
	if data["project_id"] != "project-1" {
		t.Fatalf("data = %#v", data)
	}
	if _, nested := data["data"]; nested {
		t.Fatalf("resource envelope was not flattened: %#v", data)
	}
}

func TestWriteSuccess_PreservesBusinessObjectContainingData(t *testing.T) {
	raw := json.RawMessage(`{"data":{"id":"item-1"},"status":"ready"}`)
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data, ok := got["data"].(map[string]any)
	if !ok || data["status"] != "ready" {
		t.Fatalf("business payload should remain intact: %#v", got["data"])
	}
}

func TestWriteSuccess_ArrayPassedThrough(t *testing.T) {
	// Raw arrays (no pagination wrapper) are placed under data as-is.
	raw := json.RawMessage(`[{"id":"a"}]`)
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	items, ok := got["data"].([]any)
	if !ok {
		t.Fatalf("data should be array, got %T", got["data"])
	}
	if len(items) != 1 {
		t.Errorf("len = %d", len(items))
	}
	if _, ok := got["_pagination"]; ok {
		t.Errorf("_pagination should not be added for raw array input")
	}
}

func TestWriteSuccess_EmptyRaw(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, nil, EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	if !strings.Contains(buf.String(), `"data": null`) {
		t.Errorf("empty raw should produce null data, got: %s", buf.String())
	}
}

func TestWriteSuccess_AttachesMeta(t *testing.T) {
	raw := json.RawMessage(`{"id":"a"}`)
	meta := EnvelopeMeta{
		RateLimit: json.RawMessage(`{"remaining":47,"limit":100}`),
		Notice:    json.RawMessage(`{"update":{"latest":"1.0.0"}}`),
	}
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, raw, meta); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	if _, ok := got["_rate_limit"]; !ok {
		t.Errorf("_rate_limit missing: %s", buf.String())
	}
	if _, ok := got["_notice"]; !ok {
		t.Errorf("_notice missing: %s", buf.String())
	}
}

func TestWriteError_ExitError(t *testing.T) {
	ee := &ExitError{
		Type:    "validation",
		Code:    "VALIDATION_ERROR",
		Message: "title is required",
		Hint:    "pass --title",
	}
	var buf bytes.Buffer
	if err := WriteError(&buf, ee); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["ok"] != false {
		t.Errorf("ok = %v, want false", got["ok"])
	}
	errObj := got["error"].(map[string]any)
	if errObj["type"] != "validation" {
		t.Errorf("type = %v", errObj["type"])
	}
	if errObj["code"] != "VALIDATION_ERROR" {
		t.Errorf("code = %v", errObj["code"])
	}
	if errObj["hint"] != "pass --title" {
		t.Errorf("hint = %v", errObj["hint"])
	}
}

func TestWriteError_DocsTooManyDataValidations(t *testing.T) {
	ee := ParseBackendError(http.StatusRequestEntityTooLarge, []byte(`{"error":"too_many_data_validations"}`))
	var buf bytes.Buffer
	if err := WriteError(&buf, ee); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Type    string          `json:"type"`
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Hint    string          `json:"hint"`
			Detail  json.RawMessage `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK || got.Error.Type != "validation" || got.Error.Code != "too_many_data_validations" {
		t.Fatalf("envelope = %s", buf.String())
	}
	if got.Error.Message != "too_many_data_validations" {
		t.Fatalf("message = %q", got.Error.Message)
	}
	const wantHint = "send fewer rules per sheet; each sheet array replaces the whole rule set, so splitting one sheet across retries drops rules"
	if got.Error.Hint != wantHint {
		t.Fatalf("hint = %q, want %q", got.Error.Hint, wantHint)
	}
	var detail map[string]any
	if err := json.Unmarshal(got.Error.Detail, &detail); err != nil {
		t.Fatalf("detail is not valid JSON: %v", err)
	}
	if detail["error"] != "too_many_data_validations" {
		t.Fatalf("detail = %s", got.Error.Detail)
	}
}

func TestWriteError_DocsTooManySheetResources(t *testing.T) {
	ee := ParseBackendError(http.StatusRequestEntityTooLarge, []byte(`{"error":"too_many_sheet_resources"}`))
	var buf bytes.Buffer
	if err := WriteError(&buf, ee); err != nil {
		t.Fatalf("WriteError: %v", err)
	}

	var got struct {
		OK    bool `json:"ok"`
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OK || got.Error.Type != "validation" || got.Error.Code != "too_many_sheet_resources" {
		t.Fatalf("envelope = %s", buf.String())
	}
	const wantHint = "send fewer freeze or filter sheet entries per edit; split only those top-level sheet keys across batches"
	if got.Error.Hint != wantHint {
		t.Fatalf("hint = %q, want %q", got.Error.Hint, wantHint)
	}
}

func TestWriteError_GenericError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteError(&buf, errors.New("something went wrong")); err != nil {
		t.Fatalf("WriteError: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(buf.Bytes(), &got)
	errObj := got["error"].(map[string]any)
	if errObj["type"] != "internal" {
		t.Errorf("type = %v, want internal", errObj["type"])
	}
	if errObj["message"] != "something went wrong" {
		t.Errorf("message = %v", errObj["message"])
	}
}

func TestSplitPagination_Detection(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"valid paginated", `{"data":[],"pagination":{}}`, true},
		{"valid with items", `{"data":[{"id":"x"}],"pagination":{"has_more":false}}`, true},
		{"data not array", `{"data":{"x":1},"pagination":{}}`, false},
		{"missing pagination", `{"data":[]}`, false},
		{"missing data", `{"pagination":{}}`, false},
		{"scalar object", `{"id":"x"}`, false},
		{"array root", `[{"x":1}]`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := splitPagination(json.RawMessage(tt.raw))
			if ok != tt.want {
				t.Errorf("splitPagination(%q) ok=%v, want %v", tt.raw, ok, tt.want)
			}
		})
	}
}

func TestSplitResourceData_Detection(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"resource object", `{"data":{"id":"x"}}`, true},
		{"extra top-level field", `{"data":{"id":"x"},"status":"ok"}`, false},
		{"array data", `{"data":[]}`, false},
		{"scalar data", `{"data":"x"}`, false},
		{"raw resource", `{"id":"x"}`, false},
		{"invalid", `{`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := splitResourceData(json.RawMessage(tt.raw))
			if ok != tt.want {
				t.Errorf("splitResourceData(%q) ok=%v, want %v", tt.raw, ok, tt.want)
			}
		})
	}
}

func TestWriteError_NilError(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteError(&buf, nil); err != nil {
		t.Fatalf("WriteError(nil): %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if env["ok"] != false {
		t.Errorf("ok = %v", env["ok"])
	}
	errObj, _ := env["error"].(map[string]any)
	if errObj["type"] != "internal" {
		t.Errorf("nil error should produce internal type, got %v", errObj["type"])
	}
}

func TestWriteSuccess_NilMeta(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSuccess(&buf, json.RawMessage(`{"id":"x"}`), EnvelopeMeta{}); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	s := buf.String()
	if strings.Contains(s, "_rate_limit") || strings.Contains(s, "_notice") {
		t.Errorf("empty meta should not emit meta fields: %s", s)
	}
}

func TestIsJSONHelpers_Whitespace(t *testing.T) {
	if !isJSONArray(json.RawMessage("  \n\t[1,2]")) {
		t.Error("whitespace-prefixed array not detected")
	}
	if !isJSONObject(json.RawMessage("  \n{\"a\":1}")) {
		t.Error("whitespace-prefixed object not detected")
	}
	if isJSONArray(json.RawMessage(`{"a":1}`)) {
		t.Error("object mistaken for array")
	}
	if isJSONObject(json.RawMessage(`[1]`)) {
		t.Error("array mistaken for object")
	}
	if isJSONArray(json.RawMessage(`   `)) {
		t.Error("whitespace-only should not classify")
	}
}
