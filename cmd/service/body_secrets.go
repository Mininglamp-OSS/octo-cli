package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/foldkey"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// CollectBodySecrets returns non-empty strings marked x-octo-secret or writeOnly
// in a decoded request body. Secret string-map properties collect each map
// value; all other secret properties must remain scalar strings.
func CollectBodySecrets(schema *registry.SchemaInfo, value any) ([]string, error) {
	if !schemaHasSecret(schema) {
		return nil, nil
	}
	return bodySecretValues(schema, value, "")
}

// CollectRequestSecrets returns the distinct declared secret values in body:
// those CollectBodySecrets finds in the resolved RequestBody tree plus those at
// d.RequestSecretPaths, which also reach below that tree.
func CollectRequestSecrets(d *registry.OperationDetail, body any) ([]string, error) {
	found, err := CollectBodySecrets(d.RequestBody, body)
	if err != nil {
		return nil, err
	}
	nested, err := CollectBodySecretPaths(d.RequestSecretPaths, body)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(found))
	out := make([]string, 0, len(found)+len(nested))
	for _, v := range append(found, nested...) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out, nil
}

// CollectBodySecretPaths returns the non-empty string values found at the
// request-body paths of OperationDetail.RequestSecretPaths. A path names a
// string, or a string map whose values are all collected; any other value at
// a secret path is refused so it can never print unmasked.
func CollectBodySecretPaths(paths []string, value any) ([]string, error) {
	var out []string
	for _, path := range paths {
		found, err := secretValuesAtPath(value, splitSecretPath(path), "")
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

// splitSecretPath splits "a.b[].*" into ["a", "b", "[]", "*"].
func splitSecretPath(path string) []string {
	var steps []string
	for _, part := range strings.Split(path, ".") {
		name := strings.TrimRight(part, "[]")
		if name != "" {
			steps = append(steps, name)
		}
		for i := len(name); i+1 < len(part); i += 2 {
			steps = append(steps, "[]")
		}
	}
	return steps
}

func secretValuesAtPath(value any, steps []string, at string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	if len(steps) == 0 {
		switch v := value.(type) {
		case string:
			if v == "" {
				return nil, nil
			}
			return []string{v}, nil
		case map[string]any:
			return secretStringMapValues(at, v)
		}
		return nil, fmt.Errorf("%s is declared secret and must be a JSON string, not %s", at, jsonKindOf(value))
	}
	var out []string
	switch step := steps[0]; step {
	case "[]":
		items, ok := value.([]any)
		if !ok {
			return nil, nil
		}
		for i, item := range items {
			found, err := secretValuesAtPath(item, steps[1:], fmt.Sprintf("%s[%d]", at, i))
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	case "*":
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, nil
		}
		for key, child := range obj {
			found, err := secretValuesAtPath(child, steps[1:], joinPropertyPath(at, key))
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	default:
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, nil
		}
		// A backend may bind this property under any case-fold spelling, so
		// every key equal to step under strings.EqualFold is followed, not only
		// the exact spelling.
		want := foldkey.Key(step)
		for key, child := range obj {
			if key != step && foldkey.Key(key) != want {
				continue
			}
			found, err := secretValuesAtPath(child, steps[1:], joinPropertyPath(at, key))
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

func schemaHasSecret(schema *registry.SchemaInfo) bool {
	if schema == nil {
		return false
	}
	if registry.RequestSensitive(schema.Secret, schema.WriteOnly) {
		return true
	}
	for name := range schema.Properties {
		prop := schema.Properties[name]
		if registry.RequestSensitive(prop.Secret, prop.WriteOnly) || schemaHasSecret(&prop) {
			return true
		}
	}
	return schemaHasSecret(schema.Items)
}

func bodySecretValues(schema *registry.SchemaInfo, value any, at string) ([]string, error) {
	if schema == nil {
		return nil, nil
	}
	if registry.RequestSensitive(schema.Secret, schema.WriteOnly) {
		return propertySecretValues(*schema, value, at)
	}
	switch v := value.(type) {
	case map[string]any:
		return objectSecretValues(schema, v, at)
	case []any:
		return arraySecretValues(schema, v, at)
	}
	return nil, nil
}

// objectSecretValues walks the properties of obj that the schema declares. A
// key spelled exactly like a declared property matches only that property;
// any other key matches every declared property equal to it under
// strings.EqualFold, because a backend may bind it case-insensitively and a
// secret under a case-fold variant must be masked like the canonical one.
func objectSecretValues(schema *registry.SchemaInfo, obj map[string]any, at string) ([]string, error) {
	if len(schema.Properties) == 0 {
		return nil, nil
	}
	byFold := make(map[string][]string, len(schema.Properties))
	for name := range schema.Properties {
		fk := foldkey.Key(name)
		byFold[fk] = append(byFold[fk], name)
	}
	var out []string
	for key, child := range obj {
		names := byFold[foldkey.Key(key)]
		if _, exact := schema.Properties[key]; exact {
			names = []string{key}
		}
		for _, name := range names {
			found, err := propertySecretValues(schema.Properties[name], child, joinPropertyPath(at, key))
			if err != nil {
				return nil, err
			}
			out = append(out, found...)
		}
	}
	return out, nil
}

func propertySecretValues(prop registry.SchemaInfo, child any, where string) ([]string, error) { //nolint:gocritic // schema properties are read-only values
	if registry.RequestSensitive(prop.Secret, prop.WriteOnly) {
		if prop.Type == "object" {
			return secretStringMapValues(where, child)
		}
		str, err := secretStringAt(where, child)
		if err != nil || str == "" {
			return nil, err
		}
		return []string{str}, nil
	}
	if prop.Type == "object" || prop.Items != nil {
		return bodySecretValues(&prop, child, where)
	}
	return nil, nil
}

func arraySecretValues(schema *registry.SchemaInfo, items []any, at string) ([]string, error) {
	if schema.Items == nil {
		return nil, nil
	}
	var out []string
	for i, item := range items {
		nested, err := bodySecretValues(schema.Items, item, fmt.Sprintf("%s[%d]", at, i))
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}

func secretStringMapValues(where string, child any) ([]string, error) {
	if child == nil {
		return nil, nil
	}
	obj, ok := child.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s is declared secret and must be a JSON object, not %s",
			where, jsonKindOf(child))
	}
	var out []string
	for key, childValue := range obj {
		value, err := secretStringAt(joinPropertyPath(where, key), childValue)
		if err != nil {
			return nil, err
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out, nil
}

func secretStringAt(where string, child any) (string, error) {
	if child == nil {
		return "", nil
	}
	str, ok := child.(string)
	if !ok {
		return "", fmt.Errorf("%s is declared secret and must be a JSON string, not %s",
			where, jsonKindOf(child))
	}
	return str, nil
}

func joinPropertyPath(at, name string) string {
	if at == "" {
		return name
	}
	return at + "." + name
}

func jsonKindOf(v any) string {
	switch v.(type) {
	case json.Number, int, int64, float64:
		return "a number"
	case bool:
		return "a boolean"
	case map[string]any:
		return "an object"
	case []any:
		return "an array"
	}
	return "a non-string value"
}
