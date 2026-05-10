// Package service provides the metadata-driven command auto-registration
// engine. For every operation in the embedded OpenAPI registry the engine
// generates a cobra command whose flags, body shape, risk gate, and
// pagination behaviour are derived entirely from the spec — so adding a new
// backend endpoint is "update the spec", nothing more.
//
// operationId "domain.verb" or "domain.resource.verb" maps to
// "octo <domain> [<resource>] <verb>" (architecture §5.2). Path parameters
// become positional args; query parameters become typed flags; simple
// top-level body fields are auto-promoted to flags (Rule 5a), with a --data
// escape hatch for complex bodies.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/cmdutil"
	"github.com/dmwork-org/octo-cli/internal/output"
	"github.com/dmwork-org/octo-cli/internal/registry"
)

// RegisterServiceCommands attaches one cobra subtree per service in the
// registry to parent. Call after the Factory and root flags are wired.
func RegisterServiceCommands(parent *cobra.Command, f *cmdutil.Factory) {
	reg := f.Registry()
	if reg == nil {
		return
	}
	for _, svc := range reg.ListServices() {
		svcCmd := ensureServiceCmd(parent, svc)
		for _, info := range reg.ListOperations(svc) {
			detail, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			attachOperation(svcCmd, f, detail)
		}
		// Register matter-specific status aliases.
		if svc == "matter" {
			attachMatterStatusAliases(svcCmd, f)
		}
	}
}

// ensureServiceCmd returns (creating if needed) the top-level command for a
// service. Idempotent so tests can register twice without duplicate adds.
func ensureServiceCmd(parent *cobra.Command, svc string) *cobra.Command {
	if existing := findChild(parent, svc); existing != nil {
		return existing
	}
	c := &cobra.Command{
		Use:   svc,
		Short: fmt.Sprintf("Operations on the %s domain", svc),
	}
	parent.AddCommand(c)
	return c
}

