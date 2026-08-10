package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// runOperation is the RunE body for every auto-registered operation.
// Builds the outbound request from flags/args, dispatches (with pagination
// if requested), and emits the envelope.
func runOperation(cobraCmd *cobra.Command, f *cmdutil.Factory, rt *operationRuntime, args []string) error {
	ctx := cobraCmd.Context()
	d := rt.detail

	// Path substitution. cobra.ExactArgs already ensured count.
	urlPath := marketplacePath(d.Service, d.Path)
	// Identity routing: reject an incompatible credential kind and pick the
	// server mount for the one in use, before any path parameter is filled in.
	urlPath, err := applyIdentityRouting(f, d, urlPath)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	// Path values come from the positional args, except for slots a
	// spec-declared path flag filled in (the escape hatch for ids starting
	// with "-").
	pathValues, verr := resolvePathValues(cobraCmd, rt, args)
	if verr != nil {
		_ = f.EmitError(verr) //nolint:errcheck // best-effort emit before returning err
		return verr
	}
	// Path values carrying a backend uint64 id are range-checked here so an
	// id mangled by an Agent's JSON parser fails locally instead of hitting a
	// backend 400 (or worse, addressing a different row).
	if verr := validatePathArgs(rt, pathValues); verr != nil {
		_ = f.EmitError(verr) //nolint:errcheck // best-effort emit before returning err
		return verr
	}
	for i, pname := range rt.pathParams {
		placeholder := "{" + pname + "}"
		urlPath = strings.ReplaceAll(urlPath, placeholder, url.PathEscape(pathValues[i]))
	}

	// Query parameters (only emit flags explicitly set by the user so defaults
	// don't override backend defaults).
	q, verr := buildQuery(cobraCmd, rt)
	if verr != nil {
		_ = f.EmitError(verr) //nolint:errcheck // best-effort emit before returning err
		return verr
	}

	// Header parameters (spec `"in": "header"`). Emit only headers the user
	// explicitly set so an omitted optional header stays absent. This is how a
	// spec-declared header such as If-Match (optimistic-concurrency base
	// version) reaches the wire from a flag — no per-endpoint transport code.
	headers := buildHeaders(cobraCmd, rt)

	// Body: start from --data (if any), then merge explicit flags on top.
	// Multipart ops take a separate path — they build a form body, not JSON.
	req := client.Request{
		Service:        serviceForBaseURL(d.BaseURLEnv),
		Method:         d.Method,
		Path:           urlPath,
		Query:          q,
		Headers:        headers,
		BinaryResponse: d.BinaryResponse,
		// Suppress X-Space-Id only when the spec explicitly declares
		// x-octo-space-header:false. An omitted flag keeps the default
		// behaviour of sending the header when the credential has a space.
		SuppressSpaceHeader: d.SpaceHeaderSet && !d.SpaceHeader,
		// Values the spec marked x-octo-secret, masked in verbose / dry-run.
		SecretValues: collectSecrets(cobraCmd, rt, pathValues),
	}
	// Binary-response ops may carry an --output/-o destination; when set, the
	// client writes the 2xx body to that path instead of only describing it.
	if rt.outputPath != nil {
		req.OutputPath = *rt.outputPath
	}
	if d.Multipart {
		raw, ct, err := buildMultipartBody(cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		req.RawBody = raw
		req.ContentType = ct
	} else if err := attachJSONBody(f, cobraCmd, rt, &req); err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}

	// Pagination loop (--page-all). Only for operations declaring pagination.
	if rt.pageAll != nil && *rt.pageAll && (f.Globals == nil || !f.Globals.DryRun) {
		return runPaginated(ctx, f, rt, &req)
	}

	return emitOnce(ctx, f, rt, &req)
}

