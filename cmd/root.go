// Package cmd is the command tree. It wires cobra commands with the Factory DI
// container and holds root-level persistent flags. Service-domain commands are
// auto-registered from the embedded OpenAPI registry via cmd/service — the
// only hand-written leaves are `schema`, `version`, and `api` (generic
// passthrough).
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// NewRootCmd builds the top-level command tree.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	if f == nil {
		f = cmdutil.NewDefaultFactory()
	}

	root := &cobra.Command{
		Use:   "octo",
		Short: "Octo CLI — command-line interface for the Octo ecosystem",
		Long:  "octo is a CLI for AI Agent Bots to interact with Octo services.\nService commands are generated from the embedded OpenAPI registry.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if skipValidation(cmd) {
				return nil
			}
			cfg, err := f.Config()
			if err != nil {
				return err
			}
			return cfg.Validate()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	pf := root.PersistentFlags()
	pf.StringVar(&f.Globals.Format, "format", "", "output format: json (default) | table | csv | ndjson")
	pf.StringVarP(&f.Globals.JQ, "jq", "q", "", "filter output with a jq expression")
	pf.BoolVar(&f.Globals.DryRun, "dry-run", false, "print request without executing")
	pf.BoolVar(&f.Globals.Verbose, "verbose", false, "write request/response trace to stderr")
	pf.StringVar(&f.Globals.Timeout, "timeout", "", "per-request timeout (e.g. 30s, 2m)")
	pf.BoolVar(&f.Globals.NoRetry, "no-retry", false, "disable retry on transient failures")
	pf.StringVar(&f.Globals.Space, "space", "", "space id (for platform-scoped bots)")

	root.AddCommand(newSchemaCmd(f))
	root.AddCommand(newVersionCmd(f))
	root.AddCommand(newAPICmd(f))
	root.AddCommand(newConfigCmd(f))
	root.AddCommand(newSkillsCmd(f))
	service.RegisterServiceCommands(root, f)

	return root
}

// skipValidation is true for commands that must run without a configured
// credential (e.g. `octo version`, `octo help`, `octo schema`, `octo config`,
// and cobra's generated `completion`). Walks the parent chain so leaves like
// `config show` match through their parent.
func skipValidation(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "help", "schema", "config", "completion", "skills", "":
			return true
		}
	}
	return false
}
