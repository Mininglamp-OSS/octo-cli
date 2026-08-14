package cmdutil

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// TestMain isolates the whole package from the developer's own OCTO_
// environment. NewDefaultFactory resolves credentials through the on-disk store
// and then the env chain, so an ambient variable can outrank or redirect the
// fixtures these tests pin with t.Setenv: OCTO_TOKEN outranks the OCTO_BOT_TOKEN
// they set (internal/credential/env_provider.go), OCTO_BOT_ID selects a stored
// profile that does not exist here, OCTO_FORMAT reshapes the resolved config.
//
// The sweep is by prefix rather than by name on purpose. Naming variables is
// what kept breaking: the clear list has been out of date three times
// (OCTO_TOKEN, then OCTO_FORMAT, then OCTO_BOT_ID), each time because a new
// variable was added without updating the tests. OCTO_MARKETPLACE_API_PREFIX
// (cmd/service/run.go) is read straight from the environment with no config
// constant behind it, so it is exactly the kind a by-name list misses.
//
// OCTO_CONFIG_DIR is re-set *after* the sweep: it must point at an empty temp
// dir rather than be absent, or authstore falls back to the real user config dir
// and a developer's stored profiles leak back in. An empty dir yields zero
// profiles, so resolution falls through to the env chain as these tests expect.
// Individual tests may still override any of this with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octo-cmdutil-test")
	if err != nil {
		panic(err)
	}
	sweepOctoEnv()
	os.Setenv("OCTO_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// sweepOctoEnv unsets every OCTO_-prefixed variable in the process environment.
// os.Environ returns a snapshot, so unsetting while ranging over it is safe.
func sweepOctoEnv() {
	for _, kv := range os.Environ() {
		if name, _, found := strings.Cut(kv, "="); found && strings.HasPrefix(name, "OCTO_") {
			os.Unsetenv(name)
		}
	}
}

func TestNewDefaultFactory_InitializesFields(t *testing.T) {
	f := NewDefaultFactory()
	if f == nil {
		t.Fatal("NewDefaultFactory returned nil")
	}
	if f.IOStreams == nil {
		t.Error("IOStreams should be initialized")
	}
	if f.Globals == nil {
		t.Error("Globals should be initialized")
	}
	if f.ConfigFunc == nil || f.CredentialFunc == nil || f.ClientFunc == nil ||
		f.MailCredentialFunc == nil || f.MailClientFunc == nil || f.RegistryFunc == nil {
		t.Error("all provider funcs should be set")
	}
}

func TestFactory_ConfigCaches(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_xxx")
	t.Setenv("OCTO_API_BASE_URL", "http://x")

	f := NewDefaultFactory()
	c1, err := f.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	c2, err := f.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if c1 != c2 {
		t.Errorf("Config should cache: %p vs %p", c1, c2)
	}
}

func TestFactory_ConfigFormatOverride(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_xxx")
	t.Setenv("OCTO_FORMAT", "json")

	f := NewDefaultFactory()
	f.Globals.Format = "table"

	cfg, err := f.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Format != "table" {
		t.Errorf("Globals.Format should override config, got %q", cfg.Format)
	}
}

func TestFactory_CredentialFromEnv(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_yyy")
	t.Setenv("OCTO_SPACE_ID", "space-A")

	f := NewDefaultFactory()
	cred, err := f.Credential()
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.Token != "app_yyy" {
		t.Errorf("Token = %q", cred.Token)
	}
	if cred.SpaceID != "space-A" {
		t.Errorf("SpaceID = %q", cred.SpaceID)
	}
}

func TestFactory_ProfileBaseURLErrorNamesProfile(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvAPIBaseURL, "")
	t.Setenv(config.EnvBotToken, "")
	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProfile("legacy", &authstore.ProfileMeta{
		APIBaseURL: "https://api.example.com/fleet/api/v1",
		RobotID:    "bot-legacy",
	}, "app_legacy"); err != nil {
		t.Fatal(err)
	}
	f := NewDefaultFactory()
	f.Globals.Profile = "legacy"
	_, err = f.Config()
	if err == nil || !strings.Contains(err.Error(), `profile "legacy"`) || !strings.Contains(err.Error(), "auth login") {
		t.Fatalf("Config error = %v, want named profile recovery guidance", err)
	}
}