// attachJSONBody resolves the JSON body and applies the operation's declared
// pre-send body checks. Kept separate from runOperation so the request-assembly
// flow reads as one sequence of steps.
func attachJSONBody(f *cmdutil.Factory, cobraCmd *cobra.Command, rt *operationRuntime, req *client.Request) error {
	body, err := resolveBody(f, cobraCmd, rt)
	if err != nil {
		return err
	}
	// Reject index-less / malformed-index whiteboard elements locally, before
	// sending, for operations that declare it in the spec (docs.scene.edit).
	if rt.detail.ValidateElementsIndex {
		if verr := validateElementsIndex(body); verr != nil {
			return verr
		}
	}
	req.Body = body
	return nil
}

// resolvePathValues fills one value per path parameter, in path order. A slot
// whose spec parameter declares x-octo-flag takes the flag's value when the
// caller set it; every other slot consumes the next positional argument.
//
// Operations with no flagged path param keep cobra.ExactArgs, so this is a
// straight copy of args for them.
func resolvePathValues(cobraCmd *cobra.Command, rt *operationRuntime, args []string) ([]string, *output.ExitError) {
	if len(rt.pathFlags) == 0 {
		return args, nil
	}
	byParam := make(map[string]*pathFlag, len(rt.pathFlags))
	for _, pf := range rt.pathFlags {
		byParam[pf.paramName] = pf
	}

	values := make([]string, 0, len(rt.pathParams))
	next := 0
	for _, name := range rt.pathParams {
		if pf := byParam[name]; pf != nil && cobraCmd.Flags().Changed(pf.flagName) {
			// An empty flag value would address the collection URL instead of a
			// resource — `--share-id "$SHARE_ID"` with an unset variable would
			// DELETE /shares/ rather than fail. cobra used to catch the equivalent
			// as a missing positional; MaximumNArgs no longer does.
			if *pf.strVal == "" {
				return nil, output.ErrValidation(
					fmt.Sprintf("--%s is empty", pf.flagName),
					"pass the id value; an empty flag would address the collection, not a resource")
			}
			values = append(values, *pf.strVal)
			continue
		}
		if next < len(args) {
			values = append(values, args[next])
			next++
			continue
		}
		return nil, missingPathValueError(cobraCmd, name, byParam[name])
	}
	if next < len(args) {
		return nil, output.ErrValidation(
			fmt.Sprintf("unexpected extra argument %q", args[next]),
			"a path value supplied by flag must not also be given positionally")
	}
	return values, nil
}

// missingPathValueError names both ways to supply the value, so a caller who
// hit the leading-dash trap is told the flag form exists.
func missingPathValueError(cobraCmd *cobra.Command, paramName string, pf *pathFlag) *output.ExitError {
	arg := "<" + strings.ReplaceAll(paramName, "_", "-") + ">"
	if pf == nil {
		return output.ErrValidation(fmt.Sprintf("missing required argument %s", arg),
			fmt.Sprintf("see `%s --help`", cobraCmd.CommandPath()))
	}
	return output.ErrValidation(
		fmt.Sprintf("missing required argument %s (or --%s)", arg, pf.flagName),
		fmt.Sprintf("%s --%s <value>; an id starting with \"-\" needs the flag form "+
			"or a \"--\" separator: %s -- -Ab3cD…",
			cobraCmd.CommandPath(), pf.flagName, cobraCmd.CommandPath()))
}

// validatePathArgs range-checks every resolved path value whose spec parameter
// is a backend uint64 id (integer / format uint64). Non-id path params are left
// alone, so this is a no-op for every operation that does not declare the format.
func validatePathArgs(rt *operationRuntime, values []string) *output.ExitError {
	for i, name := range rt.pathParams {
		if i >= len(values) {
			return nil
		}
		p := findParam(rt.detail, name, "path")
		if p == nil || p.Format != uint64Format {
			continue
		}
		if _, err := output.ParseUint64Decimal("<"+strings.ReplaceAll(name, "_", "-")+">", values[i]); err != nil {
			return err
		}
	}
	return nil
}

