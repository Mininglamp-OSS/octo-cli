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
// a validation error rather than being forwarded as a partial write. The
// primitive-required contract (a required *primitive* body property gets a
// cobra MarkFlagRequired in registerBodyFlags) is unchanged; this closes the
// gap left by required *object/array* properties like `time_range` and
// `sources`, which cannot be promoted to flags and previously reached the
// backend as an empty or partial payload (see summary.create + octo-cli
// PR #113 review).
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

	if len(base) == 0 {
		return nil, nil
	}
	return base, nil
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

// validateRequiredBodyFields enforces spec-driven RequestBody.Required against
// the merged base map. Object/array required fields are not eligible for flag
// promotion (promotableKind rejects them), so they can slip through as absent
// unless we check here. This must run before the empty-base short-circuit in
// resolveBody: a caller who supplies no --data and no promoted flags on a
// POST/PUT with required body fields is exactly the case we need to catch
// (round-1 review: `octo-cli summary create --idempotency-key k` with no body
// used to reach the backend as an empty write). Primitive requireds are
// double-covered by cobra's MarkFlagRequired plus this check — harmless.
func validateRequiredBodyFields(rt *operationRuntime, base map[string]any) error {
	if rt.detail == nil || rt.detail.RequestBody == nil {
		return nil
	}
	var missing []string
	for _, name := range rt.detail.RequestBody.Required {
		if _, ok := base[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return output.ErrValidation(
			fmt.Sprintf("--data is missing required field(s): %s", strings.Join(missing, ", ")),
			"pass the missing fields via --data JSON (or a matching flag when the field is a primitive)")
	}
	return nil
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
	return f.EmitSuccess(body)
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
	if firstReq.OutputPath != "" {
		err := output.ErrWithHint(
			"internal",
			"PAGINATION_OUTPUT_CONFLICT",
			"operation is paginated and also writes a binary body to --output; these are mutually exclusive",
			"operation-spec bug: an op with x-octo-pagination must not also declare an inline 2xx binary body",
		)
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}

	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	pag := rt.detail.Pagination
	cursorParam := pag.CursorParam
	if cursorParam == "" {
		cursorParam = "cursor"
	}
	limit := 10
	if rt.pageLimit != nil && *rt.pageLimit > 0 {
		limit = *rt.pageLimit
	}

	merged := make([]json.RawMessage, 0, 64)
	req := *firstReq
	for page := 0; page < limit; page++ {
		body, err := cli.Do(ctx, &req)
		if err != nil {
			_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
			return err
		}
		data, nextCursor, hasMore, perr := parsePage(body)
		if perr != nil {
			_ = f.EmitError(perr) //nolint:errcheck // best-effort emit before returning err
			return perr
		}
		merged = append(merged, data...)
		if !hasMore || nextCursor == "" {
			break
		}
		// Prepare next request. Copy the whole struct so no field is silently
		// dropped (RawBody/ContentType/BinaryResponse/OutputPath). The cursor
		// goes wherever the spec declares it: a query parameter (GET endpoints
		// like matter.list) or a JSON body field (POST endpoints like the
		// message.search family). Either way we clone the container before
		// setting so the previous page's request is never mutated.
		next := req
		if cursorIsQueryParam(rt, cursorParam) {
			nextQ := url.Values{}
			for k, vs := range req.Query {
				nextQ[k] = append([]string(nil), vs...)
			}
			nextQ.Set(cursorParam, nextCursor)
			next.Query = nextQ
		} else {
			next.Body = withCursorBody(req.Body, cursorParam, nextCursor)
		}
		req = next
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return f.EmitSuccess(out)
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

// parsePage extracts {data:[], pagination:{has_more, next_cursor}} from a
// backend response. Tolerant: missing fields → empty data, no more pages.
func parsePage(body []byte) (items []json.RawMessage, cursor string, hasMore bool, exitErr *output.ExitError) {
	if len(body) == 0 {
		return nil, "", false, nil
	}
	var page struct {
		Data       []json.RawMessage `json:"data"`
		Pagination struct {
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, "", false, output.ErrWithHint("internal", "PAGINATION_PARSE", err.Error(), "response did not match expected pagination shape")
	}
	return page.Data, page.Pagination.NextCursor, page.Pagination.HasMore, nil
}