func TestFactory_TaskCredentialMode(t *testing.T) {
	t.Run("uses only injected token without opening auth store", func(t *testing.T) {
		blockedStore := t.TempDir() + "/not-a-directory"
		if err := os.WriteFile(blockedStore, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(authstore.EnvConfigDir, blockedStore)
		t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
		t.Setenv(config.EnvBotToken, "octo_loop_task")

		f := NewDefaultFactory()
		cred, err := f.Credential()
		if err != nil {
			t.Fatalf("Credential: %v", err)
		}
		if cred.Token != "octo_loop_task" || cred.BotKind != "agent_task" || cred.SpaceID != "" ||
			cred.Source != "env:"+config.EnvBotToken {
			t.Fatalf("credential = %+v", cred)
		}
		identity, ok := f.identityValue().(map[string]any)
		if !ok || identity["type"] != "agent_task" || identity["credential_kind"] != "agent_task" {
			t.Fatalf("identity = %#v", identity)
		}
	})

	t.Run("rejects generic token override", func(t *testing.T) {
		t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
		t.Setenv(config.EnvToken, "bf_human")
		t.Setenv(config.EnvBotToken, "octo_loop_task")
		f := NewDefaultFactory()
		if _, err := f.Credential(); err == nil || !strings.Contains(err.Error(), config.EnvToken) {
			t.Fatalf("Credential error = %v, want %s rejection", err, config.EnvToken)
		}
	})

	t.Run("rejects non-loop bot token", func(t *testing.T) {
		t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
		t.Setenv(config.EnvBotToken, "bf_human")
		f := NewDefaultFactory()
		if _, err := f.Credential(); err == nil || !strings.Contains(err.Error(), "Loop task credential") {
			t.Fatalf("Credential error = %v, want Loop credential rejection", err)
		}
	})

	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Factory)
	}{
		{"profile selector", func(_ *testing.T, f *Factory) { f.Globals.Profile = "default" }},
		{"bot id selector", func(_ *testing.T, f *Factory) { f.Globals.BotID = "bot-1" }},
		{"bot id environment", func(t *testing.T, _ *Factory) { t.Setenv(config.EnvBotID, "bot-1") }},
		{"space override", func(_ *testing.T, f *Factory) { f.Globals.Space = "space-1" }},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
			t.Setenv(config.EnvBotToken, "octo_loop_task")
			f := NewDefaultFactory()
			tc.setup(t, f)
			if _, err := f.Credential(); err == nil {
				t.Fatalf("task mode should reject %s", tc.name)
			}
		})
	}

	t.Run("missing injected token fails closed", func(t *testing.T) {
		t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
		t.Setenv(config.EnvBotToken, "")
		f := NewDefaultFactory()
		if _, err := f.Credential(); !errors.Is(err, credential.ErrNoCredential) {
			t.Fatalf("Credential error = %v, want ErrNoCredential", err)
		}
	})
}

func TestFactory_CredentialSpaceOverride(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_yyy")
	t.Setenv("OCTO_SPACE_ID", "space-env")

	f := NewDefaultFactory()
	f.Globals.Space = "space-flag"

	cred, err := f.Credential()
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.SpaceID != "space-flag" {
		t.Errorf("--space flag should override env, got %q", cred.SpaceID)
	}
}

func TestFactory_ExplicitBotIDAndEnvironmentClaimHaveDistinctEmptyStoreSemantics(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_bot")

	f := NewDefaultFactory()
	f.Globals.BotID = "claimed-bot"
	if _, err := f.Credential(); err == nil || !strings.Contains(err.Error(), "no profile found") {
		t.Fatalf("explicit --bot-id fell through to environment token: %v", err)
	}

	t.Setenv(config.EnvBotID, "claimed-bot")
	f = NewDefaultFactory()
	cred, err := f.Credential()
	if err != nil {
		t.Fatalf("environment Bot claim did not fall through: %v", err)
	}
	if cred.Token != "app_env_bot" || cred.RobotID != "claimed-bot" || cred.Profile != "" {
		t.Fatalf("environment credential = %+v", cred)
	}
}

func TestFactory_CredentialMissing(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "")

	f := NewDefaultFactory()
	_, err := f.Credential()
	if err == nil {
		t.Error("expected error when token is absent")
	}
}

