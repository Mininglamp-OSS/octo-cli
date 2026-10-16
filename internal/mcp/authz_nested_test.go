package mcp

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// A new nested routing field or an opaque filter that advertises routing must
// be reviewed explicitly; the top-level force map cannot rewrite it.
func TestAuthzCoverage_NestedRoutingFieldsAreGuarded(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		detail, ok := reg.GetOperation(op.ID)
		if !ok || detail.RequestBody == nil {
			continue
		}
		walkSchemaRouting(detail.RequestBody, "", func(path string, schema *registry.SchemaInfo) {
			// The search filter is opaque in the registry, so the runtime
			// examines every nested key with the same isScopeField classifier.
			if strings.HasPrefix(op.ID, "message.search") && path == "filters" {
				return
			}
			name := path[strings.LastIndex(path, ".")+1:]
			if strings.Contains(path, ".") && isScopeField(name) {
				t.Errorf("%s declares nested routing field %s outside the force table", op.ID, path)
			}
			if schema.Type == "object" && len(schema.Properties) == 0 && len(overridableParams[op.ID]) > 0 {
				// These two opaque values are data, not routing controls: an
				// environment map and the message content payload. Search filters
				// are handled by validateOperationBinding above.
				if op.ID == "expert.environment.update" && path == "custom_env" ||
					op.ID == "message.send" && path == "payload" {
					return
				}
				t.Errorf("%s has an opaque object %s under a forced scope without a runtime guard", op.ID, path)
			}
		})
	}
}

func TestAuthzCoverage_NestedWalkerSeesFutureScope(t *testing.T) {
	schema := registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{
		"scope": {Type: "object", Properties: map[string]registry.SchemaInfo{
			"channel_id": {Type: "string"},
		}},
	}}
	found := false
	walkSchemaRouting(&schema, "", func(path string, _ *registry.SchemaInfo) {
		if path == "scope.channel_id" {
			found = true
		}
	})
	if !found {
		t.Fatal("nested schema walker missed channel_id")
	}
}

func walkSchemaRouting(schema *registry.SchemaInfo, prefix string, visit func(string, *registry.SchemaInfo)) {
	for name := range schema.Properties {
		child := schema.Properties[name]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		visit(path, &child)
		walkSchemaRouting(&child, path, visit)
	}
	if schema.Items != nil {
		walkSchemaRouting(schema.Items, prefix+"[]", visit)
	}
	for i := range schema.OneOf {
		walkSchemaRouting(&schema.OneOf[i], prefix, visit)
	}
	for i := range schema.AnyOf {
		walkSchemaRouting(&schema.AnyOf[i], prefix, visit)
	}
}
