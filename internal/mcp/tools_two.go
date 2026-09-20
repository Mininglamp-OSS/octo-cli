package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// The two-tool Skill-driven facade (issue XIN-1958). It exposes the same
// registry/schema, execution, Skill mapping/resources, authz and error backend
// as the three meta-tools, folded into two verbs:
//
//   - get_skill: a discriminated discovery tool. intent=search is search_ops;
//     intent=describe is describe_op plus a schema fingerprint, a per-operation
//     constraint-enforcement projection, and progressive depth.
//   - execute: call_op with a structured status envelope (ok / validation_error
//     / ambiguous / execution_error / schema_drift), honest dry-run boundaries,
//     and optional schema-drift detection via the fingerprint.
//
// It is opt-in per connection (mcp serve --facade two|both). The three-tool
// facade stays the default and is unchanged, so the two can be compared
// side-by-side without ambiguity or a silent swap.

// twoToolDefinitions is the tools/list surface for the two-tool facade. Like
// the three-tool schemas these are tiny and carry no per-op schema or Skill
// body, so tools/list stays resident-cheap.
func twoToolDefinitions() []toolDef {
	return []toolDef{
		{
			Name:        "get_skill",
			Description: "Skill-driven discovery for Octo operations. intent=\"search\": find operations by domain and/or keyword (no domain and no query returns a service<->skill module map); returns operation ids + one-line summaries + which business Skill to load, NOT full schemas or Skill bodies. intent=\"describe\": return one operation's full zero-drift parameter schema (from the embedded spec), its Skill reference and pre-call advice, a per-operation constraint_enforcement projection, and a schema_fingerprint to pass to execute. depth=\"summary\" returns a shallow outline; depth=\"full\" (default) the complete schema. Parameter truth lives here; the Skill carries semantics only.",
			InputSchema: rawSchema(`{"type":"object","required":["intent"],"properties":{"intent":{"type":"string","enum":["search","describe"]},"domain":{"type":"string","description":"search: service filter, e.g. docs, message"},"query":{"type":"string","description":"search: keyword over id/summary/path"},"operation_id":{"type":"string","description":"describe: the operation id"},"depth":{"type":"string","enum":["summary","full"],"description":"describe depth; default full"}}}`),
		},
		{
			Name:        "execute",
			Description: "Invoke one Octo operation by id. arguments is a flat object keyed by the operation's declared params / body fields (see get_skill intent=describe). Returns a structured result: status is one of ok, validation_error, ambiguous, execution_error, schema_drift; the engine's JSON envelope is under \"envelope\". Set dry_run=true to construct and locally validate the request WITHOUT calling the backend (no server-side success is implied). Optionally pass the schema_fingerprint from describe: if the operation schema has drifted since you read it, execute returns schema_drift instead of calling. The fingerprint detects schema drift only; it does not prove describe was called this session. Security-sensitive fields forced by the server connection are ignored if supplied.",
			InputSchema: rawSchema(`{"type":"object","required":["operation_id"],"properties":{"operation_id":{"type":"string"},"arguments":{"type":"object"},"dry_run":{"type":"boolean","description":"construct + validate only; do not send"},"schema_fingerprint":{"type":"string","description":"fingerprint from describe; mismatch => schema_drift"}}}`),
		},
	}
}

// getSkill dispatches the discriminated get_skill tool.
func (s *Server) getSkill(intent, domain, query, operationID, depth string) toolResult {
	switch intent {
	case "search":
		// Reuse the exact same discovery backend as search_ops (contract parity).
		return s.searchOps(domain, query)
	case "describe":
		if operationID == "" {
			return jsonToolResult(map[string]any{
				"status": "validation_error",
				"error": map[string]any{
					"type": "validation", "code": "VALIDATION_ERROR",
					"message": "get_skill intent=describe requires operation_id",
					"hint":    "call get_skill intent=search to discover operation ids",
				},
			}, true)
		}
		return s.describeSkill(operationID, depth)
	default:
		return jsonToolResult(map[string]any{
			"status": "validation_error",
			"error": map[string]any{
				"type": "validation", "code": "VALIDATION_ERROR",
				"message": "get_skill requires intent to be \"search\" or \"describe\"",
			},
		}, true)
	}
}

