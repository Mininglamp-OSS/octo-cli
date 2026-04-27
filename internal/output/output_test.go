package output

import (
	"encoding/json"
	"testing"
)

func TestFormatValue(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{nil, "-"},
		{"hello", "hello"},
		{float64(42), "42"},
		{true, "yes"},
		{false, "no"},
		{"a very long string that exceeds thirty six characters definitely", "a very long string that exceeds t..."},
	}

	for _, tt := range tests {
		got := formatValue(tt.input)
		if got != tt.want {
			t.Errorf("formatValue(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCollectHeaders(t *testing.T) {
	item := map[string]any{
		"id":     "123",
		"title":  "test",
		"status": "open",
		"extra":  "ignored",
	}
	headers := collectHeaders(item)
	if len(headers) != 3 {
		t.Errorf("got %d headers, want 3", len(headers))
	}
}

func TestPrintJSON_ValidData(t *testing.T) {
	// Just verify it doesn't panic
	data := json.RawMessage(`{"id":"123","title":"test"}`)
	printJSON(data)
}
