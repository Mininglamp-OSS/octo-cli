package cmdutil

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// TestMain isolates the credential store for the whole package: NewDefaultFactory
// now resolves credentials through the on-disk store first, so env-based tests
// must not see a developer's real ~/.octo-cli. An empty OCTO_CONFIG_DIR yields
// zero profiles, so resolution falls through to OCTO_BOT_TOKEN as these tests
// expect. Individual tests may still override OCTO_CONFIG_DIR via t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octo-cmdutil-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("OCTO_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
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
	if f.ConfigFunc == nil || f.CredentialFunc == nil || f.ClientFunc == nil || f.RegistryFunc == nil {
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
	tf.Factory.cred = &credential.BotCredential{
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

// TestFactory_IdentityDefaultsToBot verifies the envelope keeps the plain "bot"
// tag when no credential was resolved (e.g. unauthenticated diagnostic commands).
func TestFactory_IdentityDefaultsToBot(t *testing.T) {
	tf := NewTestFactory()
	if err := tf.EmitSuccess([]byte(`{}`)); err != nil {
		t.Fatalf("EmitSuccess: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env["identity"] != "bot" {
		t.Errorf("identity = %v, want \"bot\"", env["identity"])
	}
}