// collectSecrets gathers the literal values of every x-octo-secret parameter or
// body field the user supplied, so the transport can mask them in verbose and
// dry-run output. Returns nil when the operation declares no secrets.
func collectSecrets(cobraCmd *cobra.Command, rt *operationRuntime, pathValues []string) []string {
	var secrets []string
	add := func(v string) {
		if v != "" {
			secrets = append(secrets, v)
		}
	}
	for i, name := range rt.pathParams {
		if i >= len(pathValues) {
			break
		}
		if p := findParam(rt.detail, name, "path"); p != nil && p.Secret {
			add(pathValues[i])
		}
	}
	for flagName, qf := range rt.queryFlags {
		if qf.secret {
			add(changedFlagValue(cobraCmd, flagName, qf.strVal))
		}
	}
	for flagName, hf := range rt.headerFlags {
		if hf.secret {
			add(changedFlagValue(cobraCmd, flagName, hf.strVal))
		}
	}
	for flagName, bf := range rt.bodyFlags {
		if bf.secret {
			add(changedFlagValue(cobraCmd, flagName, bf.strVal))
		}
	}
	return secrets
}

// changedFlagValue returns a string flag's value only if the user set it and the
// flag is string-backed, else "". Secrets are always string-valued, so a
// non-string binding is simply not a secret to mask.
func changedFlagValue(cobraCmd *cobra.Command, flagName string, val *string) string {
	if val == nil || !cobraCmd.Flags().Changed(flagName) {
		return ""
	}
	return *val
}

// findParam locates a spec parameter by wire name and location.
func findParam(d *registry.OperationDetail, name, in string) *registry.ParamInfo {
	if d == nil {
		return nil
	}
	for i := range d.Parameters {
		if d.Parameters[i].Name == name && d.Parameters[i].In == in {
			return &d.Parameters[i]
		}
	}
	return nil
}

// marketplacePath lets local development bypass the production /market
// gateway mount. When OCTO_MARKETPLACE_API_PREFIX is present, its value
// replaces the leading /market segment; setting it to an empty string maps
// /market/api/v1/... to the standalone service's /api/v1/... routes.
func marketplacePath(service, path string) string {
	if service != "marketplace" || !strings.HasPrefix(path, "/market/") {
		return path
	}
	prefix, ok := os.LookupEnv("OCTO_MARKETPLACE_API_PREFIX")
	if !ok {
		return path
	}
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	return strings.TrimSuffix(prefix, "/") + strings.TrimPrefix(path, "/market")
}

// buildQuery assembles the URL query from flags the user explicitly set, so
// omitted flags don't override backend defaults. uint64 id params are validated
// here and emitted as the same decimal text the caller passed, and any
// spec-declared enum is enforced before the request leaves — query and body
// enums are checked at the same strictness so the two do not diverge.
func buildQuery(cobraCmd *cobra.Command, rt *operationRuntime) (url.Values, *output.ExitError) {
	q := url.Values{}
	for flagName, qf := range rt.queryFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		enum := queryEnum(rt.detail, qf.apiName)
		label := "--" + flagName
		switch qf.kind {
		case kindInt:
			if err := checkEnum(label, *qf.intVal, enum); err != nil {
				return nil, err
			}
			q.Set(qf.apiName, strconv.Itoa(*qf.intVal))
		case kindBool:
			if err := checkEnum(label, *qf.boolVal, enum); err != nil {
				return nil, err
			}
			q.Set(qf.apiName, strconv.FormatBool(*qf.boolVal))
		case kindStringSlice:
			// A repeated flag's enum constrains each element, matching how the
			// spec declares it (schema.items.enum).
			itemEnum := queryItemEnum(rt.detail, qf.apiName)
			for _, v := range *qf.strSlc {
				if err := checkEnum(label, v, itemEnum); err != nil {
					return nil, err
				}
				q.Add(qf.apiName, v)
			}
		case kindUint64:
			n, err := output.ParseUint64Decimal(label, *qf.strVal)
			if err != nil {
				return nil, err
			}
			if err := checkEnum(label, n, enum); err != nil {
				return nil, err
			}
			q.Set(qf.apiName, *qf.strVal)
		default:
			if err := checkEnum(label, *qf.strVal, enum); err != nil {
				return nil, err
			}
			q.Set(qf.apiName, *qf.strVal)
		}
	}
	return q, nil
}

