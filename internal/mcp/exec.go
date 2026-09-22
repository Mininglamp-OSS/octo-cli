package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// RootBuilder builds a fresh root cobra command bound to f. It is injected from
// package cmd (cmd.NewRootCmd) so this package does not import cmd (which would
// be a cycle). A FRESH root per call_op is required: cobra retains parsed flag
// state on a command, so reusing one root would leak a previous call's flags
// into the next.
type RootBuilder func(f *cmdutil.Factory) *cobra.Command

// execPolicy carries the connection's capability posture into argument
// translation: whether this is the HTTP transport (untrusted client) and, if
// so, the operator-confined upload root that a multipart file_path must stay
// within.
type execPolicy struct {
	httpMode         bool
	uploadRoot       string // OCTO_MCP_UPLOAD_ROOT; "" => local upload disabled unless allowLocalUpload
	allowLocalUpload bool   // operator --allow-local-upload: unconfined pass-through, stdio only
}

// reservedFlagNames are the engine/root flag names a translated argument must
// never be able to synthesize (identity/scope/output controls + engine flags).
// A model-supplied key that maps to one of these is refused before cobra sees
// it, so call_op cannot smuggle --profile/--bot-id/--space/--format/--verbose
// (or --data/--file/…) through an undeclared multipart or spec parameter.
var reservedFlagNames = map[string]bool{
	"format": true, "jq": true, "dry-run": true, "verbose": true,
	"timeout": true, "no-retry": true, "space": true, "bot-id": true, "profile": true,
	"data": true, "file": true, "page-all": true, "page-limit": true, "output": true, "o": true,
	// cobra auto-registers help on every command; a spec param named help/h would
	// otherwise print usage instead of executing.
	"help": true, "h": true,
}

// callGlobals is the operator GlobalOptions carried into a per-call factory. It
// is applied AFTER build() (which resets the fresh root's persistent flags to
// their defaults), so operator routing/limit/safety intent is not silently
// dropped.
//
// The propagate/reset split is deliberate and asserted by
// TestCallGlobals_EveryFieldClassified so a newly added GlobalOptions field
// fails loudly instead of vanishing:
//   - propagate (operator legitimately controls per call): BotID, Profile,
//     Timeout, NoRetry, DryRun — plus Space, which the trusted context supplies.
//     DryRun in particular is a safety switch, not an output knob: an operator
//     who starts `mcp serve --dry-run` gets a rehearsal server where every call
//     prints its request and performs no backend mutation, honoring the flag's
//     documented "print request without executing" contract.
//   - reset (must never be inherited into a per-call run): Format (pinned to
//     json — the tool result must stay an envelope), Verbose, JQ, PageAll,
//     PageMax (output/pagination knobs the model does not control).
func applyCallGlobals(g *cmdutil.GlobalOptions, base cmdutil.GlobalOptions, forcedSpace string) {
	// Propagate operator-set routing/limit/safety knobs.
	g.BotID = base.BotID
	g.Profile = base.Profile
	g.Timeout = base.Timeout
	g.NoRetry = base.NoRetry
	g.DryRun = base.DryRun
	// Trusted space wins over anything build() or the base carried.
	g.Space = forcedSpace
	// Reset: never inherit these into a per-call execution.
	g.Format = output.FormatJSON
	g.Verbose = false
	g.JQ = ""
	g.PageAll = false
	g.PageMax = 0
}

// executeOperation runs one operation through the generated cobra tree
// in-process, reusing identity routing, request assembly, pre-flight
// validation, transport, secret masking, and envelope emission unchanged. It
// writes into f's buffers and returns the JSON envelope plus whether the
// operation succeeded. Pre-flight translation problems and cobra parse errors
// are rendered as an error envelope so the caller always gets one.
func executeOperation(ctx context.Context, build RootBuilder, f *cmdutil.Factory, detail *registry.OperationDetail, arguments map[string]any, pol execPolicy, outBuf, errBuf *bytes.Buffer) (envelope []byte, ok bool) {
	outBuf.Reset()
	errBuf.Reset()

	argv, terr := buildArgv(detail, arguments, pol)
	if terr != nil {
		return synthErrorEnvelope(output.ErrValidation(terr.Error(), "call describe_op to see the operation's declared arguments")), false
	}

	// Snapshot the operator globals makeFactory set on f BEFORE build(): root's
	// persistent-flag registration resets *f.Globals to empty defaults, so the
	// operator's routing/limit intent (and the trusted space) must be re-applied
	// afterward or it is silently dropped.
	base := *f.Globals
	forcedSpace := base.Space

	root := build(f)
	applyCallGlobals(f.Globals, base, forcedSpace)
	root.SetArgs(argv)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SilenceUsage = true
	root.SilenceErrors = true

	execErr := root.ExecuteContext(ctx)
	return readEnvelope(outBuf, errBuf, execErr)
}

