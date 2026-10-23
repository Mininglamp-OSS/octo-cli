package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func TestMarketplacePluginInstallAcceptsCustomEnvStringMap(t *testing.T) {
	wantEnv := map[string]string{"LOG_LEVEL": "debug", "REGION": "us-east-1"}
	var gotPath string
	var gotBody struct {
		PluginID  string            `json:"plugin_id"`
		Workspace string            `json:"workspace_id"`
		Runtime   string            `json:"runtime_id"`
		CustomEnv map[string]string `json:"custom_env"`
	}
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode install body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"agent_id":"agent-1"}}`))
	})

	root.SetArgs([]string{
		"marketplace", "plugin", "install",
		"--plugin-id", "plugin-1", "--workspace-id", "workspace-1", "--runtime-id", "runtime-1",
		"--data", `{"custom_env":{"LOG_LEVEL":"debug","REGION":"us-east-1"}}`,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute install with custom_env: %v", err)
	}
	if gotPath != "/market/api/v1/plugins/install" {
		t.Errorf("path = %q, want /market/api/v1/plugins/install", gotPath)
	}
	if gotBody.PluginID != "plugin-1" || gotBody.Workspace != "workspace-1" || gotBody.Runtime != "runtime-1" {
		t.Errorf("install identifiers = (%q, %q, %q)", gotBody.PluginID, gotBody.Workspace, gotBody.Runtime)
	}
	if len(gotBody.CustomEnv) != len(wantEnv) {
		t.Fatalf("custom_env = %#v, want %#v", gotBody.CustomEnv, wantEnv)
	}
	for key, want := range wantEnv {
		if got := gotBody.CustomEnv[key]; got != want {
			t.Errorf("custom_env[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestMarketplacePluginInstallDryRunDoesNotLeakCustomEnvValues(t *testing.T) {
	const secret = "custom-env-secret-9d37"
	root, tf, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("dry-run must not send an HTTP request")
	})
	cfg := &config.Config{APIBaseURL: "https://market.example", BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.Globals.DryRun = true
	tf.SetClient(client.New(cfg, cred, client.Options{DryRun: true, ErrOut: tf.ErrOut}))

	root.SetArgs([]string{
		"marketplace", "plugin", "install",
		"--plugin-id", "plugin-1", "--workspace-id", "workspace-1", "--runtime-id", "runtime-1",
		"--data", `{"custom_env":{"API_TOKEN":"` + secret + `"}}`,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute install dry-run: %v (stderr: %s)", err, tf.ErrOut.String())
	}

	stdout, stderr := tf.Out.String(), tf.ErrOut.String()
	if strings.Contains(stdout, secret) {
		t.Errorf("stdout exposed a custom_env value: %s", stdout)
	}
	if strings.Contains(stderr, secret) {
		t.Errorf("stderr exposed a custom_env value: %s", stderr)
	}

	var envelope struct {
		Data struct {
			Body map[string]any `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout)
	}
	customEnv, ok := envelope.Data.Body["custom_env"].(map[string]any)
	if !ok {
		t.Fatalf("dry-run body omitted custom_env: %#v", envelope.Data.Body)
	}
	if _, ok := customEnv["API_TOKEN"]; !ok {
		t.Errorf("dry-run body omitted API_TOKEN: %#v", customEnv)
	}
}
