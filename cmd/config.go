package cmd

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/cmdutil"
	"github.com/dmwork-org/octo-cli/internal/config"
)

// newConfigCmd returns `octo config` and its subcommands. The group is
// diagnostic-only — commands under it must tolerate an unconfigured env so an
// agent can run `octo config show` to find out what's missing.
func newConfigCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect CLI configuration",
	}
	cmd.AddCommand(newConfigShowCmd(f))
	return cmd
}

// newConfigShowCmd prints the currently resolved configuration as a success
// envelope. The bot token is masked (first 8 chars + "***") so an agent can
// verify *which* bot is in use without leaking the secret.
func newConfigShowCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the resolved configuration (token masked)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Load()
			if f.Globals != nil && f.Globals.Format != "" {
				cfg.Format = f.Globals.Format
			}
			space := cfg.SpaceID
			if f.Globals != nil && f.Globals.Space != "" {
				space = f.Globals.Space
			}
			payload := map[string]any{
				"api_url":          cfg.APIURL,
				"matters_url":      urlOrNull(cfg.MattersURL),
				"dmworkim_url":     urlOrNull(cfg.DmworkIMURL),
				"space_id":         nullableString(space),
				"format":           cfg.Format,
				"bot_token":        maskToken(cfg.BotToken),
				"bot_token_source": tokenSource(cfg.BotToken),
				"bot_kind":         botKind(cfg.BotToken),
			}
			raw, err := json.Marshal(payload)
			if err != nil {
				return f.EmitError(err)
			}
			return f.EmitSuccess(raw)
		},
	}
}

// maskToken returns the token's first 8 chars followed by "***" (or a shorter
// prefix if the token is that short). Empty tokens return null so envelope
// consumers can detect the unconfigured case cleanly.
func maskToken(tok string) any {
	if tok == "" {
		return nil
	}
	if len(tok) <= 8 {
		return tok + "***"
	}
	return tok[:8] + "***"
}

func tokenSource(tok string) any {
	if tok == "" {
		return nil
	}
	return "env:" + config.EnvBotToken
}

func botKind(tok string) any {
	switch {
	case tok == "":
		return nil
	case len(tok) >= 4 && tok[:4] == "app_":
		return "app_bot"
	case len(tok) >= 3 && tok[:3] == "bf_":
		return "user_bot"
	}
	return "unknown"
}

func urlOrNull(u string) any {
	if u == "" {
		return nil
	}
	return u
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
