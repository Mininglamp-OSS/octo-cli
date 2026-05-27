package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// loginToken stores a token under --bot-id via a fresh factory (so In/Out are
// clean) sharing the test's OCTO_CONFIG_DIR.
func loginToken(t *testing.T, botID, token string) {
	t.Helper()
	f := newTestFactoryWithReg()
	f.In.WriteString(token)
	if _, _, err := execRoot(t, f, "auth", "login", "--bot-id", botID, "--with-token"); err != nil {
		t.Fatalf("login %s: %v", botID, err)
	}
}

func dataOf(t *testing.T, out string) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, out)
	}
	if env["ok"] != true {
		t.Fatalf("ok = %v\n%s", env["ok"], out)
	}
	data, _ := env["data"].(map[string]any)
	return data
}

// activeOf returns the status command's data.active object.
func activeOf(t *testing.T, out string) map[string]any {
	t.Helper()
	a, ok := dataOf(t, out)["active"].(map[string]any)
	if !ok {
		t.Fatalf("data.active is not an object:\n%s", out)
	}
	return a
}

func TestAuth_LoginListStatusLogout(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())

	loginToken(t, "cli_demo", "app_demo_token")

	// list shows the profile, no token.
	f := newTestFactoryWithReg()
	out, _, err := execRoot(t, f, "auth", "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.Contains(out, "app_demo_token") {
		t.Error("auth list leaked the raw token")
	}
	data := dataOf(t, out)
	if data["count"].(float64) != 1 {
		t.Errorf("count = %v", data["count"])
	}

	// status (whoami) for the sole profile.
	f = newTestFactoryWithReg()
	out, _, err = execRoot(t, f, "auth", "status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	active := activeOf(t, out)
	if active["robot_id"] != "cli_demo" {
		t.Errorf("robot_id = %v", active["robot_id"])
	}
	// app_demo_token → head 2 + *** + tail 4 (middle masked, never the secret).
	if active["bot_token"] != "app_de***oken" {
		t.Errorf("bot_token = %v (want masked)", active["bot_token"])
	}

	// logout removes it.
	f = newTestFactoryWithReg()
	if _, _, err = execRoot(t, f, "auth", "logout", "--bot-id", "cli_demo"); err != nil {
		t.Fatalf("logout: %v", err)
	}
	f = newTestFactoryWithReg()
	out, _, _ = execRoot(t, f, "auth", "list")
	if dataOf(t, out)["count"].(float64) != 0 {
		t.Errorf("count after logout = %v", dataOf(t, out)["count"])
	}
}

func TestAuth_LoginRequiresIdentifier(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	f := newTestFactoryWithReg()
	f.In.WriteString("app_x")
	_, _, err := execRoot(t, f, "auth", "login", "--with-token")
	if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
		t.Errorf("want validation error, got %v", err)
	}
}

func TestAuth_ProfileNameDefaultsToBotID(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	loginToken(t, "cli_demo", "app_demo_token")

	// Selecting by --profile cli_demo (the defaulted name) resolves the same entry.
	f := newTestFactoryWithReg()
	out, _, err := execRoot(t, f, "auth", "status", "--profile", "cli_demo")
	if err != nil {
		t.Fatalf("status by profile: %v", err)
	}
	if activeOf(t, out)["robot_id"] != "cli_demo" {
		t.Errorf("robot_id = %v", activeOf(t, out)["robot_id"])
	}
}

func TestAuth_StatusAmbiguousIsInformational(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	loginToken(t, "cli_a", "app_a")
	loginToken(t, "cli_b", "app_b")

	// status with no selector must NOT error — it's introspection. It reports
	// active=null + a pointer, but never enumerates (that is `auth list`).
	f := newTestFactoryWithReg()
	out, _, err := execRoot(t, f, "auth", "status")
	if err != nil {
		t.Fatalf("status should not error on ambiguity: %v", err)
	}
	data := dataOf(t, out)
	if data["active"] != nil {
		t.Errorf("active = %v, want nil", data["active"])
	}
	if data["profile_count"].(float64) != 2 {
		t.Errorf("profile_count = %v, want 2", data["profile_count"])
	}
	if _, listed := data["profiles"]; listed {
		t.Error("status must not enumerate profiles; that's `auth list`")
	}

	// status with --bot-id resolves to the one active bot.
	f = newTestFactoryWithReg()
	out, _, err = execRoot(t, f, "auth", "status", "--bot-id", "cli_b")
	if err != nil {
		t.Fatalf("status --bot-id: %v", err)
	}
	if activeOf(t, out)["robot_id"] != "cli_b" {
		t.Errorf("robot_id = %v", activeOf(t, out)["robot_id"])
	}
}