// describeSkill is describe_op for the two-tool facade: the same zero-drift
// schema + Skill block + server-managed markers, plus a schema fingerprint, a
// constraint-enforcement projection, and progressive depth. It never copies
// parameter truth — depth=full marshals the same OperationDetail describe_op
// does; depth=summary is a shallow projection of it.
func (s *Server) describeSkill(operationID, depth string) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		p := unknownOperationPayload(s.reg, operationID, s.hintDiscover())
		p["status"] = "ambiguous"
		return jsonToolResult(p, true)
	}
	// Withhold disabled services and divergent ops exactly as describe_op does
	// (B6) — the facade must not become a discovery/introspection bypass.
	if s.reg.ServiceDisabled(detail.Service) {
		p := unknownOperationPayload(s.reg, operationID, s.hintDiscover())
		p["status"] = "ambiguous"
		return jsonToolResult(p, true)
	}
	if cli, div := divergentMCPOps[operationID]; div {
		return jsonToolResult(map[string]any{
			"status":       "validation_error",
			"operation_id": operationID,
			"envelope":     decodeEnvelope(synthErrorEnvelope(divergentOpError(operationID, cli))),
		}, true)
	}

	var obj map[string]any
	if depth == "summary" {
		obj = summaryView(detail)
	} else {
		depth = "full"
		raw, err := json.Marshal(detail)
		if err != nil {
			return jsonToolResult(map[string]any{"status": "internal_error", "error": err.Error()}, true)
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return jsonToolResult(map[string]any{"status": "internal_error", "error": err.Error()}, true)
		}
	}

	if nav := s.navFor(detail.OperationInfo, true); nav != nil {
		obj["skill"] = nav
	}
	if managed := s.trusted.serverManaged(operationID); len(managed) > 0 {
		names := make([]string, 0, len(managed))
		for k := range managed {
			names = append(names, k)
		}
		sort.Strings(names)
		obj["server_managed_arguments"] = names
		obj["server_managed_note"] = "these arguments are forced by the server connection (over-privilege防护); do not supply them"
	}
	obj["depth"] = depth
	obj["schema_fingerprint"] = schemaFingerprint(detail)
	obj["constraint_enforcement"] = constraintEnforcement(detail)
	return jsonToolResult(obj, false)
}

