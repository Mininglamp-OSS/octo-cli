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
	for i, pname := range rt.pathParams {
		placeholder := "{" + pname + "}"
		urlPath = strings.ReplaceAll(urlPath, placeholder, url.PathEscape(args[i]))
	}

	// Query parameters (only emit flags explicitly set by the user so defaults
	// don't override backend defaults).
	q := buildQuery(cobraCmd, rt)

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
		ResponseUnwrap:      d.ResponseUnwrap,
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
	} else {
		body, err := resolveBody(f, cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		// Reject index-less / malformed-index whiteboard elements locally, before
		// sending, for operations that declare it in the spec (docs.scene.edit).
		if d.ValidateElementsIndex {
			if verr := validateElementsIndex(body); verr != nil {
				_ = f.EmitError(verr) //nolint:errcheck // best-effort emit before returning err
				return verr
			}
		}
		req.Body = body
	}

	// Pagination loop (--page-all). Only for operations declaring pagination.
	if rt.pageAll != nil && *rt.pageAll && (f.Globals == nil || !f.Globals.DryRun) {
		return runPaginated(ctx, f, rt, &req)
	}

	return emitOnce(ctx, f, &req)
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
// omitted flags don't override backend defaults.
func buildQuery(cobraCmd *cobra.Command, rt *operationRuntime) url.Values {
	q := url.Values{}
	for flagName, qf := range rt.queryFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		switch qf.kind {
		case kindInt:
			q.Set(qf.apiName, strconv.Itoa(*qf.intVal))
		case kindBool:
			q.Set(qf.apiName, strconv.FormatBool(*qf.boolVal))
		case kindStringSlice:
			for _, v := range *qf.strSlc {
				q.Add(qf.apiName, v)
			}
		default:
			q.Set(qf.apiName, *qf.strVal)
		}
	}
	return q
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

	applyBodyFlags(cobraCmd, rt, base)

	if err := validateRequiredBodyFields(rt, base); err != nil {
		return nil, err
	}
	if err := validateBodyVariants(rt, base); err != nil {
		return nil, err
	}

	if len(base) == 0 {
		return nil, nil
	}
	return base, nil
}

func validateBodyVariants(rt *operationRuntime, body map[string]any) error {
	if rt.detail == nil || len(rt.detail.BodyVariants) == 0 {
		return nil
	}
	for _, variant := range rt.detail.BodyVariants {
		valid := true
		for _, name := range variant.Required {
			if value, exists := body[name]; !exists || value == nil {
				valid = false
			}
		}
		for _, name := range variant.Forbidden {
			if value, exists := body[name]; exists && value != nil {
				valid = false
			}
		}
		if valid {
			return nil
		}
	}
	return output.ErrValidation("request body does not match an allowed operation mode", "create without slug and with idempotency_key, or republish an existing slug without idempotency_key")
}

// applyBodyFlags merges body-flag values (--foo, --bar) into base, following the
// promotable-primitive kinds registered on the operation. Only Changed flags are
// applied so unset flags do not overwrite --data-supplied values with zero.
func applyBodyFlags(cobraCmd *cobra.Command, rt *operationRuntime, base map[string]any) {
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
		default:
			base[bf.apiName] = *bf.strVal
		}
	}
}

// validateRequiredBodyFields enforces the request schema against the merged
// body. It runs before the empty-body short-circuit so schema-required fields
// are checked even when no body values were supplied. Optional request bodies
// remain optional, but if supplied their nested required fields and minItems
// constraints still apply.
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
	if issue := validateBodySchema(rt.detail.RequestBody, base, ""); issue != "" {
		return output.ErrValidation(
			fmt.Sprintf("request body %s", issue),
			"pass values that satisfy the operation schema via --data JSON or matching body flags")
	}
	return nil
}

func validateBodySchema(schema *registry.SchemaInfo, value any, path string) string {
	switch schema.Type {
	case "object":
		return validateObjectSchema(schema, value, path)
	case "array":
		return validateArraySchema(schema, value, path)
	}
	return ""
}

func validateObjectSchema(schema *registry.SchemaInfo, value any, path string) string {
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return fmt.Sprintf("field %s must be an object", bodyPath(path))
	}
	var missing []string
	for _, name := range schema.Required {
		child, exists := obj[name]
		if !exists || child == nil {
			missing = append(missing, joinBodyPath(path, name))
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("is missing required field(s): %s", strings.Join(missing, ", "))
	}
	for name := range schema.Properties {
		if child, exists := obj[name]; exists && child != nil {
			childSchema := schema.Properties[name]
			if issue := validateBodySchema(&childSchema, child, joinBodyPath(path, name)); issue != "" {
				return issue
			}
		}
	}
	return ""
}

func validateArraySchema(schema *registry.SchemaInfo, value any, path string) string {
	items, ok := bodyArray(value)
	if !ok {
		return fmt.Sprintf("field %s must be an array", bodyPath(path))
	}
	if schema.MinItems > 0 && len(items) < schema.MinItems {
		return fmt.Sprintf("field %s must contain at least %d item(s)", bodyPath(path), schema.MinItems)
	}
	if schema.Items != nil {
		for i, item := range items {
			if issue := validateBodySchema(schema.Items, item, fmt.Sprintf("%s[%d]", path, i)); issue != "" {
				return issue
			}
		}
	}
	return ""
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
func emitOnce(ctx context.Context, f *cmdutil.Factory, req *client.Request) error {
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
	if req.ResponseUnwrap != "" && (f.Globals == nil || !f.Globals.DryRun) {
		body, err = unwrapResponse(body, req.ResponseUnwrap)
		if err != nil {
			unwrapErr := output.ErrWithHint("internal", "RESPONSE_UNWRAP", err.Error(), "backend response did not match its operation spec")
			_ = f.EmitError(unwrapErr) //nolint:errcheck // best-effort emit before returning err
			return unwrapErr
		}
	}
	return f.EmitSuccess(body)
}

func unwrapResponse(body []byte, path string) ([]byte, error) {
	raw, found, err := rawValueAtPath(body, path)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("response is missing unwrap field %q", path)
	}
	return raw, nil
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
