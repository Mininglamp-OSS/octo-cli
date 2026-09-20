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
	httpMode   bool
	uploadRoot string // OCTO_MCP_UPLOAD_ROOT; "" => local upload disabled over HTTP
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

	// Capture the connection-forced values BEFORE build(): registering root's
	// persistent flags (--space, --format, ...) via StringVar resets these
	// fields to their empty defaults. Format and the trusted --space must both
	// be re-applied afterward, or the stdio connection's space guard is silently
	// dropped and X-Space-Id never reaches the backend.
	forcedSpace := f.Globals.Space

	root := build(f)
	// Re-apply after build for the lifecycle reason above.
	f.Globals.Format = output.FormatJSON
	f.Globals.Space = forcedSpace
	root.SetArgs(argv)
	root.SetOut(outBuf)
	root.SetErr(errBuf)
	root.SilenceUsage = true
	root.SilenceErrors = true

	execErr := root.ExecuteContext(ctx)
	return readEnvelope(outBuf, errBuf, execErr)
}

// readEnvelope picks the envelope the run produced. A RunE that emitted an
// error wrote it to errBuf; a success wrote to outBuf; a cobra parse / auth
// gate failure returns via execErr with both buffers empty and is classified
// through the same WrapCLIError funnel the CLI's main uses.
func readEnvelope(outBuf, errBuf *bytes.Buffer, execErr error) ([]byte, bool) {
	if errBuf.Len() > 0 {
		return append([]byte(nil), errBuf.Bytes()...), false
	}
	if outBuf.Len() > 0 {
		b := append([]byte(nil), outBuf.Bytes()...)
		return b, envelopeOK(b)
	}
	if execErr != nil {
		return synthErrorEnvelope(cmdutil.WrapCLIError(execErr)), false
	}
	return synthErrorEnvelope(output.ErrWithHint("internal", "NO_OUTPUT", "operation produced no output", "")), false
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
//   - A synthesized flag name may never be a reserved engine/root flag (B3): a
//     model cannot inject --profile/--bot-id/--space/--format/--verbose/etc.
//   - For a multipart operation, only fields the schema declares (plus the file
//     binding) are accepted; an undeclared key is rejected rather than turned
//     into an arbitrary --key flag (B3).
//   - A multipart file_path is confined per policy (B2): over HTTP it is denied
//     unless an operator upload root is configured and the resolved path
//     (symlinks included) stays within it.
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
	declaredBody := map[string]bool{}
	if d.RequestBody != nil {
		for name := range d.RequestBody.Properties {
			declaredBody[name] = true
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
			// form-text flag. An UNDECLARED key is refused (B3), so it can never
			// synthesize an arbitrary or reserved flag.
			if !declaredBody[k] {
				unexpected = append(unexpected, k)
				continue
			}
			fn := strings.ReplaceAll(k, "_", "-")
			if reservedFlagNames[fn] {
				return nil, fmt.Errorf("argument %q maps to reserved flag --%s and cannot be set", k, fn)
			}
			vs, err := flagArgs(fn, v)
			if err != nil {
				return nil, err
			}
			flags = append(flags, vs...)
		case d.RequestBody != nil:
			body[k] = v
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

// resolveUploadPath enforces the multipart file-path capability boundary (B2).
// stdio is the trusted/local transport and passes the path through. Over HTTP a
// model-supplied path is denied unless the operator configured a confined
// upload root; when configured, the path (relative paths joined to the root) is
// cleaned and its symlinks resolved, and the real target must stay within the
// resolved root — blocking traversal, absolute escapes, and symlink escapes, so
// host auth/config/environment files are never reachable through MCP.
func resolveUploadPath(p string, pol execPolicy) (string, error) {
	if !pol.httpMode {
		return p, nil
	}
	if strings.TrimSpace(pol.uploadRoot) == "" {
		return "", errors.New("local file_path upload is disabled for HTTP connections; set OCTO_MCP_UPLOAD_ROOT to a confined directory to enable it")
	}
	root, err := filepath.EvalSymlinks(pol.uploadRoot)
	if err != nil {
		return "", fmt.Errorf("configured upload root is not accessible: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("configured upload root is invalid: %w", err)
	}
	candidate := p
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	candidate = filepath.Clean(candidate)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("file_path is not accessible: %w", err)
	}
	rel, err := filepath.Rel(root, resolved)
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
