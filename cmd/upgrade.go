package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const (
	upgradeHandoffEnvVar = "OCTO_CLI_UPGRADE_HANDOFF"
	upgradeReportEnvVar  = "OCTO_CLI_UPGRADE_REPORT"
)

type upgradeRequest struct {
	Version string `json:"version"`
	DryRun  bool   `json:"dry_run"`
}

type upgradePrivateError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

type upgradePrivateResult struct {
	Data  json.RawMessage      `json:"data"`
	Error *upgradePrivateError `json:"error"`
}

func newUpgradeCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "upgrade",
		Short: "Update the globally installed Octo CLI",
		Long: `Update the invoked global installation through its owning npm, Bun, or pnpm.

The manager's native registry, mirror and authentication settings select the
stable latest release. Equal or newer installed versions are successful no-ops.
The installed package, platform binary and global command are verified before
success is reported. No Octo credential is required.

Use --dry-run to check local ownership without contacting a registry or
installing. Project-local, temporary, source-linked and standalone installations
must be updated through their original installation method.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			handoffDir := os.Getenv(upgradeHandoffEnvVar)
			if os.Getenv(upgradeReportEnvVar) == "1" {
				if handoffDir == "" {
					return failErr(f, output.ErrValidation(
						"the private upgrade handoff is unavailable",
						"run `octo-cli upgrade` through its npm, Bun, or pnpm global launcher",
					))
				}
				return reportUpgradeResult(f, handoffDir)
			}
			if handoffDir == "" {
				return failErr(f, output.ErrValidation(
					"upgrade must be started by its npm, Bun, or pnpm global launcher",
					"run `octo-cli upgrade` through the global package-manager launcher; a standalone Go executable cannot update itself",
				))
			}

			request, err := json.Marshal(upgradeRequest{
				Version: strings.TrimPrefix(Version, "v"),
				DryRun:  f.Globals.DryRun,
			})
			if err != nil {
				return failErr(f, err)
			}
			if err := os.WriteFile(filepath.Join(handoffDir, "request.json"), request, 0o600); err != nil {
				return failErr(f, err)
			}
			return nil
		},
	}
}

func reportUpgradeResult(f *cmdutil.Factory, handoffDir string) error {
	body, err := os.ReadFile(filepath.Join(handoffDir, "result.json"))
	if err != nil {
		return failErr(f, err)
	}

	var result upgradePrivateResult
	if err := json.Unmarshal(body, &result); err != nil {
		return failErr(f, err)
	}
	if result.Error != nil {
		return failErr(f, output.ErrWithHint(
			result.Error.Type,
			result.Error.Code,
			result.Error.Message,
			result.Error.Hint,
		))
	}
	return f.EmitSuccess(result.Data)
}