// execute is call_op for the two-tool facade: it reuses the identical
// assembly / identity / validation / transport / envelope backend and adds a
// structured status classification, honest dry-run, and schema-drift detection.
func (s *Server) execute(ctx context.Context, operationID string, arguments map[string]any, dryRun bool, fingerprint string) toolResult {
	detail, ok := s.reg.GetOperation(operationID)
	if !ok {
		p := unknownOperationPayload(s.reg, operationID, s.hintDiscover())
		p["status"] = "ambiguous"
		return jsonToolResult(p, true)
	}
	if s.reg.ServiceDisabled(detail.Service) {
		return jsonToolResult(map[string]any{
			"status":       "validation_error",
			"operation_id": operationID,
			"envelope":     decodeEnvelope(synthErrorEnvelope(disabledOpError(operationID, s.discoveryToolName()))),
		}, true)
	}
	// Divergent ops are uncallable via the schema-derived argv; refuse and point
	// at the CLI (B6), identical to call_op — no facade bypass.
	if cli, div := divergentMCPOps[operationID]; div {
		return jsonToolResult(map[string]any{
			"status":       "validation_error",
			"operation_id": operationID,
			"envelope":     decodeEnvelope(synthErrorEnvelope(divergentOpError(operationID, cli))),
		}, true)
	}

	// Schema-drift detection (drift only — see schemaFingerprint). Checked before
	// any side effect so a stale caller never fires a request against a schema it
	// did not actually read.
	current := schemaFingerprint(detail)
	if fingerprint != "" && fingerprint != current {
		return jsonToolResult(map[string]any{
			"status":               "schema_drift",
			"operation_id":         operationID,
			"expected_fingerprint": current,
			"supplied_fingerprint": fingerprint,
			"message":              "operation schema changed since you described it; call get_skill intent=describe again before executing",
			"note":                 "the fingerprint detects schema drift only; it does not prove describe was called this session",
		}, true)
	}

	if arguments == nil {
		arguments = map[string]any{}
	}

	// B1: a multipart dry-run must NOT open/read the file_path, materialize the
	// multipart body, or contact the backend. Short-circuit here and describe the
	// planned request from metadata only — the engine's dry-run would otherwise
	// stream the file bytes into envelope.data.body. Non-multipart dry-run still
	// goes through the engine (its dry-run reads nothing from disk).
	if dryRun && detail.Multipart {
		return s.multipartDryRun(detail, arguments, current)
	}

	// Over-privilege防护: force the connection's trusted values, same table as
	// call_op — a shared authz backend, not a second policy.
	s.trusted.apply(operationID, arguments)

	fn := s.factoryFn
	if fn == nil {
		fn = s.makeFactory
	}
	pol := execPolicy{httpMode: s.httpMode, uploadRoot: s.uploadRoot, describeHint: s.hintDescribe()}
	f, outBuf, errBuf := fn(s.trusted)
	var globalFlags []string
	if dryRun {
		globalFlags = []string{"--dry-run"}
	}
	env, okRun, exitErr := executeOperation(ctx, s.build, f, detail, arguments, pol, outBuf, errBuf, globalFlags...)
	return classifyExecuteResult(env, okRun, operationID, current, dryRun, detail.Method, exitErr)
}

// multipartDryRun describes a multipart operation's planned request WITHOUT any
// filesystem or network side effect (B1): the file at file_path is never opened
// or read, its bytes never appear in the result, and the multipart body is not
// materialized. Secret-flagged form fields are masked. Confinement of
// file_path to the operator upload root, identity resolution, and transport are
// enforced only on a real (non-dry-run) call; that residual limitation is
// stated in the note rather than silently implied.
func (s *Server) multipartDryRun(detail *registry.OperationDetail, args map[string]any, fingerprint string) toolResult {
	// Trusted-context projection first (over-privilege防护, #177 B4): a forced
	// field wins and is reflected in the plan, and must not be reported as
	// "missing" when the operator supplies it.
	s.trusted.apply(detail.ID, args)

	pol := execPolicy{httpMode: s.httpMode, uploadRoot: s.uploadRoot, describeHint: s.hintDescribe(), dryRun: true}
	// Real metadata-only validation: operation shape, required path/query/header
	// fields, declared multipart binding, reserved-flag guard, and the HTTP
	// upload-root policy gate — all WITHOUT opening/reading/materializing the
	// file and without backend I/O. An invalid dry-run is a validation_error,
	// never a false "ok".
	if verr := validateMultipartDryRun(detail, args, pol); verr != nil {
		return jsonToolResult(map[string]any{
			"status":       "validation_error",
			"operation_id": detail.ID,
			"dry_run":      true,
			"envelope":     decodeEnvelope(synthErrorEnvelope(verr)),
		}, true)
	}

	pathSet := map[string]bool{}
	for _, n := range extractPathParams(detail.Path) {
		pathSet[n] = true
	}
	fileField := binaryFileField(detail)
	formFields := map[string]any{}
	var filePath string
	for _, k := range sortedKeys(args) {
		if k == "file_path" {
			filePath = fmt.Sprint(args[k]) // the PATH string only; contents are never read
			continue
		}
		if pathSet[k] || k == "page_all" {
			continue
		}
		if detail.RequestBody != nil && detail.RequestBody.Properties[k].Secret {
			formFields[k] = "***"
			continue
		}
		formFields[k] = args[k]
	}
	data := map[string]any{
		"dry_run":     true,
		"multipart":   true,
		"method":      detail.Method,
		"path":        fillPathParams(detail.Path, args),
		"form_fields": formFields,
	}
	if filePath != "" {
		data["file_field"] = fileField
		data["file_path"] = filePath
	}
	note := "multipart dry-run: metadata-only. Required path/query/header/body fields and the multipart file binding were validated; file_path is NOT opened or read and its bytes are never returned; the multipart body is not materialized and nothing is sent. Symlink-resolved upload-root containment, identity resolution, and transport are enforced only on a real (non-dry-run) call."
	out := map[string]any{
		"status":             "ok",
		"operation_id":       detail.ID,
		"schema_fingerprint": fingerprint,
		"dry_run":            true,
		"dry_run_note":       note,
		"envelope":           map[string]any{"ok": true, "data": data},
	}
	return jsonToolResult(out, false)
}

