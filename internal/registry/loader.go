// Package registry loads the embedded OpenAPI 3.x specs for each Octo service
// domain (matter, message, group, thread, file, bot, event). Consumers read
// operation metadata — parameters, request body shape, response shape, risk,
// pagination — to drive flag generation, request building, and `octo schema`.
//
// Specs are embedded at build time; no filesystem access at runtime.
package registry

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed specs/*.json
var specsFS embed.FS

// Registry holds the parsed OpenAPI documents keyed by service name.
// Values are retained as map[string]any rather than strongly-typed structs so
// the CLI tolerates additive spec changes and unknown x-octo-* extensions
// without code churn (architecture §5.3–5.5).
type Registry struct {
	specs map[string]map[string]any
}

// New loads and parses every embedded spec. It is not called at init time — the
// Factory invokes it lazily so tests can construct a Registry directly.
func New() (*Registry, error) {
	r := &Registry{specs: map[string]map[string]any{}}

	entries, err := fs.ReadDir(specsFS, "specs")
	if err != nil {
		return nil, fmt.Errorf("registry: read embed fs: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := fs.ReadFile(specsFS, "specs/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("registry: read %s: %w", e.Name(), err)
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("registry: parse %s: %w", e.Name(), err)
		}
		service := serviceName(doc, strings.TrimSuffix(e.Name(), ".json"))
		if _, dup := r.specs[service]; dup {
			return nil, fmt.Errorf("registry: duplicate service %q (files %s vs existing)", service, e.Name())
		}
		r.specs[service] = doc
	}
	return r, nil
}

// MustNew is like New but panics on error — useful for package-level wiring
// where a malformed embedded spec is a build-time bug, not a runtime condition.
func MustNew() *Registry {
	r, err := New()
	if err != nil {
		panic(err)
	}
	return r
}

// ListServices returns the sorted set of service names.
func (r *Registry) ListServices() []string {
	out := make([]string, 0, len(r.specs))
	for s := range r.specs {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// GetSpec returns the raw parsed document for a service, or nil if unknown.
func (r *Registry) GetSpec(service string) map[string]any {
	return r.specs[service]
}

// OperationInfo is the summary view of an operation — what ListOperations
// and ListAllOperations return. It carries the identifiers and metadata
// needed to route to and describe the operation without loading its full
// parameter / schema detail. Use GetOperation for the expanded view.
type OperationInfo struct {
	ID      string `json:"id"`
	Service string `json:"service"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
	Risk    string `json:"risk,omitempty"`
}

// ParamInfo describes a single parameter on an operation (one entry in the
// OpenAPI `parameters` array). Covers path, query, and header parameters;
// the `In` field records which. Type/Default/Enum come from the nested
// schema when present.
type ParamInfo struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
}

// SchemaInfo is a trimmed projection of an OpenAPI schema — just enough for
// the schema command to describe a request/response body to an Agent. One
// level of `$ref` into `components.schemas` is resolved eagerly; deeper refs
// are surfaced as an unresolved `$ref` pointer so the output still contains
// a useful reference.
type SchemaInfo struct {
	Type        string                `json:"type,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Properties  map[string]SchemaInfo `json:"properties,omitempty"`
	Items       *SchemaInfo           `json:"items,omitempty"`
	Enum        []any                 `json:"enum,omitempty"`
	Format      string                `json:"format,omitempty"`
	Description string                `json:"description,omitempty"`
	MaxLength   int                   `json:"max_length,omitempty"`
	MaxItems    int                   `json:"max_items,omitempty"`
	Ref         string                `json:"$ref,omitempty"`
}

// PaginationInfo captures the `x-octo-pagination` extension. It tells the
// service engine which query parameters carry the cursor / limit and which
// response fields carry the next cursor / has-more flag, so `--page-all`
// can walk pages without operation-specific code.
type PaginationInfo struct {
	CursorParam  string `json:"cursor_param,omitempty"`
	LimitParam   string `json:"limit_param,omitempty"`
	CursorField  string `json:"cursor_field,omitempty"`
	HasMoreField string `json:"has_more_field,omitempty"`
}

// OperationDetail is the expanded view of a single operation returned by
// GetOperation. It embeds OperationInfo for identity/summary and adds every
// piece of metadata the service engine needs to build a complete cobra
// command: parameters, request/response schemas, pagination, base-URL
// routing, space-header behaviour, and payload shape flags (multipart /
// binary response).
type OperationDetail struct {
	OperationInfo
	Parameters     []ParamInfo     `json:"parameters,omitempty"`
	RequestBody    *SchemaInfo     `json:"request_body,omitempty"`
	ResponseSchema *SchemaInfo     `json:"response_schema,omitempty"`
	Pagination     *PaginationInfo `json:"pagination,omitempty"`
	BaseURLEnv     string          `json:"base_url_env,omitempty"`
	SpaceHeader    bool            `json:"space_header,omitempty"`
	Multipart      bool            `json:"multipart,omitempty"`
	BinaryResponse bool            `json:"binary_response,omitempty"`
}

// ListOperations returns every operation for a service, sorted by operationId.
// A missing/unknown service yields an empty slice (not an error).
func (r *Registry) ListOperations(service string) []OperationInfo {
	doc := r.specs[service]
	if doc == nil {
		return nil
	}
	out := []OperationInfo{}
	walkOperations(doc, func(pathStr, method string, op map[string]any) {
		id, _ := op["operationId"].(string)
		if id == "" {
			return
		}
		out = append(out, OperationInfo{
			ID:      id,
			Service: service,
			Method:  strings.ToUpper(method),
			Path:    pathStr,
			Summary: stringOf(op["summary"]),
			Risk:    stringOf(op["x-octo-risk"]),
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListAllOperations returns operations across all services, sorted by
// service then operationId.
func (r *Registry) ListAllOperations() []OperationInfo {
	var all []OperationInfo
	for _, s := range r.ListServices() {
		all = append(all, r.ListOperations(s)...)
	}
	return all
}

// GetOperation finds an operation by its operationId. Returns ok=false when
// not found.
func (r *Registry) GetOperation(operationID string) (*OperationDetail, bool) {
	for service, doc := range r.specs {
		var found *OperationDetail
		walkOperations(doc, func(pathStr, method string, op map[string]any) {
			if found != nil {
				return
			}
			id, _ := op["operationId"].(string)
			if id != operationID {
				return
			}
			found = buildDetail(service, doc, pathStr, method, op)
		})
		if found != nil {
			return found, true
		}
	}
	return nil, false
}

// --- internals ---

var httpMethods = []string{"get", "put", "post", "delete", "patch", "options", "head"}

func walkOperations(doc map[string]any, fn func(path, method string, op map[string]any)) {
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return
	}
	// Sort paths for deterministic iteration order.
	keys := make([]string, 0, len(paths))
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, p := range keys {
		item, ok := paths[p].(map[string]any)
		if !ok {
			continue
		}
		for _, m := range httpMethods {
			op, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			fn(p, m, op)
		}
	}
}

func buildDetail(service string, doc map[string]any, pathStr, method string, op map[string]any) *OperationDetail {
	d := &OperationDetail{
		OperationInfo: OperationInfo{
			ID:      stringOf(op["operationId"]),
			Service: service,
			Method:  strings.ToUpper(method),
			Path:    pathStr,
			Summary: stringOf(op["summary"]),
			Risk:    stringOf(op["x-octo-risk"]),
		},
		BaseURLEnv:  stringOf(doc["x-octo-base-url"]),
		SpaceHeader: boolOf(doc["x-octo-space-header"]),
	}

	d.Multipart = boolOf(op["x-octo-multipart"])
	d.BinaryResponse = boolOf(op["x-octo-binary-response"])

	if params, ok := op["parameters"].([]any); ok {
		for _, p := range params {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			pi := ParamInfo{
				Name:        stringOf(pm["name"]),
				In:          stringOf(pm["in"]),
				Required:    boolOf(pm["required"]),
				Description: stringOf(pm["description"]),
			}
			if sch, ok := pm["schema"].(map[string]any); ok {
				pi.Type = stringOf(sch["type"])
				pi.Default = sch["default"]
				if enum, ok := sch["enum"].([]any); ok {
					pi.Enum = enum
				}
			}
			d.Parameters = append(d.Parameters, pi)
		}
	}

	if body, ok := op["requestBody"].(map[string]any); ok {
		if s := extractJSONSchema(body); s != nil {
			resolved := resolveSchema(doc, s)
			d.RequestBody = &resolved
		}
	}

	if resps, ok := op["responses"].(map[string]any); ok {
		d.ResponseSchema = firstSuccessSchema(doc, resps)
	}

	if pag, ok := op["x-octo-pagination"].(map[string]any); ok {
		d.Pagination = &PaginationInfo{
			CursorParam:  stringOf(pag["cursorParam"]),
			LimitParam:   stringOf(pag["limitParam"]),
			CursorField:  stringOf(pag["cursorField"]),
			HasMoreField: stringOf(pag["hasMoreField"]),
		}
	}

	return d
}

func extractJSONSchema(body map[string]any) map[string]any {
	content, ok := body["content"].(map[string]any)
	if !ok {
		return nil
	}
	// Prefer application/json; fall back to first entry.
	if mt, ok := content["application/json"].(map[string]any); ok {
		if s, ok := mt["schema"].(map[string]any); ok {
			return s
		}
	}
	for _, v := range content {
		mt, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := mt["schema"].(map[string]any); ok {
			return s
		}
	}
	return nil
}

func firstSuccessSchema(doc, resps map[string]any) *SchemaInfo {
	// Prefer 200, then 201, then anything 2xx.
	order := []string{"200", "201", "204"}
	for _, code := range order {
		if r, ok := resps[code].(map[string]any); ok {
			if s := extractJSONSchema(r); s != nil {
				resolved := resolveSchema(doc, s)
				return &resolved
			}
			return nil
		}
	}
	for code, r := range resps {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		if rm, ok := r.(map[string]any); ok {
			if s := extractJSONSchema(rm); s != nil {
				resolved := resolveSchema(doc, s)
				return &resolved
			}
		}
	}
	return nil
}

// resolveSchema performs a shallow walk of a schema node, following one level
// of $ref into components.schemas. Deeper refs are left as `$ref` strings so
// the schema command can still surface a useful pointer to the consumer.
func resolveSchema(doc map[string]any, s map[string]any) SchemaInfo {
	return resolveSchemaWithDepth(doc, s, 0)
}

func resolveSchemaWithDepth(doc map[string]any, s map[string]any, depth int) SchemaInfo {
	if s == nil {
		return SchemaInfo{}
	}
	if ref, ok := s["$ref"].(string); ok && ref != "" {
		if depth < 3 {
			if resolved := followRef(doc, ref); resolved != nil {
				return resolveSchemaWithDepth(doc, resolved, depth+1)
			}
		}
		return SchemaInfo{Ref: ref}
	}

	info := SchemaInfo{
		Type:        stringOf(s["type"]),
		Format:      stringOf(s["format"]),
		Description: stringOf(s["description"]),
	}
	if req, ok := s["required"].([]any); ok {
		for _, x := range req {
			if name, ok := x.(string); ok {
				info.Required = append(info.Required, name)
			}
		}
	}
	if enum, ok := s["enum"].([]any); ok {
		info.Enum = enum
	}
	if ml, ok := s["maxLength"].(float64); ok {
		info.MaxLength = int(ml)
	}
	if mi, ok := s["maxItems"].(float64); ok {
		info.MaxItems = int(mi)
	}
	if props, ok := s["properties"].(map[string]any); ok {
		info.Properties = map[string]SchemaInfo{}
		for name, v := range props {
			if sub, ok := v.(map[string]any); ok {
				info.Properties[name] = resolveSchemaWithDepth(doc, sub, depth+1)
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		sub := resolveSchemaWithDepth(doc, items, depth+1)
		info.Items = &sub
	}
	return info
}

func followRef(doc map[string]any, ref string) map[string]any {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	name := strings.TrimPrefix(ref, prefix)
	comps, ok := doc["components"].(map[string]any)
	if !ok {
		return nil
	}
	schemas, ok := comps["schemas"].(map[string]any)
	if !ok {
		return nil
	}
	s, _ := schemas[name].(map[string]any)
	return s
}

func serviceName(doc map[string]any, fallback string) string {
	if s := stringOf(doc["x-octo-service"]); s != "" {
		return s
	}
	return fallback
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}
