// Package service provides the metadata-driven command auto-registration
// engine. For every operation in the embedded OpenAPI registry the engine
// generates a cobra command whose flags, body shape, risk gate, and
// pagination behaviour are derived entirely from the spec — so adding a new
// backend endpoint is "update the spec", nothing more.
//
// operationId "domain.verb" or "domain.resource.verb" maps to
// "octo-cli <domain> [<resource>] <verb>" (architecture §5.2). Path parameters
// become positional args; query parameters become typed flags; simple
// top-level body fields are auto-promoted to flags (Rule 5a), with a --data
// escape hatch for complex bodies.
package service

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
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
		Use:         svc,
		Short:       fmt.Sprintf("Operations on the %s domain", svc),
		RunE:        rejectUnknownSubcommand,
		Annotations: map[string]string{"skipValidation": "true"},
	}
	parent.AddCommand(c)
	return c
}

// attachOperation inserts one operation as a leaf command, creating
// intermediate "resource" commands (matter assignee, marketplace skill, …)
// on the way down. When operationId starts with the registry service name that
// segment is omitted; otherwise it is preserved as a domain below the service.
func attachOperation(svcCmd *cobra.Command, f *cmdutil.Factory, d *registry.OperationDetail) {
	segs := strings.Split(d.ID, ".")
	if len(segs) < 2 {
		return
	}
	// Most specs use an operationId whose first segment matches the registry
	// service (docs.create under service docs). A multi-domain backend may expose
	// several resource families from one service, for example skill.list and
	// mcp.list under service marketplace. Preserve that first segment as a
	// resource group when it differs from the service name so both operations
	// become addressable instead of colliding as marketplace list.
	start := 1
	if segs[0] != d.Service {
		start = 0
	}
	cur := svcCmd
	for i := start; i < len(segs)-1; i++ {
		name := strings.ReplaceAll(segs[i], "_", "-")
		sub := findChild(cur, name)
		if sub == nil {
			sub = &cobra.Command{
				Use:         name,
				Short:       fmt.Sprintf("%s %s operations", cur.Use, name),
				RunE:        rejectUnknownSubcommand,
				Annotations: map[string]string{"skipValidation": "true"},
			}
			cur.AddCommand(sub)
		}
		cur = sub
	}
	leaf := buildOperationCmd(f, d, strings.ReplaceAll(segs[len(segs)-1], "_", "-"))
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

// rejectUnknownSubcommand makes parent commands fail loudly on an
// unrecognised subcommand. Without it, cobra treats the unknown token
// as args, finds no RunE on the parent (Runnable()==false), prints
// help, and exits 0 — which can let automation treat a removed
// command as a success.
//
// The token is echoed only when it looks like a command name someone mistyped.
// The other way to land here is omitting the verb — `drive share <token>` instead
// of `drive share revoke <token>` — which would otherwise print a share token in
// an error the caller did not opt into.
func rejectUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		if looksLikeSubcommandName(args[0]) {
			return fmt.Errorf("unknown subcommand %q for %q", args[0], cmd.CommandPath())
		}
		return fmt.Errorf("unknown subcommand for %q; run `%s --help` for the available ones",
			cmd.CommandPath(), cmd.CommandPath())
	}
	return cmd.Help()
}

// looksLikeSubcommandName reports whether s has the shape of a command word, so
// echoing it back helps with a typo rather than disclosing an id. Command names
// in this tree are short, lower-case and dash-separated; ids are longer, mixed
// case, and often carry "_" or ":".
func looksLikeSubcommandName(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c == '-':
		default:
			return false
		}
	}
	return true
}