// validateMultipartDryRun performs the metadata-only checks the engine would do
// up to (but not including) reading the file: it reuses buildArgv with a dry-run
// policy (which enforces path-arg presence, the reserved-flag guard, undeclared
// multipart-field rejection, and the pure HTTP upload-root gate without touching
// the filesystem), then adds the required query/header/body-field checks the
// engine's pre-flight validator applies. The binary file field is satisfied by
// the file_path binding rather than a form value.
func validateMultipartDryRun(d *registry.OperationDetail, args map[string]any, pol execPolicy) error {
	hint := pol.describeHint
	if _, err := buildArgv(d, args, pol); err != nil {
		return output.ErrValidation(err.Error(), hint)
	}
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if !p.Required || (p.In != "query" && p.In != "header") {
			continue
		}
		if _, ok := args[p.Name]; !ok {
			return output.ErrValidation(fmt.Sprintf("missing required %s parameter %q for %s", p.In, p.Name, d.ID), hint)
		}
	}
	if d.RequestBody == nil {
		return nil
	}
	_, fileProvided := args["file_path"]
	for _, name := range d.RequestBody.Required {
		prop, isProp := d.RequestBody.Properties[name]
		if isProp && prop.Type == "string" && prop.Format == "binary" {
			if !fileProvided {
				return output.ErrValidation(
					fmt.Sprintf("missing required multipart file binding: provide file_path for field %q of %s", name, d.ID), hint)
			}
			continue
		}
		if _, ok := args[name]; !ok {
			return output.ErrValidation(fmt.Sprintf("missing required field %q for %s", name, d.ID), hint)
		}
	}
	return nil
}

// binaryFileField returns the name of the multipart binary field (schema
// string/binary), defaulting to "file" when none is declared explicitly.
func binaryFileField(d *registry.OperationDetail) string {
	if d.RequestBody != nil {
		names := make([]string, 0, len(d.RequestBody.Properties))
		for n := range d.RequestBody.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if p := d.RequestBody.Properties[n]; p.Type == "string" && p.Format == "binary" {
				return n
			}
		}
	}
	return "file"
}

// fillPathParams substitutes {name} path placeholders from args for the
// dry-run description. A missing value is left as the literal placeholder so
// the gap is visible rather than silently dropped.
func fillPathParams(path string, args map[string]any) string {
	out := path
	for _, n := range extractPathParams(path) {
		if v, ok := args[n]; ok {
			out = strings.ReplaceAll(out, "{"+n+"}", fmt.Sprint(v))
		}
	}
	return out
}

