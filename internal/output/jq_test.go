package output

import (
	"encoding/json"
	"testing"
)

func TestApplyJQ_Empty(t *testing.T) {
	v := map[string]any{"id": "x"}
	got, err := ApplyJQ(v, "")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestApplyJQ_FieldAccess(t *testing.T) {
	v := map[string]any{"data": map[string]any{"id": "t1"}}
	got, err := ApplyJQ(v, ".data.id")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 || got[0] != "t1" {
		t.Errorf("got %v", got)
	}
}

func TestApplyJQ_ArrayLength(t *testing.T) {
	v := map[string]any{"data": []any{1, 2, 3}}
	got, err := ApplyJQ(v, ".data | length")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	// jq returns int as int for length
	switch n := got[0].(type) {
	case int:
		if n != 3 {
			t.Errorf("got %d", n)
		}
	case float64:
		if n != 3 {
			t.Errorf("got %v", n)
		}
	default:
		t.Errorf("unexpected type %T", got[0])
	}
}

func TestApplyJQ_Map(t *testing.T) {
	v := map[string]any{
		"data": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		},
	}
	got, err := ApplyJQ(v, ".data[].id")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}

func TestApplyJQ_FromRawMessage(t *testing.T) {
	raw := json.RawMessage(`{"id":"x","n":7}`)
	got, err := ApplyJQ(raw, ".n")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestApplyJQ_ParseError(t *testing.T) {
	_, err := ApplyJQ(map[string]any{}, "....invalid...")
	if err == nil {
		t.Error("expected parse error")
	}
}