// readEnvelope picks the envelope the run produced. A successful RunE writes a
// full envelope to outBuf; an error RunE writes an error envelope to errBuf; a
// cobra parse / auth gate failure returns via execErr with both buffers empty.
//
// outBuf is preferred whenever it parses as an envelope, so a benign diagnostic
// on stderr (e.g. a client warning line) can never mask a real success — the
// envelope contract stays intact even if something writes to errBuf alongside a
// good result.
func readEnvelope(outBuf, errBuf *bytes.Buffer, execErr error) ([]byte, bool) {
	if outBuf.Len() > 0 && looksLikeEnvelope(outBuf.Bytes()) {
		b := append([]byte(nil), outBuf.Bytes()...)
		return b, envelopeOK(b)
	}
	if errBuf.Len() > 0 && looksLikeEnvelope(errBuf.Bytes()) {
		return append([]byte(nil), errBuf.Bytes()...), false
	}
	if errBuf.Len() > 0 {
		// Non-envelope stderr content with no usable envelope: wrap it.
		return synthErrorEnvelope(output.ErrWithHint("internal", "UNEXPECTED_OUTPUT", strings.TrimSpace(errBuf.String()), "")), false
	}
	if execErr != nil {
		return synthErrorEnvelope(cmdutil.WrapCLIError(execErr)), false
	}
	return synthErrorEnvelope(output.ErrWithHint("internal", "NO_OUTPUT", "operation produced no output", "")), false
}

// looksLikeEnvelope reports whether b decodes to a JSON object carrying an "ok"
// field — the shared shape of both success and error envelopes.
func looksLikeEnvelope(b []byte) bool {
	var env struct {
		OK *bool `json:"ok"`
	}
	return json.Unmarshal(b, &env) == nil && env.OK != nil
}

// envelopeOK reports whether an envelope's top-level "ok" is true.
func envelopeOK(b []byte) bool {
	var env struct {
		OK bool `json:"ok"`
	}
	return json.Unmarshal(b, &env) == nil && env.OK
}

// synthErrorEnvelope renders an ExitError as a standalone error envelope, for
// failures that occur before (or instead of) the engine emitting one.
func synthErrorEnvelope(err error) []byte {
	var buf bytes.Buffer
	_ = output.WriteError(&buf, cmdutil.WrapCLIError(err))
	return buf.Bytes()
}

