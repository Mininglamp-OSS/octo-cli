package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// operationRuntime holds everything the RunE closure needs to build the
// outbound request from the user's flag values.
type operationRuntime struct {
	detail        *registry.OperationDetail
	pathParams    []string               // path param names in order
	pathFlags     []*pathFlag            // optional flag alternatives to positional args
	pathFlagNames map[string]bool        // flag names claimed by pathFlags
	queryFlags    map[string]*queryFlag  // flag name → query param binding
	headerFlags   map[string]*headerFlag // flag name → header param binding
	bodyFlags     map[string]*bodyFlag   // flag name → body field binding
	bodyData      *string                // --data (nil when command has no body)
	pageAll       *bool                  // --page-all (nil when no pagination)
	pageLimit     *int                   // --page-limit
	filePath      *string                // --file (multipart operations only)
	outputPath    *string                // --output/-o (binary-response operations only)
}

type queryFlag struct {
	apiName string // URL query parameter name
	kind    valueKind
	secret  bool // x-octo-secret: mask the value in verbose / dry-run output
	strVal  *string
	intVal  *int
	boolVal *bool
	strSlc  *[]string
}

// headerFlag binds a CLI flag to a request header declared in the spec
// (`"in": "header"`). Header values are always strings on the wire, so a
// header flag is string-valued; run.go sends it only when the user set the
// flag, so an omitted optional header stays absent.
type headerFlag struct {
	apiName string // HTTP header name (e.g. "If-Match")
	secret  bool   // x-octo-secret: mask the value in verbose / dry-run output
	strVal  *string
}

// pathFlag is the optional flag alternative to a positional path argument,
// declared by x-octo-flag on a `"in": "path"` parameter.
//
// It exists because cobra cannot accept a positional value that starts with
// "-": it is parsed as a flag before the command ever runs. base64url ids
// legitimately start with "-" (roughly one id in 64), so `share revoke -Ab3…`
// fails with "unknown shorthand flag" and the caller has to know to write
// `share revoke -- -Ab3…`. A flag form sidesteps the ambiguity entirely without
// changing how any positional argument is parsed.
type pathFlag struct {
	paramName string // path placeholder name (e.g. "share_id")
	flagName  string // CLI flag name (e.g. "share-id")
	strVal    *string
}

type bodyFlag struct {
	apiName string // JSON body field name
	kind    valueKind
	secret  bool // x-octo-secret: mask the value in verbose / dry-run output
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
	// kindUint64 is a decimal-string flag backing a backend uint64 id. It is
	// NOT kindInt: Go's int tops out an order of magnitude below math.MaxUint64,
	// so an int-typed flag silently cannot express the upper half of the id
	// space. The flag value is validated as a decimal in [0, 2^64-1] and sent as
	// a JSON integer (query params as the same decimal text), so the wire
	// contract is unchanged while the CLI surface stays lossless.
	kindUint64
)

// uint64Format is the OpenAPI `format` that marks an integer schema as a
// backend uint64 id. Declaring it opts the field into decimal-string handling
// on input and, together with x-octo-lossless-id-fields, on output.
const uint64Format = "uint64"

func registerQueryFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.In != "query" {
			continue
		}
		flagName := paramFlagName(p)
		if rt.pathFlagNames[flagName] || reservedFlagNames[flagName] {
			continue
		}
		qf := &queryFlag{apiName: p.Name, kind: queryParamKind(p), secret: p.Secret}
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
		case kindUint64:
			qf.strVal = new(string)
			cmd.Flags().StringVar(qf.strVal, flagName, uint64Default(p.Default), uint64Desc(desc))
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
			_ = cmd.MarkFlagRequired(flagName) //nolint:errcheck // static flag name, can't fail
		}
	}
}

// paramFlagName is the CLI flag name for a spec parameter: the explicit
// x-octo-flag override when present, else the wire name with underscores
// turned into dashes (the historical derivation). The override lets a header
// like `If-Match` surface as a clean first-class flag (`--base-version`)
// without a hard-coded per-endpoint carve-out.
func paramFlagName(p *registry.ParamInfo) string {
	if p.FlagName != "" {
		return p.FlagName
	}
	return strings.ReplaceAll(p.Name, "_", "-")
}

// registerHeaderFlags binds every `"in": "header"` parameter to a string flag.
// This is the general spec-declared header capability: the request engine can
// set any per-request header from a flag, so an endpoint needing (say) an
// If-Match optimistic-concurrency token declares it in the spec rather than
// requiring bespoke code. run.go emits the header only when the flag is set.
// Registered before body flags so a header flag never collides with a promoted
// body field of the same name.
func registerHeaderFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.In != "header" {
			continue
		}
		flagName := paramFlagName(p)
		if rt.pathFlagNames[flagName] || reservedFlagNames[flagName] {
			continue
		}
		hf := &headerFlag{apiName: p.Name, secret: p.Secret, strVal: new(string)}
		cmd.Flags().StringVar(hf.strVal, flagName, "", p.Description)
		rt.headerFlags[flagName] = hf
		if p.Required {
			_ = cmd.MarkFlagRequired(flagName) //nolint:errcheck // static flag name, can't fail
		}
	}
}

