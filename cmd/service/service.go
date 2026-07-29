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
func rejectUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown subcommand %q for %q", args[0], cmd.CommandPath())
	}
	return cmd.Help()
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
		Long:  buildLongDesc(d),
		Args:  cobra.ExactArgs(len(rt.pathParams)),
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

// serviceForBaseURL maps a spec endpoint to the config-level routing key. Loop
// may use a standalone Fleet endpoint during local integration; all existing
// Octo services continue to use the unified default URL.
func serviceForBaseURL(baseURLEnv string) string {
	if baseURLEnv == "OCTO_LOOP_API_BASE_URL" {
		return "loop"
	}
	return ""
}