// buildArgv translates an MCP call_op arguments object into CLI argv for the
// operation's generated leaf command. Path parameters become positionals after
// a "--" separator (so a base64url id starting with "-" is never parsed as a
// flag); query and header parameters become their spec-derived flags; every
// remaining declared/extra field is passed as the JSON --data body so run.go's
// merged-body validator judges it at every depth (no second validator here).
//
// Two capability guards run here, before cobra parsing:
//   - A synthesized flag name may never be a reserved engine/root flag: a
//     model cannot inject --profile/--bot-id/--space/--format/--verbose/etc.
//   - For a multipart operation, only fields the schema declares (plus the file
//     binding) are accepted; an undeclared key is rejected rather than turned
//     into an arbitrary --key flag.
//   - A multipart file_path is confined per policy: over HTTP it is denied
//     unless an operator upload root is configured and the resolved path
//     (symlinks included) stays within it.
//
// A query/header argument whose value is JSON null is dropped (null means
// "omitted", not "send an empty value"), so a null scope parameter cannot empty
// a required filter on the wire.
func buildArgv(d *registry.OperationDetail, args map[string]any, pol execPolicy) ([]string, error) {
	pathNames := extractPathParams(d.Path)
	pathSet := make(map[string]bool, len(pathNames))
	for _, n := range pathNames {
		pathSet[n] = true
	}

	queryByName := map[string]*registry.ParamInfo{}
	headerByName := map[string]*registry.ParamInfo{}
	for i := range d.Parameters {
		p := &d.Parameters[i]
		switch p.In {
		case "query":
			queryByName[p.Name] = p
		case "header":
			headerByName[p.Name] = p
		}
	}
	declaredBody := map[string]registry.SchemaInfo{}
	if d.RequestBody != nil {
		for name, prop := range d.RequestBody.Properties {
			declaredBody[name] = prop
		}
	}

	var flags []string
	body := map[string]any{}
	var pageAll bool
	var filePath string
	var unexpected []string

	for _, k := range sortedKeys(args) {
		v := args[k]
		switch {
		case k == "page_all":
			if b, ok := v.(bool); ok {
				pageAll = b
			}
		case k == "file_path" && d.Multipart:
			filePath = fmt.Sprint(v)
		case pathSet[k]:
			// consumed as a positional below
		case queryByName[k] != nil:
			if v == nil {
				continue // null query arg => omitted, not an empty scope
			}
			fn := flagName(queryByName[k])
			if reservedFlagNames[fn] {
				return nil, fmt.Errorf("argument %q maps to reserved flag --%s and cannot be set", k, fn)
			}
			vs, err := flagArgs(fn, v)
			if err != nil {
				return nil, err
			}
			flags = append(flags, vs...)
		case headerByName[k] != nil:
			if v == nil {
				continue // null header arg => omitted
			}
			fn := flagName(headerByName[k])
			if reservedFlagNames[fn] {
				return nil, fmt.Errorf("argument %q maps to reserved flag --%s and cannot be set", k, fn)
			}
			vs, err := flagArgs(fn, v)
			if err != nil {
				return nil, err
			}
			flags = append(flags, vs...)
		case d.Multipart:
			// Multipart ops take no --data; a declared body field rides as a
			// form-text flag. An UNDECLARED key is refused, so it can never
			// synthesize an arbitrary or reserved flag.
			prop, ok := declaredBody[k]
			if !ok {
				unexpected = append(unexpected, k)
				continue
			}
			fn := strings.ReplaceAll(k, "_", "-")
			if reservedFlagNames[fn] {
				return nil, fmt.Errorf("argument %q maps to reserved flag --%s and cannot be set", k, fn)
			}
			vs, err := flagArgs(fn, coerceToSchema(prop, v))
			if err != nil {
				return nil, err
			}
			flags = append(flags, vs...)
		case d.RequestBody != nil:
			// Coerce to the declared property type so a forced/string value (e.g.
			// a header-sourced channel_type "1") lands on the wire as the integer
			// the schema declares, matching what a typed CLI flag would send.
			if prop, ok := declaredBody[k]; ok {
				body[k] = coerceToSchema(prop, v)
			} else {
				body[k] = v
			}
		default:
			unexpected = append(unexpected, k)
		}
	}

	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		return nil, fmt.Errorf("unexpected argument(s) for %s: %s", d.ID, strings.Join(unexpected, ", "))
	}

	if len(body) > 0 {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
		flags = append(flags, "--data="+string(raw))
	}
	if pageAll && d.Pagination != nil {
		flags = append(flags, "--page-all")
	}
	if filePath != "" {
		resolved, err := resolveUploadPath(filePath, pol)
		if err != nil {
			return nil, err
		}
		flags = append(flags, "--file="+resolved)
	}

	positionals := make([]string, 0, len(pathNames))
	for _, n := range pathNames {
		pv, ok := args[n]
		if !ok {
			return nil, fmt.Errorf("missing required path argument %q for %s", n, d.ID)
		}
		s, err := scalarString(pv)
		if err != nil {
			return nil, fmt.Errorf("path argument %q: %w", n, err)
		}
		positionals = append(positionals, s)
	}

	argv := append([]string{}, commandWords(d)...)
	argv = append(argv, flags...)
	if len(positionals) > 0 {
		argv = append(argv, "--")
		argv = append(argv, positionals...)
	}
	return argv, nil
}

