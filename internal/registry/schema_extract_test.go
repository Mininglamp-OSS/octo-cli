package registry

import (
	"testing"
)

// ---------------------------------------------------------------------------
// extractJSONSchema — coverage for fallback paths
// ---------------------------------------------------------------------------

func TestExtractJSONSchema_PrefersApplicationJSON(t *testing.T) {
	body := map[string]any{
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": map[string]any{"type": "object"},
			},
			"text/plain": map[string]any{
				"schema": map[string]any{"type": "string"},
			},
		},
	}
	s := extractJSONSchema(body)
	if s == nil {
		t.Fatal("expected non-nil schema")
	}
	if s["type"] != "object" {
		t.Errorf("expected application/json schema (type=object), got %v", s["type"])
	}
}

func TestExtractJSONSchema_FallbackToFirstEntry(t *testing.T) {
	// No "application/json" key — should fall back to whatever is available.
	body := map[string]any{
		"content": map[string]any{
			"application/xml": map[string]any{
				"schema": map[string]any{"type": "string", "format": "xml"},
			},
		},
	}
	s := extractJSONSchema(body)
	if s == nil {
		t.Fatal("expected non-nil schema from fallback path")
	}
	if s["type"] != "string" {
		t.Errorf("expected fallback schema type=string, got %v", s["type"])
	}
}

func TestExtractJSONSchema_NoContent(t *testing.T) {
	// body with no "content" key at all.
	body := map[string]any{"description": "empty body"}
	if s := extractJSONSchema(body); s != nil {
		t.Errorf("expected nil for body with no content, got %v", s)
	}
}

func TestExtractJSONSchema_ContentNotMap(t *testing.T) {
	// "content" exists but is not a map.
	body := map[string]any{"content": "bad"}
	if s := extractJSONSchema(body); s != nil {
		t.Errorf("expected nil for non-map content, got %v", s)
	}
}

func TestExtractJSONSchema_MediaTypeNoSchema(t *testing.T) {
	// Media type entry exists but has no "schema" key.
	body := map[string]any{
		"content": map[string]any{
			"application/json": map[string]any{"example": "{}"},
		},
	}
	if s := extractJSONSchema(body); s != nil {
		t.Errorf("expected nil when media type lacks schema, got %v", s)
	}
}

func TestExtractJSONSchema_FallbackMediaTypeNoSchema(t *testing.T) {
	// No application/json, fallback entry exists but has no "schema".
	body := map[string]any{
		"content": map[string]any{
			"text/plain": map[string]any{"example": "hello"},
		},
	}
	if s := extractJSONSchema(body); s != nil {
		t.Errorf("expected nil when fallback media type lacks schema, got %v", s)
	}
}

func TestExtractJSONSchema_FallbackMediaTypeNotMap(t *testing.T) {
	// No application/json, fallback value is not a map.
	body := map[string]any{
		"content": map[string]any{
			"text/plain": "not-a-map",
		},
	}
	if s := extractJSONSchema(body); s != nil {
		t.Errorf("expected nil when fallback media type is not a map, got %v", s)
	}
}

// ---------------------------------------------------------------------------
// firstSuccessSchema — coverage for fallback paths
// ---------------------------------------------------------------------------

func TestFirstSuccessSchema_Prefers200(t *testing.T) {
	doc := map[string]any{}
	resps := map[string]any{
		"200": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "description": "ok"},
				},
			},
		},
		"201": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "description": "created"},
				},
			},
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s == nil {
		t.Fatal("expected non-nil schema")
	}
	if s.Description != "ok" {
		t.Errorf("expected 200 schema (desc=ok), got %q", s.Description)
	}
}

func TestFirstSuccessSchema_FallsBackTo201(t *testing.T) {
	doc := map[string]any{}
	resps := map[string]any{
		"201": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "description": "created"},
				},
			},
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s == nil {
		t.Fatal("expected non-nil schema")
	}
	if s.Description != "created" {
		t.Errorf("expected 201 schema (desc=created), got %q", s.Description)
	}
}

func TestFirstSuccessSchema_204NoContent(t *testing.T) {
	// 204 with no body → returns nil (no content, no schema).
	doc := map[string]any{}
	resps := map[string]any{
		"204": map[string]any{
			"description": "No Content",
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s != nil {
		t.Errorf("expected nil for 204 with no content, got %+v", s)
	}
}

func TestFirstSuccessSchema_FallbackTo2xx(t *testing.T) {
	// No 200/201/204 — should pick an arbitrary 2xx response.
	doc := map[string]any{}
	resps := map[string]any{
		"400": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "description": "error"},
				},
			},
		},
		"202": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object", "description": "accepted"},
				},
			},
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s == nil {
		t.Fatal("expected non-nil schema from 2xx fallback")
	}
	if s.Description != "accepted" {
		t.Errorf("expected 202 schema (desc=accepted), got %q", s.Description)
	}
}

func TestFirstSuccessSchema_2xxNoSchema(t *testing.T) {
	// 2xx response present but has no JSON schema.
	doc := map[string]any{}
	resps := map[string]any{
		"202": map[string]any{
			"description": "Accepted",
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s != nil {
		t.Errorf("expected nil for 2xx with no schema, got %+v", s)
	}
}

func TestFirstSuccessSchema_2xxNotMap(t *testing.T) {
	// 2xx response value is not a map — should be skipped gracefully.
	doc := map[string]any{}
	resps := map[string]any{
		"202": "not-a-map",
	}
	s := firstSuccessSchema(doc, resps)
	if s != nil {
		t.Errorf("expected nil for 2xx non-map value, got %+v", s)
	}
}

func TestFirstSuccessSchema_NoSuccessResponses(t *testing.T) {
	doc := map[string]any{}
	resps := map[string]any{
		"400": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object"},
				},
			},
		},
		"500": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{"type": "object"},
				},
			},
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s != nil {
		t.Errorf("expected nil when no 2xx responses exist, got %+v", s)
	}
}

func TestFirstSuccessSchema_ResolvesRef(t *testing.T) {
	// Verify that $ref in a success response schema gets resolved.
	doc := map[string]any{
		"components": map[string]any{
			"schemas": map[string]any{
				"Widget": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":   map[string]any{"type": "string"},
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	resps := map[string]any{
		"200": map[string]any{
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": map[string]any{
						"$ref": "#/components/schemas/Widget",
					},
				},
			},
		},
	}
	s := firstSuccessSchema(doc, resps)
	if s == nil {
		t.Fatal("expected non-nil schema after ref resolution")
	}
	if s.Type != "object" {
		t.Errorf("expected resolved type=object, got %q", s.Type)
	}
	if _, ok := s.Properties["id"]; !ok {
		t.Error("expected resolved property 'id' after $ref resolution")
	}
}

func TestFirstSuccessSchema_EmptyResponses(t *testing.T) {
	doc := map[string]any{}
	resps := map[string]any{}
	s := firstSuccessSchema(doc, resps)
	if s != nil {
		t.Errorf("expected nil for empty responses, got %+v", s)
	}
}
