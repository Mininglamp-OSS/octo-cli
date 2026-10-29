package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// XIN-2441: a backend may bind a nested capability property under any case-fold
// spelling, so a secret written as HEADERS, Url or with a Unicode special fold
// (U+017F long s, U+212A Kelvin sign) anywhere on the secret path must be
// masked in diagnostics exactly like the canonical spelling, while the request
// still carries what the caller wrote.

// capabilityAliasInstall is accepted by local validation: the expert object
// is closed, but each MCP server object is open, so every secret there sits
// under a noncanonical spelling (HEADERS, URL, Headers, Env, and Unicode
// special folds) across two experts and dynamic server names.
const capabilityAliasInstall = `{"definition":{"schema_version":"1.0","name":"pack","experts":[` +
	`{"name":"e1","custom_env":{"API_KEY":"CANARY_ALIAS_CUSTOM_ENV"},` +
	`"mcp_config":{"mcpServers":{"remote":{"url":"https://example.invalid/mcp","HEADERS":{"Authorization":"Bearer CANARY_ALIAS_HEADERS"}},` +
	`"remote2":{"URL":"https://example.invalid/CANARY_ALIAS_URL","Headers":{"X-Api-Key":"CANARY_ALIAS_MIXED_HEADERS"}},` +
	`"local":{"command":"npx","Env":{"TOKEN":"CANARY_ALIAS_ENV"}}}}},` +
	`{"name":"e2","mcp_config":{"mcpServers":{"r":{"UR\u029f":"x","url":"https://example.invalid/CANARY_ALIAS_CANONICAL_URL","header\u017f":{"A":"CANARY_ALIAS_UNICODE_HEADERS"},"\u212aEEP":"visible-nonsecret"}}}}` +
	`]},"bindings":{"experts":[{"expert_name":"e1","runtime_id":"11111111-1111-4111-8111-111111111111"}]}}`

// capabilityAliasOuterSteps spells the closed-object steps of the secret paths
// noncanonically (custom_ENV, MCP_CONFIG, mcpServer + long s). Local
// validation refuses it; the collector still finds every secret so no path
// can print one.
const capabilityAliasOuterSteps = `{"definition":{"schema_version":"1.0","name":"pack","experts":[` +
	`{"name":"e1","custom_ENV":{"API_KEY":"CANARY_OUTER_CUSTOM_ENV"},` +
	`"MCP_CONFIG":{"mcpServer\u017f":{"r":{"url":"https://example.invalid/CANARY_OUTER_URL","Header\u017f":{"A":"CANARY_OUTER_HEADERS"}}}}}` +
	`]},"bindings":{"experts":[{"expert_name":"e1","runtime_id":"11111111-1111-4111-8111-111111111111"}]}}`

var capabilityOuterCanaries = []string{"CANARY_OUTER_CUSTOM_ENV", "CANARY_OUTER_URL", "CANARY_OUTER_HEADERS"}

var capabilityAliasCanaries = []string{
	"CANARY_ALIAS_CUSTOM_ENV",
	"CANARY_ALIAS_HEADERS",
	"CANARY_ALIAS_URL",
	"CANARY_ALIAS_MIXED_HEADERS",
	"CANARY_ALIAS_ENV",
	"CANARY_ALIAS_CANONICAL_URL",
	"CANARY_ALIAS_UNICODE_HEADERS",
}

// TestXIN2441CollectsCaseFoldSecretPaths isolates the collector: every
// case-fold spelling of a secret path is collected, and nonsecret values that
// merely sit next to them are not.
func TestXIN2441CollectsCaseFoldSecretPaths(t *testing.T) {
	op, _ := registry.MustNew().GetOperation("capability.install")
	var body any
	if err := json.Unmarshal([]byte(capabilityAliasInstall), &body); err != nil {
		t.Fatal(err)
	}
	got, err := CollectRequestSecrets(op, body)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	joined := strings.Join(got, "\n")
	for _, c := range capabilityAliasCanaries {
		if !strings.Contains(joined, c) {
			t.Errorf("secret %q was not collected; got %q", c, got)
		}
	}
	for _, v := range got {
		if v == "npx" || v == "e1" || v == "pack" || v == "visible-nonsecret" {
			t.Errorf("nonsecret value %q was collected", v)
		}
	}

	if err := json.Unmarshal([]byte(capabilityAliasOuterSteps), &body); err != nil {
		t.Fatal(err)
	}
	got, err = CollectRequestSecrets(op, body)
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(got, "\n")
	for _, c := range capabilityOuterCanaries {
		if !strings.Contains(joined, c) {
			t.Errorf("secret %q under a noncanonical outer step was not collected; got %q", c, got)
		}
	}
}