// attachOperation inserts one operation as a leaf command, creating
// intermediate "resource" commands (matter assignee, matter timeline, …) on
// the way down. operationId segments after the first are treated as the path
// to the leaf.
func attachOperation(svcCmd *cobra.Command, f *cmdutil.Factory, d *registry.OperationDetail) {
	segs := strings.Split(d.ID, ".")
	if len(segs) < 2 {
		return
	}
	cur := svcCmd
	for i := 1; i < len(segs)-1; i++ {
		name := segs[i]
		sub := findChild(cur, name)
		if sub == nil {
			sub = &cobra.Command{
				Use:   name,
				Short: fmt.Sprintf("%s %s operations", cur.Use, name),
			}
			cur.AddCommand(sub)
		}
		cur = sub
	}
	leaf := buildOperationCmd(f, d, segs[len(segs)-1])
	cur.AddCommand(leaf)
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// --- operation command construction ---

// operationRuntime holds everything the RunE closure needs to build the
// outbound request from the user's flag values.
type operationRuntime struct {
	detail      *registry.OperationDetail
	pathParams  []string                    // path param names in order
	queryFlags  map[string]*queryFlag       // flag name → query param binding
	bodyFlags   map[string]*bodyFlag        // flag name → body field binding
	bodyData    *string                     // --data (nil when command has no body)
	pageAll     *bool                       // --page-all (nil when no pagination)
	pageLimit   *int                        // --page-limit
	filePath    *string                     // --file (multipart operations only)
	hasRequired bool                        // whether any required flag has been marked
}

type queryFlag struct {
	apiName string // URL query parameter name
	kind    valueKind
	strVal  *string
	intVal  *int
	boolVal *bool
	strSlc  *[]string
}

type bodyFlag struct {
	apiName string // JSON body field name
	kind    valueKind
	strVal  *string
	intVal  *int
	boolVal *bool
	strSlc  *[]string
}

type valueKind int

const (
	kindString valueKind = iota
	kindInt
	kindBool
	kindStringSlice
)

func buildOperationCmd(f *cmdutil.Factory, d *registry.OperationDetail, verb string) *cobra.Command {
	rt := &operationRuntime{
		detail:     d,
		queryFlags: map[string]*queryFlag{},
		bodyFlags:  map[string]*bodyFlag{},
	}

	rt.pathParams = extractPathParams(d.Path)

	usage := verb
	for _, p := range rt.pathParams {
		usage += " <" + strings.ReplaceAll(p, "_", "-") + ">"
	}

	cmd := &cobra.Command{
		Use:     usage,
		Short:   d.Summary,
		Long:    buildLongDesc(d),
		Args:    cobra.ExactArgs(len(rt.pathParams)),
		GroupID: "",
	}

	registerQueryFlags(cmd, rt, d)
	registerBodyFlags(cmd, rt, d)

	if d.Pagination != nil {
		var pageAll bool
		var pageLimit int
		cmd.Flags().BoolVar(&pageAll, "page-all", false, "auto-paginate through all pages")
		cmd.Flags().IntVar(&pageLimit, "page-limit", 10, "maximum number of pages to fetch with --page-all")
		rt.pageAll = &pageAll
		rt.pageLimit = &pageLimit
	}

	cmd.RunE = func(cobraCmd *cobra.Command, args []string) error {
		return runOperation(cobraCmd, f, rt, args)
	}
	return cmd
}

func buildLongDesc(d *registry.OperationDetail) string {
	var b strings.Builder
	if d.Summary != "" {
		b.WriteString(d.Summary)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "operationId: %s\n%s %s", d.ID, d.Method, d.Path)
	if d.Risk != "" {
		fmt.Fprintf(&b, "  [risk: %s]", d.Risk)
	}
	if d.Pagination != nil {
		b.WriteString("\n\nThis operation is paginated. Pass --page-all to walk all pages.")
	}
	return b.String()
}

// --- flag registration ---

func registerQueryFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	for _, p := range d.Parameters {
		if p.In != "query" {
			continue
		}
		flagName := strings.ReplaceAll(p.Name, "_", "-")
		qf := &queryFlag{apiName: p.Name, kind: schemaTypeKind(p.Type)}
		desc := p.Description
		if len(p.Enum) > 0 {
			desc = fmt.Sprintf("%s (one of: %s)", desc, formatEnum(p.Enum))
		}
		switch qf.kind {
		case kindInt:
			qf.intVal = new(int)
			dv := 0
			if n, ok := p.Default.(float64); ok {
				dv = int(n)
			}
			cmd.Flags().IntVar(qf.intVal, flagName, dv, desc)
		case kindBool:
			qf.boolVal = new(bool)
			dv := false
			if b, ok := p.Default.(bool); ok {
				dv = b
			}
			cmd.Flags().BoolVar(qf.boolVal, flagName, dv, desc)
		case kindStringSlice:
			qf.strSlc = new([]string)
			cmd.Flags().StringSliceVar(qf.strSlc, flagName, nil, desc)
		default:
			qf.strVal = new(string)
			dv := ""
			if s, ok := p.Default.(string); ok {
				dv = s
			}
			cmd.Flags().StringVar(qf.strVal, flagName, dv, desc)
		}
		rt.queryFlags[flagName] = qf
		if p.Required {
			_ = cmd.MarkFlagRequired(flagName)
		}
	}
}

func registerBodyFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	body := d.RequestBody
	if body == nil {
		return
	}

	// Multipart operations: register --file for the binary upload and skip
	// --data (JSON body doesn't apply). Non-binary body fields still register
	// as flags below so they can go through as form text fields.
	if d.Multipart {
		filePath := new(string)
		cmd.Flags().StringVar(filePath, "file", "", "path to the file to upload (required)")
		_ = cmd.MarkFlagRequired("file")
		rt.filePath = filePath
	} else {
		// Every non-multipart command with a body gets --data, even when we
		// also promote simple fields. Individual flags override the JSON blob
		// (architecture §5.2).
		data := new(string)
		cmd.Flags().StringVar(data, "data", "", "JSON request body (string, @file, or @- for stdin). Individual flags override.")
		rt.bodyData = data
	}

	if body.Properties == nil {
		return
	}
	required := map[string]bool{}
	for _, r := range body.Required {
		required[r] = true
	}
	// Deterministic registration order for predictable --help.
	names := make([]string, 0, len(body.Properties))
	for k := range body.Properties {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		prop := body.Properties[name]
		// Skip binary fields — those are handled by --file in multipart mode.
		if prop.Format == "binary" {
			continue
		}
		kind, ok := promotableKind(prop)
		if !ok {
			continue
		}
		flagName := strings.ReplaceAll(name, "_", "-")
		// Avoid collisions with --data, --file or a query param of the same name.
		if flagName == "data" || flagName == "file" || rt.queryFlags[flagName] != nil {
			continue
		}
		desc := prop.Description
		if len(prop.Enum) > 0 {
			desc = fmt.Sprintf("%s (one of: %s)", desc, formatEnum(prop.Enum))
		}
		if required[name] {
			desc = strings.TrimSpace(desc + " (required)")
		}
		bf := &bodyFlag{apiName: name, kind: kind}
		switch kind {
		case kindInt:
			bf.intVal = new(int)
			cmd.Flags().IntVar(bf.intVal, flagName, 0, desc)
		case kindBool:
			bf.boolVal = new(bool)
			cmd.Flags().BoolVar(bf.boolVal, flagName, false, desc)
		case kindStringSlice:
			bf.strSlc = new([]string)
			cmd.Flags().StringSliceVar(bf.strSlc, flagName, nil, desc)
		default:
			bf.strVal = new(string)
			cmd.Flags().StringVar(bf.strVal, flagName, "", desc)
		}
		rt.bodyFlags[flagName] = bf
	}
}

// promotableKind returns the primitive flag kind a body property maps to,
// or (0,false) if the property is complex (object, array-of-object, etc.)
// and must go through --data. Enums inherit their base type.
func promotableKind(p registry.SchemaInfo) (valueKind, bool) {
	switch p.Type {
	case "string":
		return kindString, true
	case "integer", "number":
		return kindInt, true
	case "boolean":
		return kindBool, true
	case "array":
		if p.Items != nil && p.Items.Type == "string" {
			return kindStringSlice, true
		}
	}
	return 0, false
}

func schemaTypeKind(t string) valueKind {
	switch t {
	case "integer", "number":
		return kindInt
	case "boolean":
		return kindBool
	}
	return kindString
}

func formatEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return strings.Join(parts, ", ")
}

// --- RunE ---

func runOperation(cobraCmd *cobra.Command, f *cmdutil.Factory, rt *operationRuntime, args []string) error {
	ctx := cobraCmd.Context()
	d := rt.detail


	// Path substitution. cobra.ExactArgs already ensured count.
	urlPath := d.Path
	for i, pname := range rt.pathParams {
		placeholder := "{" + pname + "}"
		urlPath = strings.ReplaceAll(urlPath, placeholder, url.PathEscape(args[i]))
	}

	// Query parameters (only emit flags explicitly set by the user so defaults
	// don't override backend defaults).
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

	// Body: start from --data (if any), then merge explicit flags on top.
	// Multipart ops take a separate path — they build a form body, not JSON.
	req := client.Request{
		Service:        serviceForBaseURL(d.BaseURLEnv),
		Method:         d.Method,
		Path:           urlPath,
		Query:          q,
		BinaryResponse: d.BinaryResponse,
	}
	if d.Multipart {
		raw, ct, err := buildMultipartBody(cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err)
			return err
		}
		req.RawBody = raw
		req.ContentType = ct
	} else {
		body, err := resolveBody(f, cobraCmd, rt)
		if err != nil {
			_ = f.EmitError(err)
			return err
		}
		req.Body = body
	}

	// Pagination loop (--page-all). Only for operations declaring pagination.
	if rt.pageAll != nil && *rt.pageAll && !(f.Globals != nil && f.Globals.DryRun) {
		return runPaginated(ctx, f, rt, req)
	}

	return emitOnce(ctx, f, req)
}