// queryEnum returns the enum declared on a query parameter's own schema, or nil.
func queryEnum(d *registry.OperationDetail, apiName string) []any {
	if p := findParam(d, apiName, "query"); p != nil {
		return p.Enum
	}
	return nil
}

// queryItemEnum returns the enum declared on an array query parameter's item
// schema, or nil. Array parameters constrain elements, not the whole list.
func queryItemEnum(d *registry.OperationDetail, apiName string) []any {
	if p := findParam(d, apiName, "query"); p != nil && p.Items != nil {
		return p.Items.Enum
	}
	return nil
}

// buildHeaders assembles the per-request headers from spec-declared header
// flags the user explicitly set. Returns nil when none were set, so an omitted
// optional header stays absent from the wire.
func buildHeaders(cobraCmd *cobra.Command, rt *operationRuntime) map[string]string {
	var headers map[string]string
	for flagName, hf := range rt.headerFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		if headers == nil {
			headers = map[string]string{}
		}
		headers[hf.apiName] = *hf.strVal
	}
	return headers
}

// resolveBody constructs the JSON body. Empty when the op has neither --data
// nor any promoted fields. Explicit flags override --data fields.
//
// After merging --data with promoted flag values, this checks the resolved
// map against the operation's declared `required` list from the spec. Any
// required field that neither --data nor a flag supplied fails locally with
// a validation error rather than being forwarded as a partial write. This is
// the enforcement layer for both promoted primitives and object/array fields;
// registerBodyFlags only marks required fields in help text. Nested object and
// array requirements are validated recursively as well.
func resolveBody(f *cmdutil.Factory, cobraCmd *cobra.Command, rt *operationRuntime) (any, error) {
	if rt.bodyData == nil && len(rt.bodyFlags) == 0 {
		return nil, nil
	}

	base := map[string]any{}

	if rt.bodyData != nil && *rt.bodyData != "" {
		raw, err := cmdutil.ParseInput(f, *rt.bodyData)
		if err != nil {
			return nil, output.ErrValidation(fmt.Sprintf("--data: %v", err), "pass inline JSON, @file, or @-")
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &base); err != nil {
				return nil, output.ErrValidation(fmt.Sprintf("--data is not a JSON object: %v", err), "expected a JSON object for this operation")
			}
		}
	}

	if err := applyBodyFlags(cobraCmd, rt, base); err != nil {
		return nil, err
	}

	if err := validateRequiredBodyFields(rt, base); err != nil {
		return nil, err
	}

	if len(base) == 0 {
		return nil, nil
	}
	return base, nil
}

// applyBodyFlags merges body-flag values (--foo, --bar) into base, following the
// promotable-primitive kinds registered on the operation. Only Changed flags are
// applied so unset flags do not overwrite --data-supplied values with zero.
// uint64 id flags are validated and written as json.Number so they marshal as a
// bare JSON integer — the wire contract stays integer while the flag is text.
func applyBodyFlags(cobraCmd *cobra.Command, rt *operationRuntime, base map[string]any) *output.ExitError {
	for flagName, bf := range rt.bodyFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		switch bf.kind {
		case kindInt:
			base[bf.apiName] = *bf.intVal
		case kindBool:
			base[bf.apiName] = *bf.boolVal
		case kindStringSlice:
			base[bf.apiName] = *bf.strSlc
		case kindUint64:
			n, err := output.ParseUint64Decimal("--"+flagName, *bf.strVal)
			if err != nil {
				return err
			}
			base[bf.apiName] = output.Uint64JSONNumber(n)
		default:
			base[bf.apiName] = *bf.strVal
		}
	}
	return nil
}