func TestFactory_CredentialCaches(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_yyy")
	f := NewDefaultFactory()
	c1, err := f.Credential()
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	c2, _ := f.Credential()
	if c1 != c2 {
		t.Errorf("Credential should cache: %p vs %p", c1, c2)
	}
}

func TestFactory_ClientCaches(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_yyy")
	t.Setenv("OCTO_API_BASE_URL", "http://x")
	f := NewDefaultFactory()

	c1, err := f.Client()
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	c2, _ := f.Client()
	if c1 != c2 {
		t.Errorf("Client should cache: %p vs %p", c1, c2)
	}
}

func TestFactory_RegistryCaches(t *testing.T) {
	f := NewDefaultFactory()
	r1 := f.Registry()
	r2 := f.Registry()
	if r1 == nil {
		t.Fatal("Registry should be non-nil")
	}
	if r1 != r2 {
		t.Errorf("Registry should cache: %p vs %p", r1, r2)
	}
}

func TestFactory_RegistryNilFunc(t *testing.T) {
	f := &Factory{}
	if f.Registry() != nil {
		t.Error("Registry should be nil when RegistryFunc is nil")
	}
}

func TestFactory_Format_FlagWins(t *testing.T) {
	f := NewTestFactory()
	f.Globals.Format = "csv"
	if got := f.Format(); got != "csv" {
		t.Errorf("Format = %q, want csv", got)
	}
}

func TestFactory_Format_ConfigFallback(t *testing.T) {
	f := NewTestFactory()
	f.SetConfig(&config.Config{Format: "ndjson"})
	if got := f.Format(); got != "ndjson" {
		t.Errorf("Format = %q, want ndjson from config", got)
	}
}

func TestFactory_Format_DefaultsToJSON(t *testing.T) {
	// ConfigFunc returns an error → Format falls back to the JSON default.
	f := &Factory{
		Globals:    &GlobalOptions{},
		ConfigFunc: func() (*config.Config, error) { return nil, errors.New("no config") },
	}
	if got := f.Format(); got != output.FormatJSON {
		t.Errorf("Format = %q, want %q", got, output.FormatJSON)
	}
}

