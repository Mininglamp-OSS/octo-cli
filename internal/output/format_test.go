package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormat_JSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{"id": "t1", "title": "hi"}
	if err := Format(&buf, "json", data); err != nil {
		t.Fatalf("Format json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["id"] != "t1" {
		t.Errorf("id = %v", got["id"])
	}
}

func TestFormat_Table_EnvelopeWithArray(t *testing.T) {
	env := map[string]any{
		"ok": true,
		"data": []map[string]any{
			{"id": "t1", "title": "first", "status": "open"},
			{"id": "t2", "title": "second", "status": "done"},
		},
	}
	var buf bytes.Buffer
	if err := Format(&buf, "table", env); err != nil {
		t.Fatalf("Format table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "id") || !strings.Contains(out, "title") || !strings.Contains(out, "status") {
		t.Errorf("missing headers in table output:\n%s", out)
	}
	if !strings.Contains(out, "t1") || !strings.Contains(out, "t2") {
		t.Errorf("missing rows in table output:\n%s", out)
	}
}

func TestFormat_Table_EmptyArray(t *testing.T) {
	env := map[string]any{"ok": true, "data": []map[string]any{}}
	var buf bytes.Buffer
	if err := Format(&buf, "table", env); err != nil {
		t.Fatalf("Format table: %v", err)
	}
	if !strings.Contains(buf.String(), "no results") {
		t.Errorf("expected no-results message, got:\n%s", buf.String())
	}
}

func TestFormat_CSV(t *testing.T) {
	env := map[string]any{
		"ok": true,
		"data": []map[string]any{
			{"id": "t1", "title": "first"},
			{"id": "t2", "title": "second"},
		},
	}
	var buf bytes.Buffer
	if err := Format(&buf, "csv", env); err != nil {
		t.Fatalf("Format csv: %v", err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "id") || !strings.Contains(lines[0], "title") {
		t.Errorf("header missing: %q", lines[0])
	}
}

func TestFormat_NDJSON(t *testing.T) {
	env := map[string]any{
		"ok": true,
		"data": []map[string]any{
			{"id": "t1"},
			{"id": "t2"},
		},
	}
	var buf bytes.Buffer
	if err := Format(&buf, "ndjson", env); err != nil {
		t.Fatalf("Format ndjson: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 ndjson lines, got %d", len(lines))
	}
	var row1 map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &row1); err != nil {
		t.Fatalf("line 1 not valid JSON: %v", err)
	}
	if row1["id"] != "t1" {
		t.Errorf("line 1 id = %v", row1["id"])
	}
}

func TestFormat_UnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	err := Format(&buf, "xml", map[string]any{"id": "x"})
	if err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestFormat_TableOnScalarObject(t *testing.T) {
	// Scalar object (not an envelope with data array) should still render one row.
	data := map[string]any{"id": "t1", "title": "solo"}
	var buf bytes.Buffer
	if err := Format(&buf, "table", data); err != nil {
		t.Fatalf("Format table: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "t1") {
		t.Errorf("missing value:\n%s", out)
	}
}

func TestTableHeaders_PriorityOrder(t *testing.T) {
	rows := []map[string]any{
		{"status": "open", "id": "x", "extra": 1, "title": "y"},
	}
	h := tableHeaders(rows)
	// id, title, status are priority; extra is alphabetical at the end.
	if len(h) != 4 {
		t.Fatalf("got %d headers, want 4: %v", len(h), h)
	}
	if h[0] != "id" || h[1] != "title" || h[2] != "status" {
		t.Errorf("priority order wrong: %v", h)
	}
	if h[3] != "extra" {
		t.Errorf("trailing header = %q", h[3])
	}
}