// classifyExecuteResult wraps the engine's JSON envelope in the two-tool status
// contract. The envelope itself is preserved verbatim under "envelope" (the
// single source of truth); status is a projection of it, never a rewrite.
//
// Machine-readable outcome semantics (facade contract for agent callers):
//   - auth_error       — authentication failed; NOT a generic retryable failure
//     (a caller must fix the credential, not blindly re-issue).
//   - result_unknown   — a MUTATING request whose outcome is ambiguous (transport
//     failure / timeout / 5xx after the server may have committed). It may have
//     been applied; callers MUST NOT auto-retry without an idempotency key.
//   - validation_error — refused locally before/at request build; never applied.
//   - execution_error  — a definite failure with a known, non-applied outcome
//     (4xx refusal, rate limit, or a read that produced no side effect).
//
// The ambiguity split is authoritative: it uses the engine's own
// ExitError.OutcomeUnknown() (HTTP status < 400 or > 499), combined with whether
// the operation mutates, rather than re-deriving 4xx/5xx from the rendered JSON.
func classifyExecuteResult(env []byte, okRun bool, operationID, fingerprint string, dryRun bool, method string, exitErr *output.ExitError) toolResult {
	parsed := decodeEnvelope(env)

	status := "ok"
	if !okRun {
		status = classifyFailure(method, parsed, exitErr)
	}

	out := map[string]any{
		"status":             status,
		"operation_id":       operationID,
		"schema_fingerprint": fingerprint,
		"envelope":           parsed,
	}
	switch status {
	case "result_unknown":
		out["code"] = "RESULT_UNKNOWN"
		out["retryable"] = false
		out["note"] = "mutating request with an ambiguous outcome (transport/timeout/5xx); it may have been applied. Do NOT auto-retry without an idempotency key."
	case "auth_error":
		out["retryable"] = false
		out["note"] = "authentication failed; fix the credential rather than retrying as a generic execution error."
	}
	if dryRun {
		out["dry_run"] = true
		out["dry_run_note"] = "request was constructed and locally validated only; nothing was sent and server-side success is not implied. Local file reads, multipart parts, and composite sub-requests are described, not performed."
	}
	return jsonToolResult(out, status != "ok")
}

// classifyFailure maps a failed run to a machine-readable status. It prefers the
// structured ExitError (which carries the HTTP status the rendered envelope
// drops) and falls back to the envelope's error.type when no ExitError is
// available (e.g. a synthesized envelope).
func classifyFailure(method string, parsed map[string]any, exitErr *output.ExitError) string {
	if exitErr != nil {
		switch exitErr.Type {
		case "auth_error":
			return "auth_error"
		case "validation", "config":
			return "validation_error"
		}
		// Any non-local failure on a mutating op whose outcome the engine cannot
		// confirm (transport/timeout/5xx) is result_unknown; a definite 4xx
		// refusal, rate limit, or a side-effect-free read stays execution_error.
		if isMutatingMethod(method) && exitErr.OutcomeUnknown() {
			return "result_unknown"
		}
		return "execution_error"
	}
	// No structured error (synthesized envelope): fall back to error.type only.
	if errObj, _ := parsed["error"].(map[string]any); errObj != nil {
		switch errObj["type"] {
		case "auth_error":
			return "auth_error"
		case "validation", "config":
			return "validation_error"
		}
	}
	return "execution_error"
}

// isMutatingMethod reports whether an HTTP method can change server state. A
// safe method (GET/HEAD) never leaves an ambiguous "may have applied" outcome.
func isMutatingMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "":
		return false
	default:
		return true
	}
}

// decodeEnvelope parses a JSON envelope into a map, falling back to a raw-text
// wrapper if the bytes are not a JSON object (should not happen — the engine
// always emits one — but the facade must never lose the payload).
func decodeEnvelope(env []byte) map[string]any {
	var parsed map[string]any
	if err := json.Unmarshal(env, &parsed); err == nil && parsed != nil {
		return parsed
	}
	return map[string]any{"ok": false, "raw": string(env)}
}
