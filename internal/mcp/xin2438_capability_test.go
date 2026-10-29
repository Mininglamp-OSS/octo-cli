package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestXIN2438ExecuteDryRunMasksCapabilitySecrets drives the same masking
// through the MCP execute dry run, whose envelope echoes the request body.
func TestXIN2438ExecuteDryRunMasksCapabilitySecrets(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	var args map[string]any
	if err := json.Unmarshal([]byte(`{"X-Workspace-ID":"22222222-2222-4222-8222-222222222222","Idempotency-Key":"k1",`+
		`"definition":{"schema_version":"1.0","name":"pack","experts":[{"name":"e1","custom_env":{"API_KEY":"canary-expert-env-value"},`+
		`"mcp_config":{"mcpServers":{"remote":{"url":"https://mcp.example.test/canary-url","headers":{"Authorization":"Bearer canary-header-value"}}}}}]}}`), &args); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, "execute", map[string]any{"operation_id": "capability.install", "dry_run": true, "arguments": args})
	text := res.Content[0].Text
	if res.IsError || !strings.Contains(text, "REDACTED") {
		t.Fatalf("dry run should succeed with redactions: %s", text)
	}
	for _, canary := range []string{"canary-expert-env-value", "canary-url", "canary-header-value"} {
		if strings.Contains(text, canary) {
			t.Errorf("execute dry run leaked %q: %s", canary, text)
		}
	}
	if be.path != "" {
		t.Errorf("dry run reached the backend: %s", be.path)
	}
}
