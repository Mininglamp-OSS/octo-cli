package mcp

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func retainedPackageFixture(t *testing.T) (map[string][]byte, permission.CatalogPin, permission.CatalogBaseline) {
	t.Helper()
	files := map[string][]byte{}
	root := "../../docs/permission/catalog-package-v2"
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		files[filepath.ToSlash(name)] = raw
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	m := packageTestObject(t, files["manifest.json"])
	baseline, err := PermissionCatalogBaseline()
	if err != nil {
		t.Fatal(err)
	}
	return files, permission.CatalogPin{Version: m["catalog_version"].(string), Digest: sha256Sum(files["manifest.json"])}, baseline
}
func packageTestObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	var value map[string]any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}
func clonePackageFiles(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for path, raw := range files {
		out[path] = append([]byte(nil), raw...)
	}
	return out
}
func commitPackageFixture(t *testing.T, files map[string][]byte, version string) permission.CatalogPin {
	t.Helper()
	manifest := packageTestObject(t, files["manifest.json"])
	if version != "" {
		manifest["catalog_version"] = version
	}
	paths := []string{}
	for path := range files {
		if path != "manifest.json" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	inventory := []map[string]any{}
	for _, path := range paths {
		kind := "module"
		switch {
		case path == "index.json":
			kind = "index"
		case strings.HasPrefix(path, "shared/"):
			kind = "shared"
		case strings.HasPrefix(path, "blobs/"):
			kind = "blob"
		}
		inventory = append(inventory, map[string]any{"path": path, "kind": kind, "bytes": len(files[path]), "sha256": sha256Sum(files[path])})
	}
	manifest["files"] = inventory
	files["manifest.json"] = catalogCanonicalJSON(manifest)
	return permission.CatalogPin{Version: manifest["catalog_version"].(string), Digest: sha256Sum(files["manifest.json"])}
}

func TestCatalogPackageRejectsCompleteCommitmentAndGraphFaults(t *testing.T) {
	original, pin, baseline := retainedPackageFixture(t)
	cases := map[string]func(map[string][]byte){
		"unlisted_file": func(f map[string][]byte) { f["extra.json"] = []byte(`{}`) },
		"missing_file":  func(f map[string][]byte) { delete(f, "index.json") },
		"tampered_blob": func(f map[string][]byte) { f["blobs/3.bin"][0] ^= 1 },
		"decoded_duplicate_key": func(f map[string][]byte) {
			f["index.json"] = append([]byte(`{"kind":"index",`), f["index.json"][1:]...)
		},
		"static_gap": func(f map[string][]byte) {
			m := packageTestObject(t, f["index.json"])
			binding := m["static_bindings"].([]any)[0].(map[string]any)
			binding["selector"].(map[string]any)["managed_bits"] = 254
			f["index.json"] = catalogCanonicalJSON(m)
		},
		"static_overlap": func(f map[string][]byte) {
			m := packageTestObject(t, f["index.json"])
			bindings := m["static_bindings"].([]any)
			first := packageTestObject(t, catalogCanonicalJSON(bindings[0]))
			first["selector"].(map[string]any)["managed_bits"] = 1
			bindings = append([]any{first}, bindings...)
			m["static_bindings"] = bindings
			f["index.json"] = catalogCanonicalJSON(m)
		},
		"orphan_empty_root": func(f map[string][]byte) {
			path := "shared/06.json"
			m := packageTestObject(t, f[path])
			roots := m["roots"].([]any)
			roots = append(roots, map[string]any{"sha256": sha256Sum(nil), "length": 0, "parts": []any{}})
			sort.Slice(roots, func(i, j int) bool {
				return roots[i].(map[string]any)["sha256"].(string) < roots[j].(map[string]any)["sha256"].(string)
			})
			m["roots"] = roots
			f[path] = catalogCanonicalJSON(m)
		},
		"wrong_part_type": func(f map[string][]byte) {
			path := "shared/00.json"
			m := packageTestObject(t, f[path])
			part := m["roots"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
			part["path"] = "shared/00.json"
			f[path] = catalogCanonicalJSON(m)
		},
		"null_part_path": func(f map[string][]byte) {
			path := "shared/00.json"
			m := packageTestObject(t, f[path])
			part := m["roots"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
			part["path"] = nil
			f[path] = catalogCanonicalJSON(m)
		},
		"part_overflow": func(f map[string][]byte) {
			path := "shared/00.json"
			m := packageTestObject(t, f[path])
			part := m["roots"].([]any)[0].(map[string]any)["parts"].([]any)[0].(map[string]any)
			part["offset"] = 1048576
			f[path] = catalogCanonicalJSON(m)
		},
		"index_only_descriptor_missing": func(f map[string][]byte) {
			path := "modules/_global/0.json"
			m := packageTestObject(t, f[path])
			content := m["content"].([]any)
			hash := baseline.SourceFiles[0][1]
			kept := []any{}
			for _, c := range content {
				if c.(map[string]any)["sha256"] != hash {
					kept = append(kept, c)
				}
			}
			if len(kept) == len(content) {
				t.Fatal("index-only fixture descriptor not found")
			}
			m["content"] = kept
			f[path] = catalogCanonicalJSON(m)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			files := clonePackageFiles(original)
			mutate(files)
			expected := pin
			if name != "unlisted_file" && name != "missing_file" && name != "tampered_blob" {
				expected = commitPackageFixture(t, files, "")
			}
			if _, err := permission.VerifyCatalogPackage(context.Background(), files, expected, baseline, PermissionCatalogSlotValues); err == nil {
				t.Fatal("invalid complete package was accepted", name)
			}
		})
	}
}

func TestCatalogPackageDirectoryRejectsAliasesAndMutableFiles(t *testing.T) {
	files, pin, baseline := retainedPackageFixture(t)
	dir := t.TempDir()
	for path, raw := range files {
		target := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0o400); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := permission.ReadCatalogPackageDirectory(ctx, dir, pin, baseline, PermissionCatalogSlotValues); err != nil {
		t.Fatal("complete immutable directory refused", err)
	}
	catalog, snapshot, err := readPermissionRelease(ctx, filepath.Join(dir, "manifest.json"), pin.Digest, map[string]permission.CatalogPin{pin.Digest: pin})
	if err != nil || snapshot == nil || catalog.Digest != pin.Digest {
		t.Fatal("private v2 bootstrap failed before readiness", err)
	}
	if _, _, err := readPermissionRelease(ctx, filepath.Join(dir, "manifest.json"), pin.Digest, supportedPermissionPackages); err == nil {
		t.Fatal("operator digest promoted a candidate without compiled support")
	}
	t.Run("writable", func(t *testing.T) {
		path := filepath.Join(dir, "index.json")
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0o400)
		if _, err := permission.ReadCatalogPackageDirectory(ctx, dir, pin, baseline, PermissionCatalogSlotValues); err == nil {
			t.Fatal("mutable package file accepted")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Link(filepath.Join(dir, "index.json"), alias); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(alias)
		if _, err := permission.ReadCatalogPackageDirectory(ctx, dir, pin, baseline, PermissionCatalogSlotValues); err == nil {
			t.Fatal("hardlinked package file accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		alias := filepath.Join(dir, "alias")
		if err := os.Symlink("index.json", alias); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(alias)
		if _, err := permission.ReadCatalogPackageDirectory(ctx, dir, pin, baseline, PermissionCatalogSlotValues); err == nil {
			t.Fatal("symlinked extra package file accepted")
		}
	})
}
