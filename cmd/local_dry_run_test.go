package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestSkillsInstallDryRunRejectsWithoutCreatingTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills", "nested")
	f := newTestFactoryWithReg()

	_, _, err := execRoot(t, f, "skills", "--install", target, "--dry-run")
	requireDryRunValidation(t, err)
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Errorf("skills --install --dry-run target stat error = %v, want target absent", statErr)
	}
}

func TestAuthLoginDryRunRejectsBeforeReadingTokenOrCreatingProfile(t *testing.T) {
	for _, mode := range []string{"stdin", "token file"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			configDir := filepath.Join(root, "profiles")
			t.Setenv(authstore.EnvConfigDir, configDir)

			f := newTestFactoryWithReg()
			args := []string{"auth", "login", "--dry-run", "--profile", "dry-run-profile"}
			if mode == "stdin" {
				const token = "app_dry_run_token"
				f.In.WriteString(token)
				args = append(args, "--with-token")
				defer func() {
					if f.In.Len() != len(token) {
						t.Errorf("stdin remaining bytes = %d, want %d (login read the token)", f.In.Len(), len(token))
					}
				}()
			} else {
				args = append(args, "--token-file", filepath.Join(root, "missing-token"))
			}

			_, _, err := execRoot(t, f, args...)
			requireDryRunValidation(t, err)
			if _, statErr := os.Lstat(configDir); !os.IsNotExist(statErr) {
				t.Errorf("auth login --dry-run config directory stat error = %v, want directory absent", statErr)
			}
		})
	}
}

func TestAuthUpdateDryRunLeavesProfileUnchanged(t *testing.T) {
	store := seedLocalDryRunProfile(t)
	before := loadLocalDryRunProfile(t, store)
	beforeToken, err := store.GetToken("cli_dryrun")
	if err != nil {
		t.Fatalf("GetToken before dry-run: %v", err)
	}

	f := newTestFactoryWithReg()
	_, _, err = execRoot(t, f,
		"auth", "update", "--dry-run", "--bot-id", "cli_dryrun",
		"--api-base-url", "https://after.example")
	requireDryRunValidation(t, err)

	if after := loadLocalDryRunProfile(t, store); after != before {
		t.Errorf("profile after auth update --dry-run = %+v, want unchanged %+v", after, before)
	}
	if got, err := store.GetToken("cli_dryrun"); err != nil || got != beforeToken {
		t.Errorf("token after auth update --dry-run = %q, %v; want unchanged token", got, err)
	}
}

func TestAuthLogoutDryRunKeepsProfile(t *testing.T) {
	store := seedLocalDryRunProfile(t)
	before := loadLocalDryRunProfile(t, store)
	beforeToken, err := store.GetToken("cli_dryrun")
	if err != nil {
		t.Fatalf("GetToken before dry-run: %v", err)
	}

	f := newTestFactoryWithReg()
	_, _, err = execRoot(t, f, "auth", "logout", "--dry-run", "--bot-id", "cli_dryrun")
	requireDryRunValidation(t, err)

	profiles, err := store.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles after auth logout --dry-run: %v", err)
	}
	after, exists := profiles["cli_dryrun"]
	if !exists || after != before {
		t.Errorf("profile after auth logout --dry-run = %+v, exists=%v; want unchanged %+v", after, exists, before)
	}
	if got, err := store.GetToken("cli_dryrun"); err != nil || got != beforeToken {
		t.Errorf("token after auth logout --dry-run = %q, %v; want unchanged token", got, err)
	}
}

func requireDryRunValidation(t *testing.T, err error) {
	t.Helper()
	ee := output.AsExitError(err)
	if ee == nil || ee.Type != "validation" || ee.Code != "VALIDATION_ERROR" {
		t.Errorf("error = %v, want validation error", err)
		return
	}
	if !strings.Contains(ee.Message, "--dry-run") {
		t.Errorf("validation message = %q, want dry-run refusal before command work", ee.Message)
	}
}

func seedLocalDryRunProfile(t *testing.T) *authstore.Store {
	t.Helper()
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	loginToken(t, "cli_dryrun", "app_dryrun_token")
	store, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	if err := store.UpdateProfileAPIBaseURL("cli_dryrun", "https://before.example"); err != nil {
		t.Fatalf("seed API base URL: %v", err)
	}
	return store
}

func loadLocalDryRunProfile(t *testing.T, store *authstore.Store) authstore.ProfileMeta {
	t.Helper()
	profiles, err := store.LoadProfiles()
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	profile, exists := profiles["cli_dryrun"]
	if !exists {
		t.Fatal("seed profile is missing")
	}
	return profile
}