// validateRequiredBodyFields enforces the request schema against the merged
// body. It runs before the empty-body short-circuit so schema-required fields
// are checked even when no body values were supplied. Optional request bodies
// remain optional, but if supplied their nested required fields, minItems
// constraints and enum vocabularies still apply.
func validateRequiredBodyFields(rt *operationRuntime, base map[string]any) error {
	if rt.detail == nil || rt.detail.RequestBody == nil {
		return nil
	}
	// Multipart bodies are assembled separately from base. Their required
	// binary part is validated by buildMultipartBody.
	if rt.detail.Multipart {
		return nil
	}
	if len(base) == 0 && !rt.detail.RequestBodyRequired {
		return nil
	}
	v := bodySchemaValidator{flagFor: topLevelBodyFlagNames(rt)}
	if exitErr := v.validate(rt.detail.RequestBody, base, "", ""); exitErr != nil {
		return exitErr
	}
	return nil
}

// topLevelBodyFlagNames inverts the operation's body-flag table into
// wire-field → flag-name, so a validation error on a promoted field can name
// the flag the caller typed instead of the JSON key behind it.
func topLevelBodyFlagNames(rt *operationRuntime) map[string]string {
	if len(rt.bodyFlags) == 0 {
		return nil
	}
	out := make(map[string]string, len(rt.bodyFlags))
	for flagName, bf := range rt.bodyFlags {
		out[bf.apiName] = flagName
	}
	return out
}

// bodySchemaValidator walks a resolved request body against the operation's
// request schema, enforcing required fields, minItems and enum vocabularies
// before anything is sent. It covers the merged body — promoted flags AND
// --data — because --data has never been a raw passthrough on this path:
// required-field and minItems checks already applied to it, so an enum check
// belongs at the same layer rather than only on the promoted flags.
type bodySchemaValidator struct {
	flagFor map[string]string // wire field name → promoted flag name (top level only)
}

// validate reports the first violation found, or nil. Enum violations carry
// their own ENUM_NOT_ALLOWED code so an agent can branch on "value outside a
// closed set" without parsing the message; structural violations keep the
// historical VALIDATION_ERROR envelope byte-for-byte.
func (v bodySchemaValidator) validate(schema *registry.SchemaInfo, value any, path, flagName string) *output.ExitError {
	if err := checkEnum(enumFieldLabel(path, flagName), value, schema.Enum); err != nil {
		return err
	}
	switch schema.Type {
	case "object":
		return v.validateObject(schema, value, path)
	case "array":
		return v.validateArray(schema, value, path, flagName)
	}
	return nil
}