// reservedFlagNames are the flag names the engine itself registers on an
// operation. A spec-declared flag colliding with one of them would panic pflag
// ("flag redefined") at startup, which takes down the whole binary — every leaf
// is built in RegisterServiceCommands, so even `octo-cli version` would die.
// All four registration paths (path / query / header / body) refuse a colliding
// name instead.
//
// Refusing means the spec's flag is silently absent, which is a spec bug the
// engine cannot report at load time (registration has no error channel). It is
// still strictly better than a dead binary, and
// TestFlags_NoSpecCollidesWithEngineFlags turns the whole class into a test
// failure at development time rather than a silent loss in production.
var reservedFlagNames = map[string]bool{
	"data": true, "file": true,
	"page-all": true, "page-limit": true,
	"output": true, "o": true,
}

// registerPathFlags binds every path parameter that declares x-octo-flag to an
// optional string flag alternative to its positional slot. Registered before
// query / header / body flags so a path flag always wins a name collision — a
// positional value has no other way in, while every other kind does.
//
// Operations whose path params declare no x-octo-flag get no flags and keep
// cobra.ExactArgs, so positional parsing is untouched everywhere else.
//
// A name already taken by the engine's own flags, or by an earlier path param in
// the same operation, is skipped: that param keeps working positionally, which
// is strictly better than panicking the whole tree on a spec typo.
func registerPathFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) {
	for _, name := range rt.pathParams {
		p := findParam(d, name, "path")
		if p == nil || p.FlagName == "" {
			continue
		}
		if reservedFlagNames[p.FlagName] || rt.pathFlagNames[p.FlagName] {
			continue
		}
		pf := &pathFlag{paramName: name, flagName: p.FlagName, strVal: new(string)}
		cmd.Flags().StringVar(pf.strVal, p.FlagName, "",
			fmt.Sprintf("%s (alternative to the positional <%s>)",
				firstSentence(p.Description), strings.ReplaceAll(name, "_", "-")))
		rt.pathFlags = append(rt.pathFlags, pf)
		if rt.pathFlagNames == nil {
			rt.pathFlagNames = map[string]bool{}
		}
		rt.pathFlagNames[p.FlagName] = true
	}
}

// firstSentence trims a spec description down to its leading sentence so a
// path flag's help line stays one line. Descriptions without a "." are used
// whole.
func firstSentence(desc string) string {
	if i := strings.Index(desc, ". "); i >= 0 {
		return desc[:i]
	}
	return strings.TrimSuffix(desc, ".")
}

func registerBodyFlags(cmd *cobra.Command, rt *operationRuntime, d *registry.OperationDetail) { //nolint:gocyclo // flag registration has many branches by nature; well-structured
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
		_ = cmd.MarkFlagRequired("file") //nolint:errcheck // static flag name, can't fail
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
		kind, ok := promotableKind(&prop)
		if !ok {
			continue
		}
		// Flag name: an explicit x-octo-flag override on the property wins (so a
		// clean --scope can front a shareScope wire key); otherwise derive it from
		// the property name with underscores turned into dashes. The wire body key
		// (bf.apiName) always stays the property name regardless of the flag alias.
		flagName := prop.FlagName
		if flagName == "" {
			flagName = strings.ReplaceAll(name, "_", "-")
		}
		// Avoid collisions with the engine's own flags (--data, --file, the
		// pagination and --output flags), a path flag, a query param, or a
		// spec-declared header flag of the same name (e.g. an If-Match header
		// exposed as --base-version takes precedence over a body baseVersion
		// mirror).
		if reservedFlagNames[flagName] || rt.pathFlagNames[flagName] ||
			rt.queryFlags[flagName] != nil || rt.headerFlags[flagName] != nil {
			continue
		}
		desc := prop.Description
		if len(prop.Enum) > 0 {
			desc = fmt.Sprintf("%s (one of: %s)", desc, formatEnum(prop.Enum))
		}
		if required[name] {
			desc = strings.TrimSpace(desc + " (required)")
		}
		bf := &bodyFlag{apiName: name, kind: kind, secret: prop.Secret}
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
		case kindUint64:
			bf.strVal = new(string)
			cmd.Flags().StringVar(bf.strVal, flagName, "", uint64Desc(desc))
		default:
			bf.strVal = new(string)
			cmd.Flags().StringVar(bf.strVal, flagName, "", desc)
		}
		rt.bodyFlags[flagName] = bf
	}
}

// uint64Desc appends the lossless-id contract to a flag description so the
// help text tells an Agent to paste the id verbatim rather than compute with it.
func uint64Desc(desc string) string {
	return strings.TrimSpace(desc + " (decimal uint64 id, max " + output.MaxUint64Decimal + ")")
}

// uint64Default renders a spec default for a uint64 flag. The spec writes it as
// a JSON number (e.g. parent_id defaults to 0); the flag is text, so it has to
// be formatted rather than assigned.
func uint64Default(def any) string {
	switch v := def.(type) {
	case string:
		return v
	case float64:
		if v < 0 {
			return ""
		}
		return strconv.FormatUint(uint64(v), 10)
	}
	return ""
}

// promotableKind returns the primitive flag kind a body property maps to,
// or (0,false) if the property is complex (object, array-of-object, etc.)
// and must go through --data. Enums inherit their base type.
func promotableKind(p *registry.SchemaInfo) (valueKind, bool) {
	switch p.Type {
	case "string":
		return kindString, true
	case "integer", "number":
		if p.Format == uint64Format {
			return kindUint64, true
		}
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

func queryParamKind(p *registry.ParamInfo) valueKind {
	switch p.Type {
	case "integer", "number":
		if p.Format == uint64Format {
			return kindUint64
		}
		return kindInt
	case "boolean":
		return kindBool
	case "array":
		if p.Items != nil && p.Items.Type == "string" {
			return kindStringSlice
		}
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