// buildOperationCmd builds the leaf cobra.Command for one operation.
// Flag registration is delegated to flags.go; execution to run.go.
func buildOperationCmd(f *cmdutil.Factory, d *registry.OperationDetail, verb string) *cobra.Command {
	rt := &operationRuntime{
		detail:      d,
		queryFlags:  map[string]*queryFlag{},
		headerFlags: map[string]*headerFlag{},
		bodyFlags:   map[string]*bodyFlag{},
	}

	rt.pathParams = extractPathParams(d.Path)

	usage := verb
	for _, p := range rt.pathParams {
		usage += " <" + strings.ReplaceAll(p, "_", "-") + ">"
	}

	cmd := &cobra.Command{
		Use:   usage,
		Short: d.Summary,
	}

	// Path flags must register before every other kind so they win a name
	// collision, and before Args is decided so the arity can relax.
	registerPathFlags(cmd, rt, d)
	cmd.Long = buildLongDesc(d, rt)
	if len(rt.pathFlags) > 0 {
		// A flagged path param may arrive as a flag instead of a positional, so
		// the exact count is no longer knowable here. runOperation reports the
		// precise "supply <x> or --x" error.
		cmd.Args = cobra.MaximumNArgs(len(rt.pathParams))
		cmd.SetFlagErrorFunc(leadingDashFlagError(rt))
	} else {
		cmd.Args = cobra.ExactArgs(len(rt.pathParams))
	}

	registerQueryFlags(cmd, rt, d)
	registerHeaderFlags(cmd, rt, d)
	registerBodyFlags(cmd, rt, d)

	if d.Pagination != nil {
		var pageAll bool
		var pageLimit int
		cmd.Flags().BoolVar(&pageAll, "page-all", false, "auto-paginate through all pages")
		cmd.Flags().IntVar(&pageLimit, "page-limit", 10, "maximum number of pages to fetch with --page-all")
		rt.pageAll = &pageAll
		rt.pageLimit = &pageLimit
	}

	// Operations that return a binary body inline on a 2xx success accept
	// --output/-o to WRITE those bytes to disk (e.g. docs.scene.export
	// --image-format png -o board.png). Without it the command only describes
	// the body (status/content_type/size). Redirect-style binary ops such as
	// file.download (302-only, no consumable body) are deliberately excluded:
	// registering -o there accepts the flag but never writes a file, a silent
	// footgun. See registry.OperationDetail.BinaryBody.
	if d.BinaryBody {
		var outputPath string
		cmd.Flags().StringVarP(&outputPath, "output", "o", "", "write the binary response body to this file path")
		rt.outputPath = &outputPath
	}

	cmd.RunE = func(cobraCmd *cobra.Command, args []string) error {
		return runOperation(cobraCmd, f, rt, args)
	}
	return cmd
}

func buildLongDesc(d *registry.OperationDetail, rt *operationRuntime) string {
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
	for _, pf := range rt.pathFlags {
		fmt.Fprintf(&b, "\n\n<%s> may also be given as --%s. Use the flag when the value "+
			"starts with \"-\" (base64url ids do), otherwise it is parsed as a flag:\n"+
			"  octo-cli %s --%s -Ab3cD…\n"+
			"  octo-cli %s -- -Ab3cD…",
			strings.ReplaceAll(pf.paramName, "_", "-"), pf.flagName,
			commandPathFor(d), pf.flagName, commandPathFor(d))
	}
	return b.String()
}

// commandPathFor renders the user-facing command words for an operation id,
// for use in help examples ("drive share revoke").
func commandPathFor(d *registry.OperationDetail) string {
	segs := strings.Split(d.ID, ".")
	for i := range segs {
		segs[i] = strings.ReplaceAll(segs[i], "_", "-")
	}
	return strings.Join(segs, " ")
}

// leadingDashFlagError rewrites cobra's raw flag-parse failure for commands
// that accept a flaggable positional id, because the overwhelmingly common
// cause is a base64url id starting with "-" being read as a flag.
//
// cobra's own text quotes the offending token ("unknown shorthand flag: 'A' in
// -Ab3…"), and for these operations that token is the id — a share token or an
// invite token. This runs before collectSecrets, so there is nothing to mask
// with: the value is dropped rather than redacted. The diagnostic loses nothing
// that matters, because the actionable part is which flag to use, and the hint
// already says it. The sibling guards rejectEmptyPathValue / rejectDotSegment
// take the same line.
func leadingDashFlagError(rt *operationRuntime) func(*cobra.Command, error) error {
	names := make([]string, 0, len(rt.pathFlags))
	for _, pf := range rt.pathFlags {
		names = append(names, "--"+pf.flagName)
	}
	return func(cmd *cobra.Command, err error) error {
		return output.ErrWithHint("validation", "INVALID_FLAG",
			"a positional value was parsed as a flag",
			fmt.Sprintf("if this is an id starting with \"-\", pass it as %s, "+
				"or put it after a \"--\" separator: %s -- <id>",
				strings.Join(names, " / "), cmd.CommandPath()))
	}
}

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
// config-level service key used by client.Request.Service. With the unified
// gateway model all services route to the same URL, so this always returns
// empty (default service). Retained for interface compatibility.
func serviceForBaseURL(_ string) string {
	return ""
}
