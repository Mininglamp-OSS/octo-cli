package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// XIN-2438: capability.install carries expert environment maps and MCP server
// env/header maps and URLs, all described as sensitive. They sit deeper than
// the resolved RequestBody tree reaches and inside dynamic maps and a oneOf, so
// masking must find them through RequestSecretPaths.

var capabilitySecretCanaries = []string{
	"canary-expert-env-value",
	"canary-stdio-env-value",
	"Bearer canary-remote-header-value",
	"https://mcp.example.test/canary-remote-url-token",
}

const capabilityInstallWithSecrets = `{"definition":{"schema_version":"1.0","name":"pack","experts":[{"name":"e1",` +
	`"custom_env":{"API_KEY":"canary-expert-env-value"},` +
	`"mcp_config":{"mcpServers":{"local":{"command":"npx","env":{"TOKEN":"canary-stdio-env-value"}},` +
	`"remote":{"type":"http","url":"https://mcp.example.test/canary-remote-url-token","headers":{"Authorization":"Bearer canary-remote-header-value"}}}}}]},` +
	`"bindings":{"experts":[{"expert_name":"e1","runtime_id":"11111111-1111-4111-8111-111111111111"}]}}`

func TestXIN2438CapabilityInstallSecretPaths(t *testing.T) {
	op, ok := registry.MustNew().GetOperation("capability.install")
	if !ok {
		t.Fatal("capability.install not found")
	}
	want := []string{
		"definition.experts[].custom_env",
		"definition.experts[].mcp_config.mcpServers.*.env",
		"definition.experts[].mcp_config.mcpServers.*.headers",
		"definition.experts[].mcp_config.mcpServers.*.url",
	}
	if strings.Join(op.RequestSecretPaths, ",") != strings.Join(want, ",") {
		t.Fatalf("RequestSecretPaths = %v, want %v", op.RequestSecretPaths, want)
	}
}

func TestXIN2438CapabilityInstallDryRunMasksNestedSecrets(t *testing.T) {
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: "https://octo.test", BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.Globals.DryRun = true
	tf.SetClient(client.New(cfg, cred, client.Options{DryRun: true, ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)

	root.SetArgs([]string{"loop", "capability", "install", "--workspace-id", "22222222-2222-4222-8222-222222222222",
		"--idempotency-key", "k1", "--data", capabilityInstallWithSecrets})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr %s)", err, tf.ErrOut.String())
	}
	out := tf.Out.String()
	for _, canary := range capabilitySecretCanaries {
		if strings.Contains(out, canary) {
			t.Errorf("dry-run output leaked %q: %s", canary, out)
		}
	}
	// Non-secret configuration stays readable.
	if !strings.Contains(out, "npx") || !strings.Contains(out, "REDACTED") {
		t.Errorf("dry-run output should keep the command and show redactions: %s", out)
	}
}

func TestXIN2438CapabilityInstallVerboseMasksButSendsSecrets(t *testing.T) {
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

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{NoRetry: true, Verbose: true, ErrOut: tf.ErrOut}))
	tf.RegistryFunc = registry.MustNew
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)

	root.SetArgs([]string{"loop", "capability", "install", "--workspace-id", "22222222-2222-4222-8222-222222222222",
		"--idempotency-key", "k1", "--data", capabilityInstallWithSecrets})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr %s)", err, tf.ErrOut.String())
	}
	trace := tf.ErrOut.String()
	if !strings.Contains(trace, "capabilities/install") {
		t.Fatalf("verbose trace missing the request: %s", trace)
	}
	for _, canary := range capabilitySecretCanaries {
		if strings.Contains(trace, canary) {
			t.Errorf("verbose trace leaked %q: %s", canary, trace)
		}
	}

	// Masking is presentation only: the backend still receives the request
	// exactly as written.
	mu.Lock()
	defer mu.Unlock()
	var got, want any
	if err := json.Unmarshal(sent, &got); err != nil {
		t.Fatalf("outbound body: %v (%s)", err, sent)
	}
	if err := json.Unmarshal([]byte(capabilityInstallWithSecrets), &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("outbound body changed:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestXIN2438CollectBodySecretPaths(t *testing.T) {
	body := map[string]any{"a": []any{
		map[string]any{"m": map[string]any{"x": map[string]any{"env": map[string]any{"K": "v1"}, "url": "u1"}}},
		map[string]any{"m": map[string]any{"y": map[string]any{"url": ""}}},
	}}
	got, err := CollectBodySecretPaths([]string{"a[].m.*.env", "a[].m.*.url"}, body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "v1,u1" {
		t.Errorf("collected %v, want [v1 u1]", got)
	}
	// A value that string masking cannot cover is refused, not printed.
	if _, err := CollectBodySecretPaths([]string{"a[].m.*.url"}, map[string]any{"a": []any{
		map[string]any{"m": map[string]any{"x": map[string]any{"url": 42.0}}},
	}}); err == nil || !strings.Contains(err.Error(), "a[0].m.x.url is declared secret") {
		t.Errorf("non-string secret error = %v", err)
	}
}
