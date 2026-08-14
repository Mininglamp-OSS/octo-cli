// Package registry loads the embedded OpenAPI 3.x specs for each Octo service
// domain (matter, message, group, thread, file, bot, event, marketplace). Consumers read
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
	if err := checkDuplicateOperationIDs(r.specs); err != nil {
		return nil, err
	}
	return r, nil
}

// checkDuplicateOperationIDs rejects an operationId claimed by more than one
// spec. The id namespace is global: GetOperation resolves an id by iterating
// the service map, so a duplicate would make command routing depend on Go's
// randomized map order — a different backend could win on every process start.
// Mirrors the duplicate-service guard above: a collision between embedded
// specs is a build-time bug, surfaced on first registry load. Hidden and
// disabled operations are included because GetOperation still resolves them.
func checkDuplicateOperationIDs(specs map[string]map[string]any) error {
	owner := map[string]string{}
	services := make([]string, 0, len(specs))
	for s := range specs {
		services = append(services, s)
	}
	sort.Strings(services)
	for _, service := range services {
		var dupErr error
		walkOperations(specs[service], func(_, _ string, op map[string]any) {
			id, _ := op["operationId"].(string)
			if id == "" || dupErr != nil {
				return
			}
			if prev, dup := owner[id]; dup {
				dupErr = fmt.Errorf("registry: duplicate operationId %q (services %q and %q)", id, prev, service)
				return
			}
			owner[id] = service
		})
		if dupErr != nil {
			return dupErr
		}
	}
	return nil
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

// ParamInfo describes a single parameter on a path or operation (one entry in
// an OpenAPI `parameters` array). Covers path, query, and header parameters;
// the `In` field records which. Type/Default/Enum come from the nested
// schema when present.
type ParamInfo struct {
	Name        string      `json:"name"`
	In          string      `json:"in"`
	Required    bool        `json:"required,omitempty"`
	Type        string      `json:"type,omitempty"`
	Format      string      `json:"format,omitempty"`
	Items       *SchemaInfo `json:"items,omitempty"`
	Description string      `json:"description,omitempty"`
	Default     any         `json:"default,omitempty"`
	Enum        []any       `json:"enum,omitempty"`
	// String constraints are exposed as schema metadata for callers; enforcing
	// them remains the service backend's job. Enum is the exception: the CLI
	// checks a query parameter's enum before sending (see buildQuery).
	MinLength int    `json:"min_length,omitempty"`
	MaxLength int    `json:"max_length,omitempty"`
	Pattern   string `json:"pattern,omitempty"`
	// Secret records the x-octo-secret extension. A secret parameter's value is
	// masked in --verbose traces and --dry-run output (share/invite tokens,
	// share passwords) so a credential-equivalent value never lands in a log.
	Secret bool `json:"secret,omitempty"`
	// FlagName is the optional CLI flag override from the x-octo-flag
	// extension on the parameter. It lets a spec expose a header/query param
	// whose wire name is awkward as a flag (e.g. the `If-Match` header) under a
	// clean, first-class flag name (e.g. `base-version`) without a hard-coded
	// carve-out in the engine. Empty when the spec does not set it, in which
	// case the engine derives the flag from Name.
	//
	// On a `"in": "path"` parameter it means something slightly different: the
	// param stays positional, and the flag becomes an OPTIONAL alternative way
	// to supply it. That is the escape hatch for a value cobra cannot accept
	// positionally at all — a base64url id starting with "-" is parsed as a flag
	// before the command runs (see cmd/service.pathFlag).
	FlagName string `json:"flag_name,omitempty"`
}

// SchemaInfo is a trimmed projection of an OpenAPI schema — just enough for
// the schema command and generic request validator. It resolves bounded local
// references, nullable type unions, and composition constraints while leaving
// an unresolved `$ref` visible when the depth guard is reached.
type SchemaInfo struct {
	Type                 string                `json:"type,omitempty"`
	Required             []string              `json:"required,omitempty"`
	Properties           map[string]SchemaInfo `json:"properties,omitempty"`
	AdditionalProperties *bool                 `json:"additional_properties,omitempty"`
	Items                *SchemaInfo           `json:"items,omitempty"`
	OneOf                []SchemaInfo          `json:"one_of,omitempty"`
	AnyOf                []SchemaInfo          `json:"any_of,omitempty"`
	Enum                 []any                 `json:"enum,omitempty"`
	Const                any                   `json:"const,omitempty"`
	Format               string                `json:"format,omitempty"`
	Description          string                `json:"description,omitempty"`
	WriteOnly            bool                  `json:"write_only,omitempty"`
	// These constraints are surfaced for schema introspection. The generic CLI
	// validator enforces Required, MinItems and Enum for every service. Loop
	// Public API operations additionally enforce closed-object and minimum-
	// property constraints, MinLength, MaxLength and MaxItems. Pattern
	// remains descriptive and is left to the backend.
	MinLength     int    `json:"min_length,omitempty"`
	MaxLength     int    `json:"max_length,omitempty"`
	MinItems      int    `json:"min_items,omitempty"`
	MaxItems      int    `json:"max_items,omitempty"`
	MinProperties int    `json:"min_properties,omitempty"`
	Pattern       string `json:"pattern,omitempty"`
	Ref           string `json:"$ref,omitempty"`
	// FlagName is the optional CLI flag override from the x-octo-flag extension
	// on a request-body property, mirroring ParamInfo.FlagName for query/header
	// params. It lets a promoted body field expose a clean flag name (e.g.
	// --scope) while the wire body key stays the property name (e.g. shareScope),
	// so the CLI can honour a caller-facing flag without diverging from the
	// byte-exact backend contract. Empty means "derive from the property name".
	FlagName string `json:"flag_name,omitempty"`
	// Secret records the x-octo-secret extension on a request-body property,
	// mirroring ParamInfo.Secret. Its value is masked in --verbose traces and
	// --dry-run output.
	Secret bool `json:"secret,omitempty"`
}

// PaginationInfo captures the `x-octo-pagination` extension. It tells the
// service engine which query/body parameters carry the cursor / limit and
// which response paths carry the items / next cursor / optional has-more flag,
// so `--page-all` can walk pages without operation-specific code.
type PaginationInfo struct {
	CursorParam         string `json:"cursor_param,omitempty"`
	LimitParam          string `json:"limit_param,omitempty"`
	ItemsField          string `json:"items_field,omitempty"`
	CursorField         string `json:"cursor_field,omitempty"`
	HasMoreField        string `json:"has_more_field,omitempty"`
	InferHasMore        bool   `json:"infer_has_more,omitempty"`
	RejectCursorRepeats bool   `json:"reject_cursor_repeats,omitempty"`
}

// OperationDetail is the expanded view of a single operation returned by
// GetOperation. It embeds OperationInfo for identity/summary and adds every
// piece of metadata the service engine needs to build a complete cobra
// command: parameters, request/response schemas, pagination, base-URL
// routing, space-header behaviour, and payload shape flags (multipart /
// binary response).
type OperationDetail struct {
	OperationInfo
	Parameters          []ParamInfo     `json:"parameters,omitempty"`
	RequestBody         *SchemaInfo     `json:"request_body,omitempty"`
	RequestBodyRequired bool            `json:"request_body_required,omitempty"`
	ResponseSchema      *SchemaInfo     `json:"response_schema,omitempty"`
	Pagination          *PaginationInfo `json:"pagination,omitempty"`
	// BaseURLEnv is retained in schema output for compatibility with existing
	// specs and tooling. Runtime routing is unified through OCTO_API_BASE_URL and
	// intentionally does not select a different service URL from this metadata.
	BaseURLEnv string `json:"base_url_env,omitempty"`
	// Credential is the x-octo-credential boundary declared by the service.
	// Empty means the active Bot credential; "mail" selects the mailbox token
	// bound to the same Bot identity.
	Credential  string `json:"credential,omitempty"`
	SpaceHeader bool   `json:"space_header,omitempty"`
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
	// RetryMode captures the optional x-octo-retry operation extension. Empty
	// keeps the transport default; "never" disables automatic retries for the
	// individual request. This is required for non-idempotent external side
	// effects such as sending mail, where a lost response must be reported as an
	// unknown result rather than risking a duplicate action.
	RetryMode string `json:"retry_mode,omitempty"`
	// AllowedTokenKinds captures x-octo-allowed-token-kinds (spec top level or
	// per-operation override). When non-empty the CLI checks the active
	// credential's kind against the list before sending, failing locally with
	// TOKEN_KIND_NOT_ALLOWED (validation / exit 2) instead of relying on a
	// backend 401. Empty means "no local gate" — the historical behaviour for
	// every domain that does not declare it.
	AllowedTokenKinds []string `json:"allowed_token_kinds,omitempty"`
	// MountByTokenKind captures x-octo-mount-by-token-kind: a token-kind →
	// server mount-prefix table. When non-empty, the operation's own path must
	// begin with exactly one of the table's values (the mount the spec paths are
	// written against); that prefix is swapped for the mount matching the active
	// credential's kind. Empty leaves the spec path byte-identical.
	MountByTokenKind map[string]string `json:"mount_by_token_kind,omitempty"`
	// ResponseFieldAliases captures x-octo-response-fields: source key →
	// target keys. The CLI renames (one target) or duplicates (several) the
	// value in the response before emitting it, so a backend DTO with a bare
	// `id` can surface as the unambiguous `share_id` + `share_token` an Agent
	// needs. Paths use the same syntax as LosslessIDFields.
	ResponseFieldAliases map[string][]string `json:"response_field_aliases,omitempty"`
	// LosslessIDFields captures x-octo-lossless-id-fields: response paths whose
	// uint64 ids are emitted as decimal strings so values above 2^53 survive an
	// Agent's JSON parser. Applied after ResponseFieldAliases, so paths name
	// post-alias keys.
	LosslessIDFields []string `json:"lossless_id_fields,omitempty"`
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
		if truthy(op["x-octo-cli-hidden"]) {
			return
		}
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
			if truthy(op["x-octo-cli-hidden"]) {
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
		Credential:  stringOf(doc["x-octo-credential"]),
		SpaceHeader: boolOf(doc["x-octo-space-header"]),
	}
	_, d.SpaceHeaderSet = doc["x-octo-space-header"]

	d.Multipart = boolOf(op["x-octo-multipart"])
	d.BinaryResponse = boolOf(op["x-octo-binary-response"])
	d.ValidateElementsIndex = boolOf(op["x-octo-validate-elements-index"])
	d.RetryMode = stringOf(op["x-octo-retry"])
	// Identity routing is declared at the spec top level (every operation in a
	// domain shares one mount table) but an operation may narrow the allowed
	// kinds. Both default to absent → no gate, no rewrite.
	d.AllowedTokenKinds = stringSliceOf(doc["x-octo-allowed-token-kinds"])
	if opKinds := stringSliceOf(op["x-octo-allowed-token-kinds"]); len(opKinds) > 0 {
		d.AllowedTokenKinds = opKinds
	}
	d.MountByTokenKind = stringMapOf(doc["x-octo-mount-by-token-kind"])
	d.ResponseFieldAliases = stringSliceMapOf(op["x-octo-response-fields"])
	d.LosslessIDFields = stringSliceOf(op["x-octo-lossless-id-fields"])
	if d.BinaryResponse {
		if resps, ok := op["responses"].(map[string]any); ok {
			d.BinaryBody = hasSuccessBody(doc, resps)
		}
	}

	d.Parameters = operationParameters(doc, pathStr, op)
	d.RequestBody, d.RequestBodyRequired = operationRequestBody(doc, op)

	if resps, ok := op["responses"].(map[string]any); ok {
		d.ResponseSchema = firstSuccessSchema(doc, resps)
	}

	if pag, ok := op["x-octo-pagination"].(map[string]any); ok {
		d.Pagination = &PaginationInfo{
			CursorParam:         stringOf(pag["cursorParam"]),
			LimitParam:          stringOf(pag["limitParam"]),
			ItemsField:          stringOf(pag["itemsField"]),
			CursorField:         stringOf(pag["cursorField"]),
			HasMoreField:        stringOf(pag["hasMoreField"]),
			InferHasMore:        truthy(pag["inferHasMore"]),
			RejectCursorRepeats: truthy(pag["rejectCursorRepeats"]),
		}
	}

	return d
}

func operationParameters(doc map[string]any, pathStr string, op map[string]any) []ParamInfo {
	// OpenAPI allows parameters shared by every operation on a path to be
	// declared on the Path Item Object. Operation-level parameters are merged
	// afterwards and override a path-level parameter with the same name and
	// location (OpenAPI 3.1 section 4.8.12.1).
	var pathParams []any
	if paths, ok := doc["paths"].(map[string]any); ok {
		if pathItem, ok := paths[pathStr].(map[string]any); ok {
			pathParams, _ = pathItem["parameters"].([]any)
		}
	}
	opParams, _ := op["parameters"].([]any)

	result := make([]ParamInfo, 0, len(pathParams)+len(opParams))
	indexes := make(map[string]int, len(pathParams)+len(opParams))
	appendParams := func(params []any) {
		for _, raw := range params {
			pm, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if ref := stringOf(pm["$ref"]); ref != "" {
				pm = followComponentRef(doc, ref, "parameters")
				if pm == nil {
					continue
				}
			}
			parameter := ParamInfo{
				Name:        stringOf(pm["name"]),
				In:          stringOf(pm["in"]),
				Required:    boolOf(pm["required"]),
				Description: stringOf(pm["description"]),
				FlagName:    stringOf(pm["x-octo-flag"]),
				Secret:      truthy(pm["x-octo-secret"]),
			}
			if schema, ok := pm["schema"].(map[string]any); ok {
				parameter.Type = stringOf(schema["type"])
				parameter.Format = stringOf(schema["format"])
				parameter.Default = schema["default"]
				if items, ok := schema["items"].(map[string]any); ok {
					resolved := resolveSchema(doc, items)
					parameter.Items = &resolved
				}
				if enum, ok := schema["enum"].([]any); ok {
					parameter.Enum = enum
				}
				parameter.MinLength = intOf(schema["minLength"])
				parameter.MaxLength = intOf(schema["maxLength"])
				parameter.Pattern = stringOf(schema["pattern"])
			}
			name := parameter.Name
			if strings.EqualFold(parameter.In, "header") {
				name = strings.ToLower(name)
			}
			key := strings.ToLower(parameter.In) + "\x00" + name
			if index, exists := indexes[key]; exists {
				result[index] = parameter
				continue
			}
			indexes[key] = len(result)
			result = append(result, parameter)
		}
	}
	appendParams(pathParams)
	appendParams(opParams)
	return result
}

func operationRequestBody(doc, op map[string]any) (*SchemaInfo, bool) {
	body, ok := op["requestBody"].(map[string]any)
	if !ok {
		return nil, false
	}
	if ref := stringOf(body["$ref"]); ref != "" {
		body = followComponentRef(doc, ref, "requestBodies")
		if body == nil {
			return nil, false
		}
	}
	required := boolOf(body["required"])
	if schema := extractJSONSchema(body); schema != nil {
		resolved := resolveSchema(doc, schema)
		return &resolved, required
	}
	return nil, required
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
	return followComponentRef(doc, ref, "responses")
}

func followComponentRef(doc map[string]any, ref, section string) map[string]any {
	prefix := "#/components/" + section + "/"
	if !strings.HasPrefix(ref, prefix) {
		return nil
	}
	name := strings.TrimPrefix(ref, prefix)
	comps, ok := doc["components"].(map[string]any)
	if !ok {
		return nil
	}
	entries, ok := comps[section].(map[string]any)
	if !ok {
		return nil
	}
	s, _ := entries[name].(map[string]any)
	return s
}

func firstSuccessSchema(doc, resps map[string]any) *SchemaInfo {
	// Prefer 200, then 201, then anything 2xx.
	order := []string{"200", "201", "204"}
	for _, code := range order {
		if r, ok := resps[code].(map[string]any); ok {
			if ref := stringOf(r["$ref"]); ref != "" {
				r = followResponseRef(doc, ref)
			}
			if s := extractJSONSchema(r); s != nil {
				resolved := resolveSchema(doc, s)
				return &resolved
			}
			// A valid success status may document only a description (for
			// example an idempotent 200 replay). Keep looking for another 2xx
			// response that carries the shared success schema.
			continue
		}
	}
	for code, r := range resps {
		if !strings.HasPrefix(code, "2") {
			continue
		}
		if rm, ok := r.(map[string]any); ok {
			if ref := stringOf(rm["$ref"]); ref != "" {
				rm = followResponseRef(doc, ref)
			}
			if s := extractJSONSchema(rm); s != nil {
				resolved := resolveSchema(doc, s)
				return &resolved
			}
		}
	}
	return nil
}

// resolveSchema performs a bounded walk of a schema node. The budget counts
// followed references rather than ordinary property/item nesting, so deeply
// nested acyclic schemas remain enforceable while recursive component models
// still terminate with an unresolved `$ref`.
func resolveSchema(doc, s map[string]any) SchemaInfo {
	return resolveSchemaWithDepth(doc, s, 0)
}

func resolveSchemaWithDepth(doc, s map[string]any, depth int) SchemaInfo {
	if s == nil {
		return SchemaInfo{}
	}
	if resolved, ok := resolveSchemaReference(doc, s, depth); ok {
		return resolved
	}

	info := schemaInfoFromNode(s)
	mergeAllOfSchemas(&info, doc, s["allOf"], depth)
	appendSchemaRequired(&info, s["required"])
	appendSchemaProperties(&info, doc, s["properties"], depth)
	appendSchemaItems(&info, doc, s["items"], depth)
	info.OneOf = resolveSchemaAlternatives(doc, s["oneOf"], depth)
	info.AnyOf = resolveSchemaAlternatives(doc, s["anyOf"], depth)
	return info
}

func resolveSchemaReference(doc, schema map[string]any, depth int) (SchemaInfo, bool) {
	ref, ok := schema["$ref"].(string)
	if !ok || ref == "" {
		return SchemaInfo{}, false
	}
	if depth < 3 {
		if resolved := followRef(doc, ref); resolved != nil {
			return resolveSchemaWithDepth(doc, resolved, depth+1), true
		}
	}
	return SchemaInfo{Ref: ref}, true
}

func schemaInfoFromNode(schema map[string]any) SchemaInfo {
	info := SchemaInfo{
		Type:          schemaType(schema["type"]),
		Format:        stringOf(schema["format"]),
		Description:   stringOf(schema["description"]),
		FlagName:      stringOf(schema["x-octo-flag"]),
		Secret:        truthy(schema["x-octo-secret"]),
		WriteOnly:     boolOf(schema["writeOnly"]),
		MinLength:     intOf(schema["minLength"]),
		MaxLength:     intOf(schema["maxLength"]),
		MinItems:      intOf(schema["minItems"]),
		MaxItems:      intOf(schema["maxItems"]),
		MinProperties: intOf(schema["minProperties"]),
		Pattern:       stringOf(schema["pattern"]),
	}
	if allowed, ok := schema["additionalProperties"].(bool); ok {
		info.AdditionalProperties = &allowed
	}
	// A schema-valued additionalProperties declaration describes allowed
	// dynamic values. Treat it as open until the validator can retain that
	// nested schema instead of incorrectly rejecting every dynamic key.
	if value, ok := schema["const"]; ok {
		info.Const = value
	}
	if enum, ok := schema["enum"].([]any); ok {
		info.Enum = enum
	}
	return info
}

func mergeAllOfSchemas(dst *SchemaInfo, doc map[string]any, value any, depth int) {
	allOf, ok := value.([]any)
	if !ok {
		return
	}
	for _, candidate := range allOf {
		if sub, ok := candidate.(map[string]any); ok {
			resolved := resolveSchemaWithDepth(doc, sub, depth)
			mergeSchemaInfo(dst, &resolved)
		}
	}
}

func appendSchemaRequired(dst *SchemaInfo, value any) {
	required, ok := value.([]any)
	if !ok {
		return
	}
	for _, candidate := range required {
		if name, ok := candidate.(string); ok {
			dst.Required = appendUniqueStrings(dst.Required, name)
		}
	}
}

func appendSchemaProperties(dst *SchemaInfo, doc map[string]any, value any, depth int) {
	properties, ok := value.(map[string]any)
	if !ok {
		return
	}
	if dst.Properties == nil {
		dst.Properties = map[string]SchemaInfo{}
	}
	for name, candidate := range properties {
		if sub, ok := candidate.(map[string]any); ok {
			dst.Properties[name] = resolveSchemaWithDepth(doc, sub, depth)
		}
	}
}

func appendSchemaItems(dst *SchemaInfo, doc map[string]any, value any, depth int) {
	items, ok := value.(map[string]any)
	if !ok {
		return
	}
	resolved := resolveSchemaWithDepth(doc, items, depth)
	dst.Items = &resolved
}

func schemaType(value any) string {
	if single, ok := value.(string); ok {
		return single
	}
	union, ok := value.([]any)
	if !ok {
		return ""
	}
	for _, candidate := range union {
		if typ, ok := candidate.(string); ok && typ != "null" {
			return typ
		}
	}
	return ""
}

func resolveSchemaAlternatives(doc map[string]any, value any, depth int) []SchemaInfo {
	alternatives, ok := value.([]any)
	if !ok {
		return nil
	}
	resolved := make([]SchemaInfo, 0, len(alternatives))
	for _, candidate := range alternatives {
		if sub, ok := candidate.(map[string]any); ok {
			resolved = append(resolved, resolveSchemaWithDepth(doc, sub, depth))
		}
	}
	return resolved
}

func mergeSchemaInfo(dst, src *SchemaInfo) {
	mergeSchemaIdentity(dst, src)
	mergeSchemaCollections(dst, src)
	mergeSchemaConstraints(dst, src)
}

func mergeSchemaIdentity(dst, src *SchemaInfo) {
	if dst.Type == "" {
		dst.Type = src.Type
	}
	if dst.Format == "" {
		dst.Format = src.Format
	}
	if dst.Description == "" {
		dst.Description = src.Description
	}
	if dst.FlagName == "" {
		dst.FlagName = src.FlagName
	}
	dst.WriteOnly = dst.WriteOnly || src.WriteOnly
	if dst.Const == nil {
		dst.Const = src.Const
	}
}

func mergeSchemaCollections(dst, src *SchemaInfo) {
	dst.Required = appendUniqueStrings(dst.Required, src.Required...)
	if len(src.Properties) > 0 {
		if dst.Properties == nil {
			dst.Properties = map[string]SchemaInfo{}
		}
		for name := range src.Properties {
			dst.Properties[name] = src.Properties[name]
		}
	}
	mergeAdditionalProperties(dst, src)
	if dst.Items == nil {
		dst.Items = src.Items
	}
	dst.OneOf = append(dst.OneOf, src.OneOf...)
	dst.AnyOf = append(dst.AnyOf, src.AnyOf...)
	if len(dst.Enum) == 0 {
		dst.Enum = src.Enum
	}
}

func mergeAdditionalProperties(dst, src *SchemaInfo) {
	if src.AdditionalProperties == nil {
		return
	}
	if dst.AdditionalProperties == nil || !*src.AdditionalProperties {
		allowed := *src.AdditionalProperties
		dst.AdditionalProperties = &allowed
	}
}

func mergeSchemaConstraints(dst, src *SchemaInfo) {
	if dst.MinLength == 0 {
		dst.MinLength = src.MinLength
	}
	if dst.MaxLength == 0 {
		dst.MaxLength = src.MaxLength
	}
	if dst.MinItems == 0 {
		dst.MinItems = src.MinItems
	}
	if dst.MaxItems == 0 {
		dst.MaxItems = src.MaxItems
	}
	if src.MinProperties > dst.MinProperties {
		dst.MinProperties = src.MinProperties
	}
	if dst.Pattern == "" {
		dst.Pattern = src.Pattern
	}
}

func appendUniqueStrings(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
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

func intOf(v any) int {
	n, _ := v.(float64)
	return int(n)
}

// stringSliceOf reads a JSON array of strings, ignoring non-string entries.
// A missing or wrongly-typed value yields nil, which every consumer treats as
// "extension not declared".
func stringSliceOf(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringMapOf reads a JSON object of string→string. Non-string values are
// dropped rather than coerced, so a malformed table degrades to "not declared"
// for that key instead of producing a bogus path.
func stringMapOf(v any) map[string]string {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(obj))
	for k, x := range obj {
		if s, ok := x.(string); ok && s != "" {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stringSliceMapOf reads a JSON object of string→[]string. A scalar string
// value is accepted as a one-element list so a simple rename can be written
// without array syntax.
func stringSliceMapOf(v any) map[string][]string {
	obj, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(obj))
	for k, x := range obj {
		switch t := x.(type) {
		case string:
			if t != "" {
				out[k] = []string{t}
			}
		case []any:
			if targets := stringSliceOf(t); len(targets) > 0 {
				out[k] = targets
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
