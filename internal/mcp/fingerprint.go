package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// schemaFingerprint is a deterministic digest of one operation's parameter
// truth (the marshaled OperationDetail: id, path, method, parameters, request
// body, and the strict-schema flag). It exists for ONE purpose — schema drift
// detection: the two-tool `execute` facade can compare the fingerprint a caller
// read from `get_skill intent=describe` against the current one and refuse if
// the embedded spec changed underneath them (a rebuilt binary, a spec edit).
//
// It deliberately does NOT prove that describe was called in this session, nor
// that any particular client saw the schema: the value is derived only from the
// spec and is identical for every caller, so anyone can reproduce it without
// ever calling describe. A session-scoped "you must describe before you call"
// challenge is a separate, stateful mechanism and is documented as deferred
// (design §5, issue scope item 5). Determinism holds because encoding/json
// marshals map keys in sorted order and struct fields in declaration order, so
// the same spec always yields the same bytes.
func schemaFingerprint(d *registry.OperationDetail) string {
	raw, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// Enforcement scopes returned by constraintEnforcement.
const (
	enforceAllServices = "all-services" // the generic validator enforces this for every domain
	enforceStrictLocal = "local-strict" // enforced locally only for strict-request-schema operations
	enforceBackendOnly = "backend-only" // not gated locally for this operation; the backend is the authority
)

// extendedValidationApplies mirrors the engine's exact runtime gate for the
// extended request-schema checks (closed objects, minProperties, string
// lengths, maxItems): cmd/service/run.go enforces them when
// `rt.detail.Service == "loop" || rt.detail.StrictRequestSchema`. The facade
// projection MUST use the same condition, not a partial copy, so a Loop
// operation is reported accurately even when it never sets the strict flag.
func extendedValidationApplies(d *registry.OperationDetail) bool {
	return d.Service == "loop" || d.StrictRequestSchema
}

// constraintEnforcement projects, per operation, which request constraints are
// checked locally before any HTTP versus left to the backend — from the actual
// runtime behaviour documented in the engine, not an aspiration. The generic
// pre-flight validator (cmd/service/run.go) enforces required, minItems, enum,
// uint64 range and path safety for every service; the extended checks (closed
// objects, minProperties, string lengths, maxItems) run for Loop's Public API
// AND any operation that opts into x-octo-strict-request-schema. This is
// navigation metadata for the caller; the local gate itself is unchanged.
func constraintEnforcement(d *registry.OperationDetail) map[string]any {
	extended := enforceBackendOnly
	if extendedValidationApplies(d) {
		extended = enforceStrictLocal
	}
	ce := map[string]any{
		// Enforced locally for every domain.
		"required":     enforceAllServices,
		"min_items":    enforceAllServices,
		"enum":         enforceAllServices,
		"uint64_range": enforceAllServices,
		"path_safety":  enforceAllServices,
		// Extended checks: local for Loop + strict operations, else backend-only.
		"max_items":      extended,
		"min_properties": extended,
		"min_length":     extended,
		"max_length":     extended,
		"closed_object":  extended,
		// The switch that decides the extended set for this operation.
		"strict_request_schema": d.StrictRequestSchema,
		"extended_local":        extendedValidationApplies(d),
	}
	// Honesty for multipart operations: the local pre-flight validates required
	// fields and the file binding, but multipart form fields do not flow through
	// the JSON-body walker, so per-field content constraints (lengths, patterns,
	// closed-object) on them are backend-enforced — not projected as local here.
	if d.Multipart {
		ce["multipart_note"] = "multipart operation: local checks cover required fields and the file binding; per-form-field content constraints are backend-enforced, not locally projected"
	}
	return ce
}

// summaryView is the progressive-depth (depth=summary) projection of an
// operation: identity plus a shallow parameter/body outline, without the full
// nested property tree that depth=full returns. It lets a caller triage which
// operation to fully describe before pulling the complete schema.
func summaryView(d *registry.OperationDetail) map[string]any {
	out := map[string]any{
		"id":      d.ID,
		"service": d.Service,
		"method":  d.Method,
		"path":    d.Path,
	}
	if d.Summary != "" {
		out["summary"] = d.Summary
	}
	if d.Risk != "" {
		out["risk"] = d.Risk
	}
	if len(d.Parameters) > 0 {
		params := make([]map[string]any, 0, len(d.Parameters))
		for _, p := range d.Parameters {
			m := map[string]any{"name": p.Name, "in": p.In, "required": p.Required}
			if p.Type != "" {
				m["type"] = p.Type
			}
			if len(p.Enum) > 0 {
				m["enum"] = p.Enum
			}
			params = append(params, m)
		}
		out["parameters"] = params
	}
	if d.RequestBody != nil {
		body := map[string]any{}
		if d.RequestBody.Type != "" {
			body["type"] = d.RequestBody.Type
		}
		if len(d.RequestBody.Required) > 0 {
			body["required"] = d.RequestBody.Required
		}
		names := make([]string, 0, len(d.RequestBody.Properties))
		for k := range d.RequestBody.Properties {
			names = append(names, k)
		}
		sort.Strings(names)
		body["property_names"] = names
		out["request_body"] = body
		out["request_body_required"] = d.RequestBodyRequired
	}
	return out
}