func TestFactory_EmitSuccess_EmitsEnvelope(t *testing.T) {
	f := NewTestFactory()
	raw := []byte(`{"id":"t1"}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(f.Out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, f.Out.String())
	}
	if env["ok"] != true {
		t.Errorf("ok = %v", env["ok"])
	}
	data, _ := env["data"].(map[string]any)
	if data["id"] != "t1" {
		t.Errorf("data.id = %v", data["id"])
	}
}

func TestFactory_EmitSuccess_FlattensPagination(t *testing.T) {
	f := NewTestFactory()
	raw := []byte(`{"data":[{"id":"a"}],"pagination":{"has_more":false}}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(f.Out.Bytes(), &env)
	items, _ := env["data"].([]any)
	if len(items) != 1 {
		t.Errorf("data len = %d", len(items))
	}
	if _, ok := env["_pagination"]; !ok {
		t.Errorf("_pagination missing")
	}
}

func TestFactory_EmitSuccess_JQFilter(t *testing.T) {
	f := NewTestFactory()
	f.Globals.JQ = ".data.id"
	raw := []byte(`{"id":"t1"}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	got := strings.TrimSpace(f.Out.String())
	// Format=json renders single jq result back to JSON scalar.
	if got != `"t1"` {
		t.Errorf("jq output = %q, want %q", got, `"t1"`)
	}
}

func TestFactory_EmitSuccess_TableFormat(t *testing.T) {
	f := NewTestFactory()
	f.Globals.Format = "table"
	raw := []byte(`{"data":[{"id":"t1","title":"x"}],"pagination":{}}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	out := f.Out.String()
	if !strings.Contains(out, "t1") || !strings.Contains(out, "id") {
		t.Errorf("table output missing content:\n%s", out)
	}
}

func TestFactory_EmitSuccess_NDJSONFormat(t *testing.T) {
	f := NewTestFactory()
	f.Globals.Format = "ndjson"
	raw := []byte(`{"data":[{"id":"a"},{"id":"b"},{"id":"c"}],"pagination":{}}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(f.Out.String()), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d: %q", len(lines), f.Out.String())
	}
}

func TestFactory_EmitSuccess_CSVFormat(t *testing.T) {
	f := NewTestFactory()
	f.Globals.Format = "csv"
	raw := []byte(`{"data":[{"id":"t1","title":"x"}],"pagination":{}}`)
	if err := f.EmitSuccess(raw); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	if !strings.Contains(f.Out.String(), "id") || !strings.Contains(f.Out.String(), "t1") {
		t.Errorf("csv output missing:\n%s", f.Out.String())
	}
}

func TestFactory_EmitSuccessWithMeta(t *testing.T) {
	f := NewTestFactory()
	meta := output.EnvelopeMeta{RateLimit: json.RawMessage(`{"remaining":5}`)}
	if err := f.EmitSuccessWithMeta([]byte(`{"id":"x"}`), meta); err != nil {
		t.Fatalf("EmitSuccessWithMeta: %v", err)
	}
	if !strings.Contains(f.Out.String(), "_rate_limit") {
		t.Errorf("meta missing:\n%s", f.Out.String())
	}
}

func TestFactory_EmitError_ExitError(t *testing.T) {
	f := NewTestFactory()
	ee := output.ErrValidation("bad input", "try again")
	if err := f.EmitError(ee); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	if !f.ErrorEmitted {
		t.Error("ErrorEmitted should be true after EmitError")
	}
	var env map[string]any
	if err := json.Unmarshal(f.ErrOut.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env["ok"] != false {
		t.Errorf("ok = %v", env["ok"])
	}
	errObj, _ := env["error"].(map[string]any)
	if errObj["type"] != "validation" {
		t.Errorf("type = %v", errObj["type"])
	}
}

func TestFactory_ErrorEmitted_InitiallyFalse(t *testing.T) {
	f := NewTestFactory()
	if f.ErrorEmitted {
		t.Error("ErrorEmitted should be false on new Factory")
	}
}

func TestFactory_EmitError_PlainErrorWrapped(t *testing.T) {
	f := NewTestFactory()
	if err := f.EmitError(errors.New("OCTO_BOT_TOKEN missing")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	var env map[string]any
	_ = json.Unmarshal(f.ErrOut.Bytes(), &env)
	errObj, _ := env["error"].(map[string]any)
	if errObj["type"] != "auth_error" {
		t.Errorf("token-related error should be auth_error, got %v", errObj["type"])
	}
}

func TestWrapCLIError_Nil(t *testing.T) {
	if WrapCLIError(nil) != nil {
		t.Error("nil in → nil out")
	}
}

func TestWrapCLIError_PassesThroughExitError(t *testing.T) {
	orig := output.ErrAuth("no token", "set it")
	got := WrapCLIError(orig)
	if ee := output.AsExitError(got); ee != orig {
		t.Errorf("existing *ExitError should pass through unchanged")
	}
}

func TestWrapCLIError_Heuristics(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		typ  string
	}{
		{"token-required", "bot token is required", "auth_error"},
		{"env-var", "OCTO_BOT_TOKEN missing", "auth_error"},
		{"unknown-flag", "unknown flag --wat", "validation"},
		{"unknown-command", "unknown command \"foo\"", "validation"},
		{"required-flag", "required flag(s) \"title\" not set", "validation"},
		{"invalid-arg", "invalid argument \"x\" for --count", "validation"},
		{"accepts", "accepts 2 arg(s)", "validation"},
		{"generic", "something else went wrong", "config"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WrapCLIError(errors.New(c.msg))
			ee := output.AsExitError(got)
			if ee == nil {
				t.Fatalf("expected *ExitError, got %T", got)
			}
			if ee.Type != c.typ {
				t.Errorf("Type = %q, want %q (msg=%q)", ee.Type, c.typ, c.msg)
			}
		})
	}
}

func TestFactory_OutErrOutAccessors(t *testing.T) {
	f := NewTestFactory()
	if f.Factory.Out() != f.Out {
		t.Error("Out() should match IOStreams.Out")
	}
	if f.Factory.ErrOut() != f.ErrOut {
		t.Error("ErrOut() should match IOStreams.ErrOut")
	}
}

// Ensure we don't regress the "Credential SpaceID zero-override" path: when
// Globals.Space is empty, the env-sourced SpaceID must remain intact.
func TestFactory_CredentialEmptySpaceDoesNotOverride(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "app_yyy")
	t.Setenv("OCTO_SPACE_ID", "space-env")
	f := NewDefaultFactory()
	// Globals.Space left empty intentionally.
	cred, err := f.Credential()
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.SpaceID != "space-env" {
		t.Errorf("empty Globals.Space shouldn't clear env SpaceID, got %q", cred.SpaceID)
	}
}

// Sanity check: the Factory's injected hooks surface BotCredential/Config
// without touching the env when replaced.
func TestFactory_InjectedHooksUsed(t *testing.T) {
	f := NewTestFactory()
	f.SetCredential(&credential.BotCredential{Token: "stub_tok"})
	cred, err := f.Credential()
	if err != nil {
		t.Fatalf("Credential: %v", err)
	}
	if cred.Token != "stub_tok" {
		t.Errorf("injected credential not used: %q", cred.Token)
	}
}

// TestFactory_IdentityEcho verifies mechanism B: when a credential is resolved,
// the success envelope's identity becomes an object describing the active bot.
func TestFactory_IdentityEcho(t *testing.T) {
	tf := NewTestFactory()
	tf.cred = &credential.BotCredential{
		Token: "app_x", Profile: "prod", RobotID: "cli_x", Source: "profile:prod",
	}
	if err := tf.EmitSuccess([]byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, tf.Out.String())
	}
	id, ok := env["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity is not an object: %v", env["identity"])
	}
	if id["profile"] != "prod" || id["robot_id"] != "cli_x" ||
		id["bot_kind"] != "app_bot" || id["source"] != "profile:prod" {
		t.Errorf("identity = %v", id)
	}
}

func TestFactory_IdentityEchoLabelsUnverifiedEnvironmentBotID(t *testing.T) {
	tf := NewTestFactory()
	tf.cred = &credential.BotCredential{
		Token: "app_x", RobotID: "caller-claimed", Source: "env:OCTO_BOT_TOKEN",
	}
	if err := tf.EmitSuccess([]byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, tf.Out.String())
	}
	id, ok := env["identity"].(map[string]any)
	if !ok {
		t.Fatalf("identity is not an object: %v", env["identity"])
	}
	if _, exists := id["robot_id"]; exists {
		t.Fatalf("unverified environment Bot id was echoed as identity: %v", id)
	}
	if id["robot_id_claimed"] != "caller-claimed" {
		t.Fatalf("unverified environment Bot id claim is not observable: %v", id)
	}
}

func saveBoundMailCredential(t *testing.T, store *authstore.Store, robotID, spaceID, botToken, mailToken string) string {
	t.Helper()
	apiOrigin := os.Getenv(config.EnvAPIBaseURL)
	if apiOrigin == "" {
		apiOrigin = config.DefaultAPIBaseURL
	}
	key, err := authstore.MailBindingKey(robotID, spaceID, botToken, apiOrigin)
	if err != nil {
		t.Fatalf("MailBindingKey: %v", err)
	}
	if err := store.SaveMailCredential(key, mailToken); err != nil {
		t.Fatalf("SaveMailCredential: %v", err)
	}
	return key
}

func TestFactory_MailCredentialRejectsDifferentAPIOrigin(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_only")
	t.Setenv(config.EnvBotID, "runtime-bot")
	t.Setenv(config.EnvSpaceID, "space-runtime")
	t.Setenv(config.EnvAPIBaseURL, "https://origin-b.example")

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	key, err := authstore.MailBindingKey(
		"runtime-bot", "space-runtime", "app_env_only", "https://origin-a.example",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveMailCredential(key, "omb_origin_a"); err != nil {
		t.Fatal(err)
	}

	f := NewDefaultFactory()
	cred, err := f.MailCredential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Agent Mail is not connected") {
		t.Fatalf("MailCredential = %+v, %v; want origin mismatch rejection", cred, err)
	}
	if f.mailCred != nil {
		t.Fatalf("mail credential was released across API origins: %+v", f.mailCred)
	}
}

// TestFactory_IdentityDefaultsToBot verifies the envelope emits the minimal
// identity object {type:bot} when no credential was resolved — the field is
// always an object, never a bare string.
func TestFactory_IdentityDefaultsToBot(t *testing.T) {
	tf := NewTestFactory()
	if err := tf.EmitSuccess([]byte(`{}`)); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	id, ok := env["identity"].(map[string]any)
	if !ok || id["type"] != "bot" {
		t.Errorf("identity = %v, want {type:bot}", env["identity"])
	}
}

func newBotIdentityTestServer(t *testing.T, wantToken, robotID string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Errorf("Authorization = %q, want token for %s", got, robotID)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"robot_id": robotID})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestFactory_MailCredentialFollowsSelectedBotProfile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(authstore.EnvConfigDir, dir)
	t.Setenv(config.EnvBotToken, "")
	t.Setenv(config.EnvAPIBaseURL, newBotIdentityTestServer(t, "app_bot_1", "bot-1").URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	if err := store.SaveProfile("agent", &authstore.ProfileMeta{RobotID: "bot-1", SpaceID: "space-1"}, "app_bot_1"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	saveBoundMailCredential(t, store, "bot-1", "space-1", "app_bot_1", "omb_mail_1")

	f := NewDefaultFactory()
	f.Globals.Profile = "agent"
	cred, err := f.MailCredential(context.Background())
	if err != nil {
		t.Fatalf("MailCredential: %v", err)
	}
	if cred.Token != "omb_mail_1" || cred.BotID != "bot-1" || cred.BotProfile != "agent" || cred.Source != "bot:bot-1/space-1" {
		t.Errorf("MailCredential = %+v", cred)
	}
}

func TestFactory_MailCredentialSupportsRuntimeBotIdentity(t *testing.T) {
	var identityCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityCalls++
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer app_env_only" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"robot_id":"runtime-bot"}`))
	}))
	defer srv.Close()

	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_only")
	t.Setenv(config.EnvBotID, "runtime-bot")
	t.Setenv(config.EnvSpaceID, "space-runtime")
	t.Setenv(config.EnvAPIBaseURL, srv.URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	saveBoundMailCredential(t, store, "runtime-bot", "space-runtime", "app_env_only", "omb_runtime_mail")

	f := NewDefaultFactory()
	cred, err := f.MailCredential(context.Background())
	if err != nil {
		t.Fatalf("MailCredential: %v", err)
	}
	if cred.Token != "omb_runtime_mail" || cred.BotID != "runtime-bot" || cred.BotProfile != "" {
		t.Errorf("MailCredential = %+v", cred)
	}
	if identityCalls != 0 {
		t.Fatalf("Bot identity calls = %d, want 0", identityCalls)
	}
	identity, ok := f.identityValue().(map[string]any)
	if !ok || identity["robot_id"] != "runtime-bot" {
		t.Fatalf("verified runtime identity echo = %#v", identity)
	}
}

