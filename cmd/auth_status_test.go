package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// newAuthTestFactory exercises the same credential policy as actual requests.
func newAuthTestFactory() *cmdutil.TestFactory {
	f := cmdutil.NewTestFactory()
	actual := cmdutil.NewDefaultFactory()
	actual.IOStreams = f.IOStreams
	f.Factory = actual
	return f
}

func TestAuth_StatusMatchesEnvironmentCredential(t *testing.T) {
	for _, alias := range []string{config.EnvToken, config.EnvBotToken} {
		for _, count := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%s/profiles=%d", alias, count), func(t *testing.T) {
				t.Setenv(authstore.EnvConfigDir, t.TempDir())
				t.Setenv(config.EnvToken, "")
				t.Setenv(config.EnvBotToken, "")
				t.Setenv(config.EnvBotID, "claimed-env-bot")
				t.Setenv(config.EnvSpaceID, "env-space")
				t.Setenv(config.EnvAPIBaseURL, "https://env.example")
				store, err := authstore.New()
				if err != nil {
					t.Fatal(err)
				}
				for i := range count {
					name := fmt.Sprintf("stored-%d", i)
					meta := &authstore.ProfileMeta{RobotID: name, BotKind: "app_bot", APIBaseURL: "https://stored.example", SpaceID: "stored-space"}
					if err := store.SaveProfile(name, meta, "app_stored_token"); err != nil {
						t.Fatal(err)
					}
				}
				const token = "bf_environment_credential"
				t.Setenv(alias, "  "+token+"  ")
				out, _, err := execRoot(t, newAuthTestFactory(), "auth", "status")
				if err != nil {
					t.Fatalf("status: %v", err)
				}
				active := activeOf(t, out)
				want := map[string]any{"source": "env:" + alias, "bot_token": credential.MaskToken(token), "bot_kind": "user_bot", "space_id": "env-space", "api_base_url": "https://env.example", "robot_id_claimed": "claimed-env-bot"}
				for key, value := range want {
					if active[key] != value {
						t.Errorf("active.%s = %v, want %v", key, active[key], value)
					}
				}
				if active["robot_id"] != nil || active["profile"] != nil {
					t.Errorf("environment credential must not claim a stored identity: %#v", active)
				}
				if strings.Contains(out, token) || strings.Contains(out, "app_stored_token") {
					t.Error("status leaked an unmasked token")
				}
				configOut, _, err := execRoot(t, newAuthTestFactory(), "config", "show")
				if err != nil {
					t.Fatalf("config show: %v", err)
				}
				if got := dataOf(t, configOut)["bot_token_source"]; got != active["source"] {
					t.Errorf("status source %v differs from request configuration %v", active["source"], got)
				}
			})
		}
	}
}

func TestAuth_StatusHonorsExplicitSelectors(t *testing.T) {
	for _, alias := range []string{config.EnvToken, config.EnvBotToken} {
		for _, selector := range []string{"--profile", "--bot-id"} {
			t.Run(alias+"/"+selector, func(t *testing.T) {
				t.Setenv(authstore.EnvConfigDir, t.TempDir())
				t.Setenv(config.EnvToken, "")
				t.Setenv(config.EnvBotToken, "")
				loginToken(t, "stored-bot", "app_stored_token")
				t.Setenv(alias, "bf_environment_credential")
				out, _, err := execRoot(t, newAuthTestFactory(), "auth", "status", selector, "stored-bot")
				if err != nil {
					t.Fatal(err)
				}
				active := activeOf(t, out)
				if active["source"] != "profile:stored-bot" || active["robot_id"] != "stored-bot" || active["bot_token"] != credential.MaskToken("app_stored_token") {
					t.Fatalf("explicit selector did not select the stored credential: %#v", active)
				}
				if _, _, err := execRoot(t, newAuthTestFactory(), "auth", "status", selector, "missing"); output.AsExitError(err) == nil || output.AsExitError(err).Type != "auth_error" {
					t.Fatalf("missing explicit selector must fail closed: %v", err)
				}
			})
		}
	}
}

func TestAuth_StatusIgnoresTaskTokenInOrdinaryMode(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvBotToken, "")
	t.Setenv(config.EnvTaskToken, "task_credential_not_for_ordinary_commands")
	out, _, err := execRoot(t, newAuthTestFactory(), "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if dataOf(t, out)["active"] != nil || strings.Contains(out, "task_credential") {
		t.Fatalf("ordinary status must ignore the task credential: %s", out)
	}
}

func TestAuth_StatusEnvironmentBypassesUnreadableStore(t *testing.T) {
	// An ordinary request using an environment token never opens the store.
	invalidDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(invalidDir, []byte("not a store"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(authstore.EnvConfigDir, invalidDir)
	t.Setenv(config.EnvToken, "app_environment_credential")
	t.Setenv(config.EnvBotToken, "bf_lower_priority_credential")
	out, _, err := execRoot(t, newAuthTestFactory(), "auth", "status")
	if err != nil {
		t.Fatal(err)
	}
	if got := activeOf(t, out)["source"]; got != "env:OCTO_TOKEN" {
		t.Fatalf("source = %v, want canonical environment alias", got)
	}
}

func TestAuth_StatusDoesNotHideResolutionErrors(t *testing.T) {
	for _, mode := range []string{"", "invalid"} {
		t.Run("mode="+mode, func(t *testing.T) {
			invalidDir := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(invalidDir, []byte("not a store"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(authstore.EnvConfigDir, invalidDir)
			t.Setenv(config.EnvToken, "")
			t.Setenv(config.EnvBotToken, "")
			t.Setenv(config.EnvCredentialMode, mode)
			out, _, err := execRoot(t, newAuthTestFactory(), "auth", "status")
			if err == nil {
				t.Fatalf("resolution error must not become an informational success: %s", out)
			}
		})
	}
}
