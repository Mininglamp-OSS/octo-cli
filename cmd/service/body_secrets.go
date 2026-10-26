package service

import (
	"encoding/json"
	"fmt"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// CollectBodySecrets returns the non-empty string values marked x-octo-secret
// in a decoded request body. Secret string-map properties collect each map
// value; all other secret properties must remain scalar strings.
func CollectBodySecrets(schema *registry.SchemaInfo, value any) ([]string, error) {
	if !schemaHasSecret(schema) {
		return nil, nil
	}
	return bodySecretValues(schema, value, "")
}

func schemaHasSecret(schema *registry.SchemaInfo) bool {
	if schema == nil {
		return false
	}
	for name := range schema.Properties {
		prop := schema.Properties[name]
		if prop.Secret || schemaHasSecret(&prop) {
			return true
		}
	}
	return schemaHasSecret(schema.Items)
}

func bodySecretValues(schema *registry.SchemaInfo, value any, at string) ([]string, error) {
	switch v := value.(type) {
	case map[string]any:
		return objectSecretValues(schema, v, at)
	case []any:
		return arraySecretValues(schema, v, at)
	}
	return nil, nil
}

func objectSecretValues(schema *registry.SchemaInfo, obj map[string]any, at string) ([]string, error) {
	var out []string
	for name := range schema.Properties {
		prop := schema.Properties[name]
		child, present := obj[name]
		if !present {
			continue
		}
		where := joinPropertyPath(at, name)
		if prop.Secret {
			if prop.Type == "object" {
				found, err := secretStringMapValues(where, child)
				if err != nil {
					return nil, err
				}
				out = append(out, found...)
			} else {
				str, err := secretStringAt(where, child)
				if err != nil {
					return nil, err
				}
				if str != "" {
					out = append(out, str)
				}
			}
			continue
		}
		if prop.Type == "object" || prop.Items != nil {
			nested, err := bodySecretValues(&prop, child, where)
			if err != nil {
				return nil, err
			}
			out = append(out, nested...)
		}
	}
	return out, nil
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
