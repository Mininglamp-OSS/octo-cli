package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/marketplace"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/skillinstall"
)

func newMarketplaceCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "marketplace",
		Aliases: []string{"market"},
		Short:   "Install agent resources from Octo Marketplace",
	}
	cmd.AddCommand(newMarketplaceSkillCmd(f))
	return cmd
}

func newMarketplaceSkillCmd(f *cmdutil.Factory) *cobra.Command {
	var installRoot string
	cmd := &cobra.Command{
		Use:   "skills <skill-id>",
		Short: "Download and install one skill from Octo Marketplace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(installRoot) == "" {
				return emitMarketplaceError(f, output.ErrValidation("--install is required", "pass the agent skills root, for example --install ~/.codex/skills"))
			}
			cred, err := f.Credential()
			if err != nil {
				return emitMarketplaceError(f, err)
			}
			if credential.TokenKind(cred.Token) != "user_bot" {
				return emitMarketplaceError(f, output.ErrAuth("marketplace skill installation requires a bf_* User Bot token", "select a User Bot profile or set OCTO_BOT_TOKEN to a bf_* token"))
			}
			api, err := f.Client()
			if err != nil {
				return emitMarketplaceError(f, err)
			}
			client := marketplace.NewClient(api, &http.Client{
				Timeout: 30 * time.Second,
				CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
					return http.ErrUseLastResponse
				},
			})
			skill, err := client.GetSkill(cmd.Context(), args[0])
			if err != nil {
				return emitMarketplaceError(f, err)
			}
			archive, err := client.DownloadSkill(cmd.Context(), args[0])
			if err != nil {
				return emitMarketplaceError(f, err)
			}
			installed, err := skillinstall.Install(installRoot, skill.Name, archive, skill.FileSHA256)
			if err != nil {
				return emitMarketplaceError(f, output.ErrValidation(fmt.Sprintf("install skill: %v", err), "verify the marketplace archive and target directory"))
			}
			payload, err := json.Marshal(map[string]any{
				"source":       "marketplace",
				"skill_id":     skill.ID,
				"name":         skill.Name,
				"installed_to": installed.InstalledTo,
				"sha256":       skill.FileSHA256,
				"files":        installed.Files,
			})
			if err != nil {
				return emitMarketplaceError(f, err)
			}
			return f.EmitSuccess(payload)
		},
	}
	cmd.Flags().StringVar(&installRoot, "install", "", "install into this agent skills root")
	return cmd
}

func emitMarketplaceError(f *cmdutil.Factory, err error) error {
	_ = f.EmitError(err)
	return err
}