// resolveUploadPath enforces the multipart file-path capability boundary.
// stdio is the trusted/local transport and passes the path through. Over HTTP a
// model-supplied path is denied unless the operator configured a confined
// upload root; when configured, the path (relative paths joined to the root) is
// cleaned and its symlinks resolved, and the real target must stay within the
// resolved root — blocking traversal, absolute escapes, and symlink escapes, so
// host auth/config/environment files are never reachable through MCP.
// resolveUploadPath enforces the multipart file-path capability boundary on
// EVERY transport: a model-supplied file_path can otherwise turn call_op into
// an arbitrary local-file read (file.upload / html.asset.add /
// loop.attachment.upload then exfiltrate through the bot's space). The default
// is deny; the model can never toggle it.
//
//   - OCTO_MCP_UPLOAD_ROOT set  → confine (both transports): the path (relative
//     joined to the root) is cleaned and its symlinks resolved, and the real
//     target must stay within the resolved root — blocking traversal, absolute
//     escapes, and symlink escapes.
//   - root unset, stdio + operator --allow-local-upload → pass through, the
//     documented trusted-local escape hatch (operator flag, not model input).
//   - root unset otherwise (HTTP always, or stdio without the flag) → refuse.
func resolveUploadPath(p string, pol execPolicy) (string, error) {
	root := strings.TrimSpace(pol.uploadRoot)
	if root == "" {
		if pol.allowLocalUpload && !pol.httpMode {
			return p, nil
		}
		hint := "set OCTO_MCP_UPLOAD_ROOT to a confined directory to enable uploads"
		if !pol.httpMode {
			hint += ", or start `mcp serve --allow-local-upload` for a trusted-local host"
		}
		return "", errors.New("local file_path upload is disabled; " + hint)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("configured upload root is not accessible: %w", err)
	}
	resolvedRoot, err = filepath.Abs(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("configured upload root is invalid: %w", err)
	}
	candidate := p
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(resolvedRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("file_path is not accessible: %w", err)
	}
	rel, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("file_path escapes the configured upload root")
	}
	return resolved, nil
}

// commandWords maps an operationId to its CLI command path words, matching the
// engine's own registration rule (cmd/service/service.go attachOperation):
// the service name, then every operationId segment after the first when that
// first segment equals the service (docs.create -> docs create), or every
// segment when it differs (mcp.probe under marketplace -> marketplace mcp
// probe). Underscores become dashes, as the engine does.
func commandWords(d *registry.OperationDetail) []string {
	segs := strings.Split(d.ID, ".")
	start := 1
	if len(segs) == 0 || segs[0] != d.Service {
		start = 0
	}
	words := []string{d.Service}
	for _, s := range segs[start:] {
		words = append(words, strings.ReplaceAll(s, "_", "-"))
	}
	return words
}

// flagName derives a parameter's CLI flag name the same way flags.go does: an
// explicit x-octo-flag override, else the wire name with underscores dashed.
func flagName(p *registry.ParamInfo) string {
	if p.FlagName != "" {
		return p.FlagName
	}
	return strings.ReplaceAll(p.Name, "_", "-")
}

// flagArgs renders one argument value as "--name=value" tokens. A string slice
// repeats the flag; scalars are stringified losslessly (json.Number keeps its
// exact decimal text so a uint64 id is never rounded).
func flagArgs(name string, v any) ([]string, error) {
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, e := range arr {
			s, err := scalarString(e)
			if err != nil {
				return nil, fmt.Errorf("argument --%s: %w", name, err)
			}
			out = append(out, "--"+name+"="+s)
		}
		return out, nil
	}
	s, err := scalarString(v)
	if err != nil {
		return nil, fmt.Errorf("argument --%s: %w", name, err)
	}
	return []string{"--" + name + "=" + s}, nil
}

// coerceToSchema converts a string value to the primitive JSON type the schema
// declares (integer/number/boolean), so a trusted-context or header-sourced
// value such as channel_type "1" reaches the wire as the integer 1 the backend
// expects — matching what a typed CLI flag would send. Anything that does not
// cleanly convert is left unchanged for the normal validator to judge.
func coerceToSchema(prop registry.SchemaInfo, v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	switch prop.Type {
	case "integer":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return json.Number(strconv.FormatInt(n, 10))
		}
	case "number":
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			return json.Number(s)
		}
	case "boolean":
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}
	return v
}

// scalarString stringifies a JSON scalar for a flag / positional value.
func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case json.Number:
		return t.String(), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unsupported value type %T", v)
	}
}

// extractPathParams returns {placeholder} names in path order (mirrors the
// engine helper of the same name, which is unexported).
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

// disabledOpError reports a call to an operation on a withheld service.
func disabledOpError(operationID string) error {
	return output.ErrValidation(
		fmt.Sprintf("operation %q belongs to a disabled service and is not callable", operationID),
		"this service is withheld; it is not exposed by search_ops")
}

// divergentOpError reports an operation whose generated schema does not match
// its callable shape, pointing at the CLI form that does work.
func divergentOpError(operationID, cli string) error {
	return output.ErrValidation(
		fmt.Sprintf("operation %q is not available over MCP: its generated schema does not describe the callable request shape", operationID),
		fmt.Sprintf("use the CLI form instead: %s", cli))
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
