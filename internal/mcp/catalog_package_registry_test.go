package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func fixtureInteger(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			panic(err)
		}
		return int(i)
	default:
		panic("unexpected fixture integer")
	}
}

func repartitionPackageFixture(t *testing.T, original map[string][]byte) map[string][]byte {
	t.Helper()
	files := clonePackageFiles(original)
	paths := []string{}
	for path := range files {
		if strings.HasPrefix(path, "blobs/") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	pool := []byte{}
	base := map[string]int{}
	for _, path := range paths {
		base[path] = len(pool)
		pool = append(pool, files[path]...)
		delete(files, path)
	}
	for at, n := 0, 0; at < len(pool); n++ {
		end := min(len(pool), at+524288)
		files[fmt.Sprintf("blobs/%d.bin", n)] = append([]byte(nil), pool[at:end]...)
		at = end
	}
	roots, rows := []map[string]any{}, []map[string]any{}
	paths = nil
	for path := range files {
		if strings.HasPrefix(path, "shared/") {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		m := packageTestObject(t, files[path])
		for _, item := range m["roots"].([]any) {
			root := item.(map[string]any)
			parts := []map[string]any{}
			for _, item := range root["parts"].([]any) {
				part := item.(map[string]any)
				offset := base[part["path"].(string)] + fixtureInteger(part["offset"])
				left := fixtureInteger(part["length"])
				for left > 0 {
					inside := offset % 524288
					n := min(left, 524288-inside)
					parts = append(parts, map[string]any{"path": fmt.Sprintf("blobs/%d.bin", offset/524288), "offset": inside, "length": n})
					offset += n
					left -= n
				}
			}
			root["parts"] = parts
			roots = append(roots, root)
		}
		for _, row := range m["rows"].([]any) {
			rows = append(rows, row.(map[string]any))
		}
		delete(files, path)
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i]["sha256"].(string) < roots[j]["sha256"].(string) })
	sort.Slice(rows, func(i, j int) bool { return rows[i]["sha256"].(string) < rows[j]["sha256"].(string) })
	if err := writeSharedShards(files, roots, rows); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCatalogRegistryAtomicDirectedSemanticEquivalence(t *testing.T) {
	files, pin, baseline := retainedPackageFixture(t)
	source, err := permission.VerifyCatalogPackage(context.Background(), files, pin, baseline, PermissionCatalogSlotValues)
	if err != nil {
		t.Fatal(err)
	}
	repacked := repartitionPackageFixture(t, files)
	targetPin := commitPackageFixture(t, repacked, "fixture-equivalent-package-v2")
	target, err := permission.VerifyCatalogPackage(context.Background(), repacked, targetPin, baseline, PermissionCatalogSlotValues)
	if err != nil {
		t.Fatal("positive immutable repartition failed", err)
	}
	if !source.SameSemantics(target) || source.SemanticDigest() != target.SemanticDigest() {
		t.Fatal("physical recipes leaked into semantic compatibility")
	}
	registry, err := permission.NewCatalogRegistry([]permission.CatalogPin{pin, targetPin}, []permission.DirectedCatalogPair{{From: pin, To: targetPin}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Lookup(pin); ok {
		t.Fatal("cold registry fabricated a recovered positive snapshot")
	}
	if err = registry.Install(context.Background(), []*permission.VerifiedCatalog{source, target}); err != nil {
		t.Fatal(err)
	}
	if !registry.Compatible(pin, targetPin) || registry.Compatible(targetPin, pin) {
		t.Fatal("directed migration permission was inferred/reversed")
	}
	if err = registry.Install(context.Background(), []*permission.VerifiedCatalog{source}); err == nil {
		t.Fatal("partial registry installation accepted")
	}
	if got, ok := registry.Lookup(targetPin); !ok || got.Pin() != targetPin {
		t.Fatal("failed install replaced old complete snapshot")
	}
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for n := 0; n < 20; n++ {
				if _, ok := registry.Lookup(pin); !ok {
					t.Error("concurrent partial visibility")
				}
				if err := registry.Install(context.Background(), []*permission.VerifiedCatalog{target, source}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wait.Wait()
	changed := clonePackageFiles(files)
	index := packageTestObject(t, changed["index.json"])
	for _, item := range index["profiles"].([]any) {
		p := item.(map[string]any)
		if p["name"] == "leaf" {
			p["limits"].(map[string]any)["local_input_bytes"] = 16777215
		}
	}
	changed["index.json"] = catalogCanonicalJSON(index)
	changedPin := commitPackageFixture(t, changed, "fixture-changed-profile-v2")
	different, err := permission.VerifyCatalogPackage(context.Background(), changed, changedPin, baseline, PermissionCatalogSlotValues)
	if err != nil {
		t.Fatal(err)
	}
	if source.SameSemantics(different) {
		t.Fatal("same IDs concealed changed permission-profile meaning")
	}
	bad, err := permission.NewCatalogRegistry([]permission.CatalogPin{pin, changedPin}, []permission.DirectedCatalogPair{{From: pin, To: changedPin}})
	if err != nil {
		t.Fatal(err)
	}
	if err = bad.Install(context.Background(), []*permission.VerifiedCatalog{source, different}); err == nil {
		t.Fatal("author-chosen pair bypassed full semantic equivalence")
	}
	if _, ok := bad.Lookup(pin); ok {
		t.Fatal("invalid pair published part of proposed registry")
	}
	unknown := pin
	unknown.Digest = strings.Repeat("0", 64)
	if _, ok := registry.Lookup(unknown); ok {
		t.Fatal("unknown current pin fell back to cached old version")
	}
	detached, _ := source.File("index.json")
	detached[0] ^= 1
	original, _ := source.File("index.json")
	if sha256Sum(original) != sha256Sum(files["index.json"]) {
		t.Fatal("caller mutated immutable snapshot bytes")
	}
}
