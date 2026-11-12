package permission

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

//go:embed contracts/permission-catalog-package-v2.schema.json
var catalogPackageSchema []byte

// CatalogJSONWork counts values, including containers, independently of keys.
type CatalogJSONWork struct{ Nodes, Depth int }

func catalogJSONValue(raw []byte, metadata bool) (any, CatalogJSONWork, error) {
	if !metadata {
		work, err := catalogRawJSONWork(raw, 8388608)
		return nil, work, err
	}
	limit := 8388608
	if metadata {
		limit = 262144
	}
	if len(raw) > limit || !utf8.Valid(raw) || !pairedEscapes(raw) {
		return nil, CatalogJSONWork{}, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := valueLimited(d, 0, 32)
	if err != nil {
		return nil, CatalogJSONWork{}, err
	}
	work := catalogJSONWork(v)
	if _, err = d.Token(); err != io.EOF || work.Depth > 32 || work.Nodes > 1048576 {
		return nil, work, ErrInvalid
	}
	if metadata && !bytes.Equal(raw, canonical(v)) {
		return nil, work, ErrInvalid
	}
	return v, work, nil
}

func catalogJSONWork(v any) CatalogJSONWork {
	w := CatalogJSONWork{Nodes: 1, Depth: 1}
	visit := func(child any) {
		c := catalogJSONWork(child)
		w.Nodes += c.Nodes
		if c.Depth+1 > w.Depth {
			w.Depth = c.Depth + 1
		}
	}
	switch x := v.(type) {
	case map[string]any:
		for _, c := range x {
			visit(c)
		}
	case []any:
		for _, c := range x {
			visit(c)
		}
	}
	return w
}

// ValidateCatalogDefinition validates the separately versioned closed grammar.
// Raw roots use their declared renderer/media domain, never this metadata schema.
func ValidateCatalogDefinition(name string, raw []byte) error {
	v, _, err := catalogJSONValue(raw, true)
	if err != nil {
		return err
	}
	var schema map[string]any
	d := json.NewDecoder(bytes.NewReader(catalogPackageSchema))
	d.UseNumber()
	if d.Decode(&schema) != nil {
		return ErrInvalid
	}
	defs := object(schema["$defs"])
	if !catalogSchemaMatches(defs[name], v, defs) {
		return fmt.Errorf("catalog %s: %w", name, ErrInvalid)
	}
	return nil
}

// This bounded structural evaluator implements only the keywords present in
// the immutable embedded schema. Reference graph/source semantics are separate.
func catalogSchemaMatches(schema, v any, defs map[string]any) bool { //nolint:gocyclo // closed schema keywords retain exact scalar, inherited-null and union semantics
	s := object(schema)
	if s == nil {
		return false
	}
	if ref, ok := s["$ref"].(string); ok {
		if !strings.HasPrefix(ref, "#/$defs/") {
			return false
		}
		return catalogSchemaMatches(defs[strings.TrimPrefix(ref, "#/$defs/")], v, defs)
	}
	if expected, ok := s["const"]; ok && !bytes.Equal(canonical(expected), canonical(v)) {
		return false
	}
	if options, ok := s["enum"].([]any); ok {
		found := false
		for _, option := range options {
			if bytes.Equal(canonical(option), canonical(v)) {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	for _, key := range []string{"allOf", "anyOf", "oneOf"} {
		if arms, ok := s[key].([]any); ok {
			n := 0
			for _, arm := range arms {
				if catalogSchemaMatches(arm, v, defs) {
					n++
				}
			}
			if key == "allOf" && n != len(arms) || key == "anyOf" && n == 0 || key == "oneOf" && n != 1 {
				return false
			}
		}
	}
	if condition, ok := s["if"]; ok && catalogSchemaMatches(condition, v, defs) {
		if then, ok := s["then"]; ok && !catalogSchemaMatches(then, v, defs) {
			return false
		}
	}
	if neg, ok := s["not"]; ok && catalogSchemaMatches(neg, v, defs) {
		return false
	}
	if typ, ok := s["type"].(string); ok {
		valid := false
		switch typ {
		case "object":
			_, valid = v.(map[string]any)
		case "array":
			_, valid = v.([]any)
		case "string":
			_, valid = v.(string)
		case "boolean":
			_, valid = v.(bool)
		case "null":
			valid = v == nil
		case "integer":
			n, ok := v.(json.Number)
			valid = ok && catalogInteger(string(n))
		default:
			return false
		}
		if !valid {
			return false
		}
	}
	if m, ok := v.(map[string]any); ok {
		props := object(s["properties"])
		if required, ok := s["required"].([]any); ok {
			for _, key := range required {
				if _, exists := m[text(key)]; !exists {
					return false
				}
			}
		}
		for key, value := range m {
			if sub, exists := props[key]; exists {
				if !catalogSchemaMatches(sub, value, defs) {
					return false
				}
			} else if s["additionalProperties"] == false {
				return false
			}
		}
	}
	if a, ok := v.([]any); ok {
		if !catalogRange(len(a), s, "minItems", "maxItems") {
			return false
		}
		seen := map[string]bool{}
		for _, item := range a {
			if sub, ok := s["items"]; ok && !catalogSchemaMatches(sub, item, defs) {
				return false
			}
			if s["uniqueItems"] == true {
				key := string(canonical(item))
				if seen[key] {
					return false
				}
				seen[key] = true
			}
		}
	}
	if str, ok := v.(string); ok {
		if !catalogRange(utf8.RuneCountInString(str), s, "minLength", "maxLength") {
			return false
		}
		if pattern, ok := s["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err != nil || !re.MatchString(str) {
				return false
			}
		}
	}
	if num, ok := v.(json.Number); ok {
		n, err := strconv.ParseInt(string(num), 10, 64)
		if err != nil {
			return false
		}
		if !catalogRange64(n, s, "minimum", "maximum") {
			return false
		}
	}
	return true
}

func catalogInteger(s string) bool {
	if s == "0" {
		return true
	}
	if s == "" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
		if s == "0" {
			return false
		}
	}
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
func catalogRange(n int, m map[string]any, lower, upper string) bool {
	return catalogRange64(int64(n), m, lower, upper)
}
func catalogRange64(n int64, m map[string]any, lower, upper string) bool {
	for _, key := range []string{lower, upper} {
		if v, ok := m[key].(json.Number); ok {
			limit, err := strconv.ParseInt(string(v), 10, 64)
			if err != nil || key == lower && n < limit || key == upper && n > limit {
				return false
			}
		}
	}
	return true
}