// TestXIN2441CapabilityOuterAliasRefusedWithoutLeak: noncanonical spellings of
// the closed expert properties are refused locally, and neither the refusal
// nor anything else printed carries a secret value.
func TestXIN2441CapabilityOuterAliasRefusedWithoutLeak(t *testing.T) {
	for _, dry := range []bool{true, false} {
		root, tf := capabilityRootData(t, "http://127.0.0.1:9", client.Options{DryRun: dry, NoRetry: true, Verbose: true}, dry, capabilityAliasOuterSteps)
		err := root.Execute()
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("dry=%v: noncanonical closed-object steps must be refused, got %v", dry, err)
		}
		all := tf.Out.String() + tf.ErrOut.String() + err.Error()
		for _, c := range capabilityOuterCanaries {
			if strings.Contains(all, c) {
				t.Errorf("dry=%v: refusal leaked %q: %s", dry, c, all)
			}
		}
	}
}

// TestXIN2441DeclaredSecretFoldVariant covers schema-declared secrets reached
// through the resolved RequestBody tree, not RequestSecretPaths: a fold
// variant of a declared secret is masked, and an exact declared nonsecret
// property is not swept in by a secret that differs from it only by case.
func TestXIN2441DeclaredSecretFoldVariant(t *testing.T) {
	schema := &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{
		"password": {Type: "string", Secret: true},
		"note":     {Type: "string"},
		"Token":    {Type: "string"},
		"token":    {Type: "string", Secret: true},
	}}
	got, err := CollectBodySecrets(schema, map[string]any{
		"PASSWORD": "pw-ascii", "paſſword": "pw-unicode", "note": "visible", "Token": "exact-nonsecret", "TOKEN": "tok-alias",
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "pw-ascii,pw-unicode,tok-alias" {
		t.Errorf("collected %v, want [pw-ascii pw-unicode tok-alias]", got)
	}
}

func capabilityRoot(t *testing.T, apiBase string, opts client.Options, dryRun bool) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	return capabilityRootData(t, apiBase, opts, dryRun, capabilityAliasInstall)
}

func capabilityRootData(t *testing.T, apiBase string, opts client.Options, dryRun bool, data string) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: apiBase, BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.Globals.DryRun = dryRun
	if opts.ErrOut == nil {
		opts.ErrOut = tf.ErrOut
	}
	tf.SetClient(client.New(cfg, cred, opts))
	tf.RegistryFunc = registry.MustNew
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"loop", "capability", "install", "--workspace-id", "22222222-2222-4222-8222-222222222222",
		"--idempotency-key", "k1", "--data", data})
	return root, tf
}

func TestXIN2441CapabilityAliasDryRunMasked(t *testing.T) {
	root, tf := capabilityRoot(t, "https://octo.test", client.Options{DryRun: true, ErrOut: io.Discard}, true)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr %s)", err, tf.ErrOut.String())
	}
	all := tf.Out.String() + tf.ErrOut.String()
	for _, c := range capabilityAliasCanaries {
		if strings.Contains(all, c) {
			t.Errorf("dry-run output leaked %q", c)
		}
	}
	if !strings.Contains(all, "npx") || !strings.Contains(all, "REDACTED") {
		t.Errorf("dry-run must keep nonsecret values and show redactions: %s", all)
	}
}

func TestXIN2441CapabilityAliasVerboseMaskedButSent(t *testing.T) {
	var mu sync.Mutex
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = raw
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"type":"expert","experts":[],"skills":[]}}`))
	}))
	t.Cleanup(srv.Close)

	root, tf := capabilityRoot(t, srv.URL, client.Options{NoRetry: true, Verbose: true}, false)
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr %s)", err, tf.ErrOut.String())
	}
	trace := tf.ErrOut.String()
	if !strings.Contains(trace, "capabilities/install") {
		t.Fatalf("verbose trace missing the request: %s", trace)
	}
	for _, c := range capabilityAliasCanaries {
		if strings.Contains(trace+tf.Out.String(), c) {
			t.Errorf("verbose output leaked %q", c)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	var got, want any
	if err := json.Unmarshal(sent, &got); err != nil {
		t.Fatalf("outbound body: %v (%s)", err, sent)
	}
	if err := json.Unmarshal([]byte(capabilityAliasInstall), &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Errorf("outbound body changed:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}
