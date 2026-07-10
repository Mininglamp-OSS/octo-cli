// Package registry loads the embedded OpenAPI 3.x specs for each Octo service
// domain (matter, message, group, thread, file, bot, event). Consumers read
// operation metadata — parameters, request body shape, response shape, risk,
// pagination — to drive flag generation, request building, and `octo-cli schema`.
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

// ServiceDisabled reports the x-octo-disabled extension for a service.
// Disabled services stay loaded — so schema lookups and the metadata-driven
// engine still see them — but are withheld from the command tree and the
// global discovery listing. Flip the flag in the spec to re-enable.
//
// The flag is truthy as either a JSON boolean (`true`) or the string
// `"true"`, matching the skill frontmatter convention so the two surfaces
// can't drift. ListServices / ListAllOperations are deliberately NOT filtered
// — use EnabledServices / EnabledOperations for a caller-facing view.
func (r *Registry) ServiceDisabled(service string) bool {
	return truthy(r.specs[service]["x-octo-disabled"])
}

// EnabledServices is ListServices minus any service whose spec sets
// x-octo-disabled. This is the single source of truth for "what callers
// should see" — command registration and global discovery both use it so the
// filter can't be forgotten at one site and applied at another.
func (r *Registry) EnabledServices() []string {
	all := r.ListServices()
	out := make([]string, 0, len(all))
	for _, s := range all {
		if !r.ServiceDisabled(s) {
			out = append(out, s)
		}
	}
	return out
}

// EnabledOperations is ListAllOperations minus operations belonging to a
// disabled service.
func (r *Registry) EnabledOperations() []OperationInfo {
	all := r.ListAllOperations()
	out := make([]OperationInfo, 0, len(all))
	for _, op := range all {
		if !r.ServiceDisabled(op.Service) {
			out = append(out, op)
		}
	}
	return out
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
	// FlagName is the optional CLI flag override from the x-octo-flag
	// extension on the parameter. It lets a spec expose a header/query param
	// whose wire name is awkward as a flag (e.g. the `If-Match` header) under a
	// clean, first-class flag name (e.g. `base-version`) without a hard-coded
	// carve-out in the engine. Empty when the spec does not set it, in which
	// case the engine derives the flag from Name.
	FlagName string `json:"flag_name,omitempty"`
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
	// SpaceHeaderSet records whether the spec declared x-octo-space-header at
	// all. It lets the transport distinguish an explicit `false` (suppress the
	// X-Space-Id header) from an omitted flag (keep the default behaviour of
	// sending it when the credential carries a space).
	SpaceHeaderSet bool `json:"space_header_set,omitempty"`
	Multipart      bool `json:"multipart,omitempty"`
	BinaryResponse bool `json:"binary_response,omitempty"`
	// BinaryBody is true only when the operation delivers a binary body inline
	// on a 2xx success (declared via a 2xx response with a content body), so
	// the CLI can write those bytes to disk with --output/-o. It is false for
	// redirect-style binary ops such as file.download, which return a 302 with
	// no consumable body — offering -o there is a silent no-op footgun.
	BinaryBody bool `json:"binary_body,omitempty"`
	// ValidateElementsIndex captures the x-octo-validate-elements-index
	// extension. When true, the CLI rejects a request whose body carries any
	// `elements[]` entry missing a valid fractional-index `index` before it is
	// sent — so index-less / garbage-index whiteboard elements (XIN-792) can no
	// longer reach the backend and corrupt a board.
	ValidateElementsIndex bool `json:"validate_elements_index,omitempty"`
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
	_, d.SpaceHeaderSet = doc["x-octo-space-header"]

	d.Multipart = boolOf(op["x-octo-multipart"])
	d.BinaryResponse = boolOf(op["x-octo-binary-response"])
	d.ValidateElementsIndex = boolOf(op["x-octo-validate-elements-index"])
	if d.BinaryResponse {
		if resps, ok := op["responses"].(map[string]any); ok {
			d.BinaryBody = hasSuccessBody(doc, resps)
		}
	}

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
				FlagName:    stringOf(pm["x-octo-flag"]),
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

// hasSuccessBody reports whether any 2xx response declares a content body,
// i.e. the operation returns bytes inline on success (docs.scene.export) rather
// than a bodyless redirect (file.download's 302-only). It gates the --output/-o
// flag so -o is offered only where it can actually write something.
//
// A 2xx entry may either inline the response object or factor it out via
// {"$ref": "#/components/responses/..."}. Both are handled: a response-level
// $ref is resolved through components.responses before the content check, so a
// spec that shares its success response does not fail-closed and silently drop
// -o. Only one hop is followed (OpenAPI response objects do not chain refs); an
// unresolvable ref is treated as "no body".
func hasSuccessBody(doc, resps map[string]any) bool {
	for code, r := range resps {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if ref, ok := rm["$ref"].(string); ok && ref != "" {
			resolved := followResponseRef(doc, ref)
			if resolved == nil {
				continue
			}
			rm = resolved
		}
		if content, ok := rm["content"].(map[string]any); ok && len(content) > 0 {
			return true
		}
	}
	return false
}

// followResponseRef resolves a `#/components/responses/<name>` pointer to its
// response object. It returns nil for any other ref shape or an unknown name.
func followResponseRef(doc map[string]any, ref string) map[string]any {
	const prefix = "#/components/responses/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	name := strings.TrimPrefix(ref, prefix)
	comps, ok := doc["components"].(map[string]any)
	if !ok {
		return nil
	}
	responses, ok := comps["responses"].(map[string]any)
	if !ok {
		return nil
	}
	s, _ := responses[name].(map[string]any)
	return s
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
func resolveSchema(doc, s map[string]any) SchemaInfo {
	return resolveSchemaWithDepth(doc, s, 0)
}

func resolveSchemaWithDepth(doc, s map[string]any, depth int) SchemaInfo { //nolint:gocyclo // recursive schema resolver, branching is inherent to OpenAPI
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

// truthy accepts a JSON value that may be a boolean `true` or the string
// `"true"`. It exists so the x-octo-disabled flag can't silently no-op when
// written as a string in a spec — the same rule the skill frontmatter uses.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	default:
		return false
	}
}