// buildMultipartBody assembles a multipart/form-data payload for operations
// tagged x-octo-multipart. The binary upload is read from --file and attached
// under the "file" form field (backend uses FormFile("file")). Any promoted
// body flags the user set are included as form text fields.
func buildMultipartBody(cobraCmd *cobra.Command, rt *operationRuntime) ([]byte, string, error) {
	if rt.filePath == nil || *rt.filePath == "" {
		return nil, "", output.ErrValidation("--file is required for multipart upload", "pass --file <path>")
	}
	path := *rt.filePath
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}

	// Any promoted body fields the user set become form text fields.
	for flagName, bf := range rt.bodyFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		var value string
		switch bf.kind {
		case kindInt:
			value = strconv.Itoa(*bf.intVal)
		case kindBool:
			value = strconv.FormatBool(*bf.boolVal)
		case kindStringSlice:
			// Flatten slice: one form field per value.
			for _, v := range *bf.strSlc {
				if err := w.WriteField(bf.apiName, v); err != nil {
					return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
				}
			}
			continue
		default:
			value = *bf.strVal
		}
		if err := w.WriteField(bf.apiName, value); err != nil {
			return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}

// resolveBody constructs the JSON body. Empty when the op has neither --data
// nor any promoted fields. Explicit flags override --data fields.
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

	if len(base) == 0 {
		return nil, nil
	}
	return base, nil
}

// emitOnce runs one request and emits the envelope. Returns the same error
// value so cobra sets a non-zero exit code.
func emitOnce(ctx context.Context, f *cmdutil.Factory, req client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err)
		return err
	}
	body, err := cli.Do(ctx, req)
	if err != nil {
		_ = f.EmitError(err)
		return err
	}
	return f.EmitSuccess(body)
}

// --- pagination ---

// runPaginated walks pages until has_more is false, --page-limit is hit, or
// the context is cancelled. The merged result is a flat array of all data
// items — the caller gets a single envelope with no _pagination block
// (architecture §4.4).
func runPaginated(ctx context.Context, f *cmdutil.Factory, rt *operationRuntime, firstReq client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err)
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
	req := firstReq
	for page := 0; page < limit; page++ {
		body, err := cli.Do(ctx, req)
		if err != nil {
			_ = f.EmitError(err)
			return err
		}
		data, nextCursor, hasMore, perr := parsePage(body)
		if perr != nil {
			_ = f.EmitError(perr)
			return perr
		}
		merged = append(merged, data...)
		if !hasMore || nextCursor == "" {
			break
		}
		// Prepare next request. Clone Query so we don't mutate the previous.
		nextQ := url.Values{}
		for k, vs := range req.Query {
			nextQ[k] = append([]string(nil), vs...)
		}
		nextQ.Set(cursorParam, nextCursor)
		req = client.Request{
			Service: req.Service,
			Method:  req.Method,
			Path:    req.Path,
			Query:   nextQ,
			Body:    req.Body,
			Headers: req.Headers,
		}
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return f.EmitSuccess(out)
}

// parsePage extracts {data:[], pagination:{has_more, next_cursor}} from a
// backend response. Tolerant: missing fields → empty data, no more pages.
func parsePage(body []byte) ([]json.RawMessage, string, bool, *output.ExitError) {
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

// --- helpers ---

// extractPathParams returns the names of {placeholder} segments in path
// order. Unknown chars inside braces are passed through.
func extractPathParams(path string) []string {
	var out []string
	for {
		start := strings.IndexByte(path, '{')
		if start < 0 {
			return out
		}
		end := strings.IndexByte(path[start:], '}')
		if end < 0 {
			return out
		}
		end += start
		out = append(out, path[start+1:end])
		path = path[end+1:]
	}
}

// serviceForBaseURL maps the spec's x-octo-base-url env-var name to the
// config-level service key used by client.Request.Service. Unknown envs fall
// back to the default (OCTO_API_URL) so a new backend can appear with just a
// new spec.
func serviceForBaseURL(envVar string) string {
	switch envVar {
	case "OCTO_MATTERS_URL":
		return "matters"
	case "OCTO_DMWORKIM_URL":
		return "dmworkim"
	}
	return ""
}