func TestFactory_MailCredentialRejectsUnmatchedRuntimeTokenBinding(t *testing.T) {
	var identityCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityCalls++
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"robot_id":"actual-bot"}`))
	}))
	defer srv.Close()

	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_not_the_victim")
	t.Setenv(config.EnvBotID, "victim-bot")
	t.Setenv(config.EnvSpaceID, "space-victim")
	t.Setenv(config.EnvAPIBaseURL, srv.URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	saveBoundMailCredential(t, store, "victim-bot", "space-victim", "app_victim", "omb_victim_mail")

	f := NewDefaultFactory()
	cred, err := f.MailCredential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Agent Mail is not connected") {
		t.Fatalf("MailCredential = %+v, %v; want unmatched binding rejection", cred, err)
	}
	if identityCalls != 0 {
		t.Fatalf("Bot identity calls = %d, want 0", identityCalls)
	}
	if f.mailCred != nil {
		t.Fatalf("mail credential was released after identity mismatch: %+v", f.mailCred)
	}
}

func TestFactory_MailCredentialRejectsStoredProfileTokenSwap(t *testing.T) {
	var identityCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityCalls++
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer app_bot_b" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"robot_id":"bot-b"}`))
	}))
	defer srv.Close()

	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "")
	t.Setenv(config.EnvBotID, "")
	t.Setenv(config.EnvAPIBaseURL, srv.URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatalf("authstore.New: %v", err)
	}
	// Re-login can replace the token while retaining the profile's claimed
	// RobotID. The old mailbox credential must not be released because its
	// encrypted-store binding carries the previous token's fingerprint.
	if err := store.SaveProfile("agent", &authstore.ProfileMeta{RobotID: "bot-a", SpaceID: "space-a"}, "app_bot_b"); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	saveBoundMailCredential(t, store, "bot-a", "space-a", "app_bot_a", "omb_mail_of_bot_a")

	f := NewDefaultFactory()
	f.Globals.Profile = "agent"
	cred, err := f.MailCredential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Agent Mail is not connected") {
		t.Fatalf("MailCredential = %+v, %v; want stored-profile token swap rejection", cred, err)
	}
	if identityCalls != 0 {
		t.Fatalf("Bot identity calls = %d, want 0", identityCalls)
	}
	if f.mailCred != nil {
		t.Fatalf("mail credential was released after stored-profile identity mismatch: %+v", f.mailCred)
	}
}

