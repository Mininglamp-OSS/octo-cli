package cmd

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// newConfigCmd returns `octo-cli config` and its subcommands. The group is
// diagnostic-only — commands under it must tolerate an unconfigured env so an
// agent can run `octo-cli config show` to find out what's missing.
func newConfigCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect CLI configuration",
	}
	cmd.AddCommand(newConfigShowCmd(f))
	return cmd
}

// newConfigShowCmd prints the currently resolved configuration as a success
// envelope. The bot token is masked (prefix only + "***") so an agent can
// verify *which* bot is in use without leaking the secret.
//
// Resolution goes through the Factory so the values reflect the same path the
// rest of the CLI takes (ConfigFunc/CredentialFunc may be stubbed by tests).
// When the Factory can't produce a config — e.g. ambiguous profiles — fall back
// to config.Load() so `config show` still emits a diagnostic envelope.
func newConfigShowCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the resolved configuration (token masked)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := f.Config()
			if err != nil {
				// Surface structured resolution errors (e.g. a --bot-id that
				// matches no profile, or ambiguous profiles) instead of hiding
				// them behind a misleading env fallback. Only the unconfigured
				// case (a plain error / nil cfg) falls back to a diagnostic view.
				if ee := output.AsExitError(err); ee != nil {
					_ = f.EmitError(ee) //nolint:errcheck // best-effort envelope write
					return ee
				}
				cfg = config.Load()
			}
			if cfg == nil {
				cfg = config.Load()
			}
			space := cfg.SpaceID
			if f.Globals != nil && f.Globals.Space != "" {
				space = f.Globals.Space
			}

			// Prefer the resolved credential so the displayed token/source/
			// identity reflect the active profile (not just the env var).
			token := cfg.BotToken
			source := defaultTokenSource(cfg.BotToken)
			var profileName, robotID string
			if cred, cerr := f.Credential(); cerr == nil && cred != nil {
				token = cred.Token
				if cred.Source != "" {
					source = cred.Source
				}
				profileName = cred.Profile
				robotID = cred.RobotID
			}

			payload := map[string]any{
				"api_base_url":     cfg.APIBaseURL,
				"space_id":         nullableString(space),
				"format":           cfg.Format,
				"bot_token":        maskToken(token),
				"bot_token_source": nullableString(source),
				"bot_kind":         botKind(token),
				"profile":          nullableString(profileName),
				"robot_id":         nullableString(robotID),
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return f.EmitError(err)
			}
			return f.EmitSuccess(raw)
		},
	}
}

// maskToken masks a token to its kind prefix + "***", returning nil for empty
// so envelope consumers can detect the unconfigured case cleanly.
func maskToken(tok string) any {
	if tok == "" {
		return nil
	}
	return credential.MaskToken(tok)
}

// defaultTokenSource is the source tag used when no resolved credential is
// available — the env var that would supply the token, following the same
// OCTO_TOKEN → OCTO_BOT_TOKEN precedence as credential.EnvProvider.
func defaultTokenSource(tok string) string {
	if tok == "" {
		return ""
	}
	if os.Getenv(config.EnvToken) != "" {
		return "env:" + config.EnvToken
	}
	return "env:" + config.EnvBotToken
}

func botKind(tok string) any {
	if tok == "" {
		return nil
	}
	return credential.TokenKind(tok)
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
