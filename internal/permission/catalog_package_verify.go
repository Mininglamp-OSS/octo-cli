package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func catalogNumber(v any) int {
	n, ok := v.(json.Number)
	if !ok {
		return -1
	}
	i, err := strconv.Atoi(string(n))
	if err != nil {
		return -1
	}
	return i
}
func catalogArray(v any) []any { a, _ := v.([]any); return a }
func catalogStringSet(values []string) map[string]bool {
	m := map[string]bool{}
	for _, v := range values {
		m[v] = true
	}
	return m
}
func catalogInvalid(reason string) error { return fmt.Errorf("catalog %s: %w", reason, ErrInvalid) }

func (v *VerifiedCatalog) verifyFiles(ctx context.Context, files map[string][]byte, baseline CatalogBaseline) error { //nolint:gocyclo // complete inventory validation precedes any snapshot publication
	if !validHex(v.pin.Digest, 64) || !asciiToken(v.pin.Version, 64, false) || len(baseline.Entries) != 827 || len(files) > 64 {
		return catalogInvalid("private pin/baseline")
	}
	manifest, ok := files["manifest.json"]
	if !ok || len(manifest) > 65536 {
		return catalogInvalid("manifest bytes")
	}
	manifest = append([]byte(nil), manifest...)
	if !ok || len(manifest) > 65536 || catalogHash(manifest) != v.pin.Digest {
		return catalogInvalid("manifest commitment")
	}
	if err := ValidateCatalogDefinition("Manifest", manifest); err != nil {
		return err
	}
	decoded, work, err := catalogJSONValue(manifest, true)
	if err != nil {
		return err
	}
	v.manifest = object(decoded)
	if v.manifest["catalog_version"] != v.pin.Version {
		return catalogInvalid("manifest version")
	}
	v.ledger = CatalogLedger{Files: len(files), StoredBytes: len(manifest), MetadataNodes: work.Nodes, MaxDepth: work.Depth}
	listed := catalogArray(v.manifest["files"])
	if len(files) != len(listed)+1 {
		return catalogInvalid("inventory mismatch")
	}
	previous := ""
	shards := map[string]int{}
	for _, item := range listed {
		m := object(item)
		path, kind := text(m["path"]), text(m["kind"])
		size := catalogNumber(m["bytes"])
		source, exists := files[path]
		if path <= previous || !exists || len(source) != size {
			return catalogInvalid("listed file")
		}
		raw := append([]byte(nil), source...)
		if catalogHash(raw) != m["sha256"] {
			return catalogInvalid("listed file")
		}
		previous = path
		v.files[path] = raw
		if size > 8388608-v.ledger.StoredBytes {
			return catalogInvalid("stored bytes")
		}
		v.ledger.StoredBytes += size
		if kind == "blob" {
			if !strings.HasPrefix(path, "blobs/") || size > 1048576 {
				return catalogInvalid("blob kind/size")
			}
			n := int(path[len("blobs/")] - '0')
			if n != shards["blobs"] {
				return catalogInvalid("blob shard sequence")
			}
			shards["blobs"]++
			continue
		}
		if size > 262144 {
			return catalogInvalid("metadata file size")
		}
		name := ""
		switch kind {
		case "index":
			if path != "index.json" {
				return catalogInvalid("index path")
			}
			name = "Index"
			shards["index"]++
		case "module":
			parts := strings.Split(path, "/")
			if len(parts) != 3 || parts[0] != "modules" {
				return catalogInvalid("module path")
			}
			name = "Module"
		case "shared":
			if !strings.HasPrefix(path, "shared/") {
				return catalogInvalid("shared path")
			}
			name = "Shared"
		default:
			return catalogInvalid("file kind")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := ValidateCatalogDefinition(name, raw); err != nil {
			return err
		}
		value, work, err := catalogJSONValue(raw, true)
		if err != nil {
			return err
		}
		v.ledger.MetadataNodes += work.Nodes
		if v.ledger.MetadataNodes > 1048576 {
			return catalogInvalid("metadata nodes")
		}
		v.ledger.MaxDepth = max(v.ledger.MaxDepth, work.Depth)
		m = object(value)
		switch kind {
		case "index":
			v.index = m
		case "module":
			parts := strings.Split(path, "/")
			module := parts[1]
			n := int(parts[2][0] - '0')
			if text(m["module"]) != module || catalogNumber(m["shard"]) != n || n != shards[module] {
				return catalogInvalid("module shard sequence")
			}
			shards[module]++
			if len(catalogArray(m["entries"]))+len(catalogArray(m["content"])) == 0 {
				return catalogInvalid("empty module")
			}
			last := ""
			for _, item := range catalogArray(m["entries"]) {
				var e CatalogEntry
				if catalogDecodeInto(item, &e) != nil || e.ID <= last {
					return catalogInvalid("entry order")
				}
				last = e.ID
				if _, exists := v.entries[e.ID]; exists {
					return catalogInvalid("duplicate entry")
				}
				if module != catalogEntryModule(e) {
					return catalogInvalid("entry owner")
				}
				v.entries[e.ID] = e
			}
			last = ""
			for _, item := range catalogArray(m["content"]) {
				record := object(item)
				hash := catalogContentIdentity(record)
				if hash <= last {
					return catalogInvalid("content order/hash")
				}
				last = hash
				if _, exists := v.content[hash]; exists {
					return catalogInvalid("duplicate/cross-kind content")
				}
				v.content[hash] = record
				v.owners[hash] = module
			}
		case "shared":
			n := int(path[7]-'0')*10 + int(path[8]-'0')
			if catalogNumber(m["shard"]) != n || n != shards["shared"] {
				return catalogInvalid("shared shard sequence")
			}
			shards["shared"]++
			if len(catalogArray(m["roots"]))+len(catalogArray(m["rows"])) == 0 {
				return catalogInvalid("empty shared shard")
			}
			last := ""
			for _, item := range catalogArray(m["roots"]) {
				var root catalogRoot
				if catalogDecodeInto(item, &root) != nil || root.SHA <= last {
					return catalogInvalid("root order")
				}
				last = root.SHA
				if _, exists := v.roots[root.SHA]; exists {
					return catalogInvalid("duplicate root")
				}
				v.roots[root.SHA] = root
			}
			last = ""
			for _, item := range catalogArray(m["rows"]) {
				var row catalogRow
				if catalogDecodeInto(item, &row) != nil || row.SHA <= last || catalogDescriptorHash(object(item)) != row.SHA {
					return catalogInvalid("row identity/order")
				}
				last = row.SHA
				if _, exists := v.rows[row.SHA]; exists {
					return catalogInvalid("duplicate row")
				}
				v.rows[row.SHA] = row
			}
		}
	}
	for _, module := range catalogModules() {
		if shards[module] == 0 {
			return catalogInvalid("missing physical module")
		}
	}
	if shards["index"] != 1 || v.index == nil || len(v.entries) != 827 || len(v.roots) > 8192 || len(v.rows) > 4096 || len(v.content) > 16384 {
		return catalogInvalid("complete counts")
	}
	expected := map[string]CatalogEntry{}
	for _, e := range baseline.Entries {
		expected[e.ID] = e
	}
	if len(expected) != 827 {
		return catalogInvalid("baseline duplicates")
	}
	for id, e := range v.entries {
		b, exists := expected[id]
		if !exists || e.Kind != b.Kind || e.Profile != b.Profile || !bytes.Equal(catalogEncode(e.Target), catalogEncode(b.Target)) || !bytes.Equal(catalogEncode(e.Aliases), catalogEncode(b.Aliases)) {
			return catalogInvalid("changed identity")
		}
		if !sort.StringsAreSorted(e.ContentRefs) {
			return catalogInvalid("content reference ordering")
		}
	}
	if !bytes.Equal(canonical(v.index["source_files"]), catalogEncode(baseline.SourceFiles)) {
		return catalogInvalid("source tuple provenance")
	}
	v.files["manifest.json"] = manifest
	v.ledger.Content = len(v.content)
	v.ledger.Roots = len(v.roots)
	v.ledger.Rows = len(v.rows)
	return nil
}

func catalogContentIdentity(m map[string]any) string {
	kind := text(m["kind"])
	if kind == "bytes" {
		return text(m["sha256"])
	}
	hash := catalogDescriptorHash(m)
	if kind != "schema_variant" && m["sha256"] != hash {
		return ""
	}
	return hash
}
func catalogModules() []string {
	return []string{"_global", "bot", "docs", "drive", "event", "file", "group", "html", "loop", "mail", "marketplace", "message", "thread"}
}
func catalogEntryModule(e CatalogEntry) string { //nolint:gocritic // bounded immutable identity headers are intentionally passed by value
	switch e.Kind {
	case "resource", "inventory":
		return "_global"
	case "secret":
		return "bot"
	case "derived":
		return "docs"
	case "discovery":
		if e.ID == "octo-cli/discovery/module-map" || e.ID == "octo-cli/discovery/search-all" {
			return "_global"
		}
		parts := strings.Split(e.ID, "/")
		if len(parts) > 1 {
			return parts[1]
		}
	default:
		var tuple []string
		if json.Unmarshal(e.Target, &tuple) == nil && len(tuple) > 1 {
			return tuple[0]
		}
	}
	return ""
}
