package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestCatalogPackageRejectsOmittedDiscoveryOperation(t *testing.T) {
	files, _, baseline := retainedPackageFixture(t)
	const path = "modules/_global/0.json"
	module := packageTestObject(t, files[path])
	content := module["content"].([]any)
	old, replacement := "", ""
	for _, item := range content {
		record := item.(map[string]any)
		if record["kind"] != "discovery_index" {
			continue
		}
		old = record["sha256"].(string)
		rows := record["rows"].([]any)
		record["rows"] = rows[:len(rows)-1]
		delete(record, "sha256")
		replacement = sha256Sum(catalogCanonicalJSON(record))
		record["sha256"] = replacement
		break
	}
	if old == "" {
		t.Fatal("global discovery fixture missing")
	}
	key := func(item any) string {
		record := item.(map[string]any)
		if hash, ok := record["sha256"].(string); ok {
			return hash
		}
		return sha256Sum(catalogCanonicalJSON(record))
	}
	sort.Slice(content, func(i, j int) bool { return key(content[i]) < key(content[j]) })
	for _, item := range module["entries"].([]any) {
		refs := item.(map[string]any)["content_refs"].([]any)
		for i, ref := range refs {
			if ref == old {
				refs[i] = replacement
			}
		}
		sort.Slice(refs, func(i, j int) bool { return refs[i].(string) < refs[j].(string) })
	}
	files[path] = catalogCanonicalJSON(module)
	pin := commitPackageFixture(t, files, "")
	if _, err := permission.VerifyCatalogPackage(context.Background(), files, pin, baseline, PermissionCatalogSlotValues); err == nil {
		t.Fatal("discovery descriptor omitted a known operation but was accepted")
	}
}