func TestFactory_MailCredentialDryRunDoesNotVerifyRuntimeBotOverNetwork(t *testing.T) {
	var identityCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityCalls++
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"robot_id":"runtime-bot"}`))
	}))
	defer srv.Close()

	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_only")
	t.Setenv(config.EnvBotID, "runtime-bot")
	t.Setenv(config.EnvSpaceID, "space-runtime")
	t.Setenv(config.EnvAPIBaseURL, srv.URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	saveBoundMailCredential(t, store, "runtime-bot", "space-runtime", "app_env_only", "omb_runtime_mail")

	f := NewDefaultFactory()
	f.Globals.DryRun = true
	cred, err := f.MailCredential(context.Background())
	if err != nil {
		t.Fatalf("MailCredential: %v", err)
	}
	if cred.BotID != "runtime-bot" || cred.Token != "omb_runtime_mail" {
		t.Fatalf("MailCredential = %+v", cred)
	}
	if identityCalls != 0 {
		t.Fatalf("Bot identity calls = %d, want 0 during dry-run", identityCalls)
	}
}

func TestFactory_MailCredentialResolvesRuntimeBotIDFromToken(t *testing.T) {
	var identityCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identityCalls++
		if r.URL.Path != "/v1/bot/register" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer app_env_only" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"robot_id":"resolved-bot"}`))
	}))
	defer srv.Close()

	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_only")
	t.Setenv(config.EnvBotID, "")
	t.Setenv(config.EnvAPIBaseURL, srv.URL)

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	saveBoundMailCredential(t, store, "resolved-bot", "space-runtime", "app_env_only", "omb_runtime_mail")

	f := NewDefaultFactory()
	cred, err := f.MailCredential(context.Background())
	if err != nil {
		t.Fatalf("MailCredential: %v", err)
	}
	if cred.BotID != "resolved-bot" || cred.Token != "omb_runtime_mail" || cred.BotProfile != "" {
		t.Fatalf("MailCredential = %+v", cred)
	}
	if identityCalls != 0 {
		t.Fatalf("Bot identity calls = %d, want 0", identityCalls)
	}
}

func TestFactory_MailCredentialRequiresSpaceWhenTokenHasMultipleBindings(t *testing.T) {
	t.Setenv(authstore.EnvConfigDir, t.TempDir())
	t.Setenv(config.EnvBotToken, "app_env_only")
	t.Setenv(config.EnvBotID, "runtime-bot")
	t.Setenv(config.EnvSpaceID, "")

	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	saveBoundMailCredential(t, store, "runtime-bot", "space-a", "app_env_only", "omb_a")
	saveBoundMailCredential(t, store, "runtime-bot", "space-b", "app_env_only", "omb_b")

	f := NewDefaultFactory()
	cred, err := f.MailCredential(context.Background())
	if err == nil || !strings.Contains(err.Error(), "multiple Agent Mail connections") {
		t.Fatalf("MailCredential = %+v, %v; want Space ambiguity", cred, err)
	}

	f = NewDefaultFactory()
	f.Globals.Space = "space-b"
	cred, err = f.MailCredential(context.Background())
	if err != nil {
		t.Fatalf("MailCredential with --space: %v", err)
	}
	if cred.Token != "omb_b" || cred.BotID != "runtime-bot" {
		t.Fatalf("MailCredential with --space = %+v", cred)
	}
}
