package mcp

import (
	"encoding/json"
	"testing"
)

func TestXIN2572WithheldClassification(t *testing.T) {
	var f exposureFile
	if err := json.Unmarshal(exposureJSON, &f); err != nil {
		t.Fatal(err)
	}
	ids := []string{"plugin.version.get", "plugin.version.download", "plugin.dependents.list", "plugin.graph.get"}
	if len(f.Withheld) != len(ids) {
		t.Fatalf("withheld count = %d, want %d", len(f.Withheld), len(ids))
	}
	for _, id := range ids {
		entry, ok := f.Withheld[id]
		if !ok || entry.Reason == "" || entry.UpstreamCommit != "e8f60c58a2c55b271690246beebedcaabafdd1b6" || entry.UpstreamPR != "https://github.com/Mininglamp-OSS/octo-cli/pull/216" {
			t.Errorf("%s: missing integration decision/provenance: %+v", id, entry)
		}
		if mcpExposed(id) {
			t.Errorf("%s: integration must not expand MCP exposure", id)
		}
	}
}

func TestXIN2572WithheldOverridesAllowedClasses(t *testing.T) {
	allowed := loadExposure([]byte(`{"baseline":["inherited","unchanged"],"approved":{"inherited":[]},"withheld":{"inherited":{"reason":"pending approval"}}}`))
	if allowed["inherited"] || !allowed["unchanged"] {
		t.Fatalf("withheld must win without denying unchanged operations: %v", allowed)
	}
}
