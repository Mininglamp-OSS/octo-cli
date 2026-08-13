// Package cmd is the command tree. It wires cobra commands with the Factory DI
// container and holds root-level persistent flags. Service-domain commands are
// auto-registered from the embedded OpenAPI registry via cmd/service — the
// hand-written leaves are `schema`, `version`, `api` (generic passthrough),
// `config`, `skills`, `auth`, and `sheet-cell` (offline value→cell helper).
package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

// NewRootCmd builds the top-level command tree.
func NewRootCmd(f *cmdutil.Factory) *cobra.Command {
	if f == nil {
		f = cmdutil.NewDefaultFactory()
	}

	root := &cobra.Command{
		Use:   "octo-cli",
		Short: "Octo CLI — command-line interface for the Octo ecosystem",
		Long:  "octo-cli is a CLI for AI Agent Bots to interact with Octo services.\nService commands are generated from the embedded OpenAPI registry.",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTaskModeCommand(cmd); err != nil {
				return err
			}
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
	pf.StringVar(&f.Globals.BotID, "bot-id", "", "select/assert the bot credential by robot id (env OCTO_BOT_ID)")
	pf.StringVar(&f.Globals.Profile, "profile", "", "select the bot credential by profile name")

	root.AddCommand(newSchemaCmd(f))
	root.AddCommand(newVersionCmd(f))
	root.AddCommand(newAPICmd(f))
	root.AddCommand(newConfigCmd(f))
	root.AddCommand(newSkillsCmd(f))
	root.AddCommand(newAuthCmd(f))
	root.AddCommand(newSheetCellCmd(f))
	service.RegisterServiceCommands(root, f)
	registerDocsImportCmd(root, f)
	registerDocsExportCmd(root, f)
	registerDriveCmds(root, f)
	withholdDisabledServices(root, f)

	return root
}

// validateTaskModeCommand is defense in depth. It keeps daemon task processes
// on the Loop surface without duplicating Fleet's route/action policy in the
// CLI. Offline discovery commands remain available to agents.
func validateTaskModeCommand(cmd *cobra.Command) error {
	if strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvCredentialMode))) != config.CredentialModeTask {
		return nil
	}
	root := cmd.Root()
	current := cmd
	for current.Parent() != nil && current.Parent() != root {
		current = current.Parent()
	}
	if current == root {
		return nil
	}
	switch current.Name() {
	case "loop", "help", "version", "schema", "skills", "completion":
		return nil
	default:
		return fmt.Errorf("%s=task only allows Loop commands", config.EnvCredentialMode)
	}
}

// withholdDisabledServices removes the command subtree for any service whose
// spec sets x-octo-disabled (e.g. matter, whose backend API is not yet stable).
// The spec stays embedded, so `octo-cli schema` and the metadata-driven engine
// still see it — flip the flag in the spec to re-enable. Done here (after
// registration) rather than inside RegisterServiceCommands so the engine tests
// that drive it directly keep their full fixture coverage.
func withholdDisabledServices(root *cobra.Command, f *cmdutil.Factory) {
	reg := f.Registry()
	if reg == nil {
		return
	}
	for _, svc := range reg.ListServices() {
		if !reg.ServiceDisabled(svc) {
			continue
		}
		for _, c := range root.Commands() {
			if c.Name() == svc {
				root.RemoveCommand(c)
				break
			}
		}
	}
}

// skipValidation is true for commands that must run without a configured
// credential (e.g. `octo-cli version`, `octo-cli help`, `octo-cli schema`, `octo-cli config`,
// `octo-cli auth`, and cobra's generated `completion`). Walks the parent chain so
// leaves like `config show` match through their parent.
func skipValidation(cmd *cobra.Command) bool {
	// Per-command annotation, deliberately NOT inherited through the parent
	// chain: service parents (`octo-cli thread`, `octo-cli group`, …) opt out of the
	// auth gate so their help / unknown-subcommand path works without a token,
	// but their leaf operations (`octo-cli thread create …`) must still authenticate.
	if cmd.Annotations["skipValidation"] == "true" {
		return true
	}
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "version", "help", "schema", "config", "completion", "skills", "auth", "sheet-cell", "":
			return true
		}
	}
	return false
}