func (v bodySchemaValidator) validateObject(schema *registry.SchemaInfo, value any, path string) *output.ExitError {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return schemaError(fmt.Sprintf("field %s must be an object", bodyPath(path)))
	}
	var missing []string
	for _, name := range schema.Required {
		child, exists := obj[name]
		if !exists || child == nil {
			missing = append(missing, joinBodyPath(path, name))
		}
	}
	if len(missing) > 0 {
		return schemaError(fmt.Sprintf("is missing required field(s): %s", strings.Join(missing, ", ")))
	}
	for name := range schema.Properties {
		if child, exists := obj[name]; exists && child != nil {
			childSchema := schema.Properties[name]
			// Only root-level fields have a promoted flag; a nested field of the
			// same name is --data-only and must not borrow that flag's label.
			childFlag := ""
			if path == "" {
				childFlag = v.flagFor[name]
			}
			if err := v.validate(&childSchema, child, joinBodyPath(path, name), childFlag); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v bodySchemaValidator) validateArray(schema *registry.SchemaInfo, value any, path, flagName string) *output.ExitError {
	items, ok := bodyArray(value)
	if !ok {
		return schemaError(fmt.Sprintf("field %s must be an array", bodyPath(path)))
	}
	if schema.MinItems > 0 && len(items) < schema.MinItems {
		return schemaError(fmt.Sprintf("field %s must contain at least %d item(s)", bodyPath(path), schema.MinItems))
	}
	if schema.Items == nil {
		return nil
	}
	// Elements come from the same flag as the array itself (a repeated flag), so
	// they keep its label; the rejected value in the message identifies which one.
	for i, item := range items {
		if err := v.validate(schema.Items, item, fmt.Sprintf("%s[%d]", path, i), flagName); err != nil {
			return err
		}
	}
	return nil
}

// schemaError wraps a structural violation in the historical request-body
// validation envelope.
func schemaError(issue string) *output.ExitError {
	return output.ErrValidation(
		fmt.Sprintf("request body %s", issue),
		"pass values that satisfy the operation schema via --data JSON or matching body flags")
}

func bodyArray(value any) ([]any, bool) {
	switch items := value.(type) {
	case []any:
		return items, true
	case []string:
		out := make([]any, len(items))
		for i := range items {
			out[i] = items[i]
		}
		return out, true
	default:
		return nil, false
	}
}

func joinBodyPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func bodyPath(path string) string {
	if path == "" {
		return "<root>"
	}
	return path
}

// emitOnce runs one request and emits the envelope. Returns the same error
// value so cobra sets a non-zero exit code.
func emitOnce(ctx context.Context, f *cmdutil.Factory, rt *operationRuntime, req *client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	body, err := cli.Do(ctx, req)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	body, err = normalizeResponse(f, rt, body)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	return f.EmitSuccess(body)
}

// normalizeResponse applies the operation's spec-declared output transforms
// (x-octo-response-fields, x-octo-lossless-id-fields). It is skipped under
// --dry-run, where the body is the CLI's own request description rather than a
// backend DTO, and is a no-op for operations declaring neither extension.
func normalizeResponse(f *cmdutil.Factory, rt *operationRuntime, body []byte) ([]byte, error) {
	if rt == nil || rt.detail == nil {
		return body, nil
	}
	if f.Globals != nil && f.Globals.DryRun {
		return body, nil
	}
	out, err := output.NormalizeResponse(body, rt.detail.ResponseFieldAliases, rt.detail.LosslessIDFields)
	if err != nil {
		return nil, output.ErrWithHint("internal", "RESPONSE_NORMALIZE", err.Error(), "report the unexpected response shape")
	}
	return out, nil
}

// --- pagination ---

// runPaginated walks pages until has_more is false, --page-limit is hit, or
// the context is cancelled. The merged result is a flat array of all data
// items — the caller gets a single envelope with no _pagination block
// (architecture §4.4).
func runPaginated(ctx context.Context, f *cmdutil.Factory, rt *operationRuntime, firstReq *client.Request) error {
	// Pagination and binary output-to-disk are mutually exclusive. The loop
	// reuses OutputPath for every page, so an operation that was both paginated
	// AND declared an inline binary body would write each page to the same file,
	// leaving only the last page's bytes. No spec op declares both today, so this
	// never fires in practice; it is a fail-loud guard against a future spec that
	// wires the two together and would otherwise silently corrupt --output.
	if err := validatePaginationRequest(firstReq); err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}

	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	pag := rt.detail.Pagination
	cursorParam, limit := paginationControls(rt, pag)
	merged := make([]json.RawMessage, 0, 64)
	req := *firstReq
	seenCursors := paginationSeenCursors(&req, rt, pag, cursorParam)
	for page := 0; page < limit; page++ {
		body, err := cli.Do(ctx, &req)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		data, nextCursor, hasMore, perr := parsePage(body, pag)
		if perr != nil {
			_ = f.EmitError(perr) //nolint:errcheck // best-effort emit before returning err
			return perr
		}
		merged = append(merged, data...)
		if !hasMore || nextCursor == "" || page+1 >= limit {
			break
		}
		if cursorWasSeen(seenCursors, nextCursor) {
			loopErr := paginationLoopError(nextCursor)
			_ = f.EmitError(loopErr) //nolint:errcheck // best-effort emit before returning err
			return loopErr
		}
		if seenCursors != nil {
			seenCursors[nextCursor] = struct{}{}
		}
		req = requestWithCursor(&req, rt, cursorParam, nextCursor)
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return f.EmitSuccess(out)
}

func paginationControls(rt *operationRuntime, pag *registry.PaginationInfo) (cursorParam string, limit int) {
	cursorParam = pag.CursorParam
	if cursorParam == "" {
		cursorParam = "cursor"
	}
	limit = 10
	if rt.pageLimit != nil && *rt.pageLimit > 0 {
		limit = *rt.pageLimit
	}
	return cursorParam, limit
}

func paginationSeenCursors(req *client.Request, rt *operationRuntime, pag *registry.PaginationInfo, cursorParam string) map[string]struct{} {
	if !pag.RejectCursorRepeats {
		return nil
	}
	return initialSeenCursors(req, rt, cursorParam)
}

func validatePaginationRequest(req *client.Request) *output.ExitError {
	if req.OutputPath == "" {
		return nil
	}
	return output.ErrWithHint(
		"internal",
		"PAGINATION_OUTPUT_CONFLICT",
		"operation is paginated and also writes a binary body to --output; these are mutually exclusive",
		"operation-spec bug: an op with x-octo-pagination must not also declare an inline 2xx binary body",
	)
}

func cursorWasSeen(seen map[string]struct{}, cursor string) bool {
	if seen == nil || cursor == "" {
		return false
	}
	_, ok := seen[cursor]
	return ok
}

func paginationLoopError(cursor string) *output.ExitError {
	return output.ErrWithHint(
		"internal",
		"PAGINATION_LOOP",
		fmt.Sprintf("backend repeated pagination cursor %q", cursor),
		"retry without --page-all or report the backend cursor loop",
	)
}

// requestWithCursor copies the whole request so no field is silently dropped.
// It clones the query/body container before setting the cursor, leaving the
// previous page's request unchanged.
func requestWithCursor(req *client.Request, rt *operationRuntime, cursorParam, cursor string) client.Request {
	next := *req
	if cursorIsQueryParam(rt, cursorParam) {
		nextQ := url.Values{}
		for key, values := range req.Query {
			nextQ[key] = append([]string(nil), values...)
		}
		nextQ.Set(cursorParam, cursor)
		next.Query = nextQ
	} else {
		next.Body = withCursorBody(req.Body, cursorParam, cursor)
	}
	return next
}

// cursorIsQueryParam reports whether this operation declares its pagination
// cursor as a URL query parameter (GET endpoints, e.g. matter.list) rather than
// a JSON body field (POST endpoints, e.g. the message.search family). The spec
// binds a query-declared cursor to a queryFlag whose apiName is the cursor
// param; a body-declared cursor has no such binding.
func cursorIsQueryParam(rt *operationRuntime, cursorParam string) bool {
	for _, qf := range rt.queryFlags {
		if qf.apiName == cursorParam {
			return true
		}
	}
	return false
}

// initialSeenCursors seeds the repeated-cursor guard with the cursor already
// carried by the first request. This prevents --cursor C --page-all from
// requesting C twice when a backend incorrectly returns C as its own next cursor.
func initialSeenCursors(req *client.Request, rt *operationRuntime, cursorParam string) map[string]struct{} {
	seen := map[string]struct{}{}
	if cursor := requestCursor(req, rt, cursorParam); cursor != "" {
		seen[cursor] = struct{}{}
	}
	return seen
}

func requestCursor(req *client.Request, rt *operationRuntime, cursorParam string) string {
	if cursorIsQueryParam(rt, cursorParam) {
		return req.Query.Get(cursorParam)
	}
	if body, ok := req.Body.(map[string]any); ok {
		if cursor, ok := body[cursorParam].(string); ok {
			return cursor
		}
	}
	return ""
}

// withCursorBody returns a shallow clone of the JSON body map with the cursor
// key set to nextCursor, so paging never mutates the previous page's body. A nil
// or non-map body yields a fresh single-key map (search bodies are always
// map[string]any from resolveBody).
func withCursorBody(body any, cursorParam, nextCursor string) map[string]any {
	next := map[string]any{}
	if m, ok := body.(map[string]any); ok {
		for k, v := range m {
			next[k] = v
		}
	}
	next[cursorParam] = nextCursor
	return next
}

// parsePage extracts pagination fields using x-octo-pagination paths. Defaults
// preserve the historical {data, pagination:{has_more,next_cursor}} contract.
// Specs without a has-more field must explicitly opt into cursor inference.
func parsePage(body []byte, pag *registry.PaginationInfo) (items []json.RawMessage, cursor string, hasMore bool, exitErr *output.ExitError) {
	if len(body) == 0 {
		return nil, "", false, nil
	}
	fields := paginationFieldsFor(pag)
	items, err := parsePageItems(body, fields.items)
	if err != nil {
		return nil, "", false, paginationParseError(err)
	}
	cursor, err = parsePageCursor(body, fields.cursor)
	if err != nil {
		return nil, "", false, paginationParseError(err)
	}
	if fields.inferHasMore {
		return items, cursor, cursor != "", nil
	}
	hasMore, err = parsePageHasMore(body, fields.hasMore)
	if err != nil {
		return nil, "", false, paginationParseError(err)
	}
	return items, cursor, hasMore, nil
}

type paginationFields struct {
	items        string
	cursor       string
	hasMore      string
	inferHasMore bool
}

func paginationFieldsFor(pag *registry.PaginationInfo) paginationFields {
	fields := paginationFields{items: "data", cursor: "pagination.next_cursor", hasMore: "pagination.has_more"}
	if pag == nil {
		return fields
	}
	if pag.ItemsField != "" {
		fields.items = pag.ItemsField
	}
	if pag.CursorField != "" {
		fields.cursor = pag.CursorField
	}
	if pag.HasMoreField != "" {
		fields.hasMore = pag.HasMoreField
	}
	fields.inferHasMore = pag.InferHasMore
	return fields
}

func parsePageItems(body []byte, path string) ([]json.RawMessage, error) {
	raw, found, err := rawValueAtPath(body, path)
	if err != nil || !found {
		return nil, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("field %q must be an array: %w", path, err)
	}
	return items, nil
}

func parsePageCursor(body []byte, path string) (string, error) {
	raw, found, err := rawValueAtPath(body, path)
	if err != nil || !found || string(raw) == "null" {
		return "", err
	}
	var cursor string
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return "", fmt.Errorf("field %q must be a string or null: %w", path, err)
	}
	return cursor, nil
}

func parsePageHasMore(body []byte, path string) (bool, error) {
	raw, found, err := rawValueAtPath(body, path)
	if err != nil || !found || string(raw) == "null" {
		return false, err
	}
	var hasMore bool
	if err := json.Unmarshal(raw, &hasMore); err != nil {
		return false, fmt.Errorf("field %q must be a boolean or null: %w", path, err)
	}
	return hasMore, nil
}

func paginationParseError(err error) *output.ExitError {
	return output.ErrWithHint("internal", "PAGINATION_PARSE", err.Error(), "response did not match expected pagination shape")
}

// rawValueAtPath resolves dot-separated object keys while retaining the exact
// JSON bytes of the selected value. Literal dots in response keys are not
// supported by x-octo-pagination paths.
func rawValueAtPath(body []byte, path string) (json.RawMessage, bool, error) {
	if path == "" {
		return nil, false, nil
	}
	current := json.RawMessage(body)
	for _, part := range strings.Split(path, ".") {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(current, &object); err != nil {
			return nil, false, fmt.Errorf("field path %q traverses a non-object: %w", path, err)
		}
		next, ok := object[part]
		if !ok {
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}
