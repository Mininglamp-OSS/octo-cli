package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func producerModule(entry *sourceEntry) string {
	switch entry.Kind {
	case "resource", "inventory":
		return "_global"
	case "secret":
		return "bot"
	case "derived":
		return "docs"
	case "discovery":
		if entry.ID == "octo-cli/discovery/module-map" || entry.ID == "octo-cli/discovery/search-all" {
			return "_global"
		}
		return strings.Split(entry.ID, "/")[1]
	default:
		var tuple []string
		if json.Unmarshal(entry.Target, &tuple) != nil || len(tuple) < 2 {
			panic("invalid immutable identity")
		}
		return tuple[0]
	}
}

func (p *packageProducer) owners() map[string]string {
	owners := map[string]string{}
	entries := map[string]*sourceEntry{}
	profiles := map[string]map[string]any{}
	for i := range p.source.Entries {
		e := &p.source.Entries[i]
		entries[e.ID] = e
	}
	for _, profile := range p.profiles {
		profiles[profile["name"].(string)] = profile
	}
	var content func(string, string)
	content = func(hash, label string) {
		if prior, ok := owners[hash]; !ok || label < prior {
			owners[hash] = label
		}
		m := p.content[hash]
		if m["kind"] == "schema_variant" {
			content(m["template_hash"].(string), label)
			content(m["parameter_truth_hash"].(string), label)
		}
	}
	for _, source := range p.sources {
		content(source[1], "_global")
	}
	for i := range p.source.Entries {
		root := &p.source.Entries[i]
		label := producerModule(root)
		seen := map[string]bool{}
		var visit func(string)
		visit = func(id string) {
			if seen[id] {
				return
			}
			seen[id] = true
			e := entries[id]
			for ref := range p.refs[id] {
				content(ref, label)
			}
			profile := profiles[e.Profile]
			for _, child := range profile["children"].([]any) {
				visit(child.(string))
			}
			for _, phase := range []string{"prepare", "dispatch", "publish"} {
				for _, action := range profile[phase].([]any) {
					a := action.(map[string]any)
					if capability := a["capability"].(string); capability != "self" {
						visit(capability)
					}
					target := a["target_template"].(map[string]any)
					binding := target["binding"].(map[string]any)
					if binding["kind"] == "capability" {
						visit(binding["id"].(string))
					}
				}
			}
		}
		visit(root.ID)
	}
	return owners
}

func writeModuleShards(files map[string][]byte, module string, entries, content []any) error {
	shard := 0
	ei, ci := 0, 0
	for ei < len(entries) || ci < len(content) {
		es, cs := []any{}, []any{}
		size := len(catalogCanonicalJSON(map[string]any{"kind": "module", "module": module, "shard": shard, "entries": es, "content": cs}))
		for ei < len(entries) {
			n := len(catalogCanonicalJSON(entries[ei])) + 1
			if size+n > 262143 {
				break
			}
			es = append(es, entries[ei])
			size += n
			ei++
		}
		for ci < len(content) {
			n := len(catalogCanonicalJSON(content[ci])) + 1
			if size+n > 262143 {
				break
			}
			cs = append(cs, content[ci])
			size += n
			ci++
		}
		if len(es)+len(cs) == 0 || shard > 7 {
			return fmt.Errorf("v2 module shard bound %s", module)
		}
		raw := catalogCanonicalJSON(map[string]any{"kind": "module", "module": module, "shard": shard, "entries": es, "content": cs})
		if len(raw) > 262144 {
			return fmt.Errorf("v2 JSON file exceeds bound")
		}
		files[fmt.Sprintf("modules/%s/%d.json", module, shard)] = raw
		shard++
	}
	return nil
}

func writeSharedShards(files map[string][]byte, roots, rows []map[string]any) error {
	shard := 0
	ri, yi := 0, 0
	for ri < len(roots) || yi < len(rows) {
		rs, ys := []map[string]any{}, []map[string]any{}
		size := len(catalogCanonicalJSON(map[string]any{"kind": "shared", "shard": shard, "roots": rs, "rows": ys}))
		for ri < len(roots) {
			n := len(catalogCanonicalJSON(roots[ri])) + 1
			if size+n > 262143 {
				break
			}
			rs = append(rs, roots[ri])
			size += n
			ri++
		}
		for yi < len(rows) {
			n := len(catalogCanonicalJSON(rows[yi])) + 1
			if size+n > 262143 {
				break
			}
			ys = append(ys, rows[yi])
			size += n
			yi++
		}
		if len(rs)+len(ys) == 0 || shard > 15 {
			return fmt.Errorf("v2 shared shard bound")
		}
		files[fmt.Sprintf("shared/%02d.json", shard)] = catalogCanonicalJSON(map[string]any{"kind": "shared", "shard": shard, "roots": rs, "rows": ys})
		shard++
	}
	return nil
}

func packageManifest(files map[string][]byte, source *sourceIndex) []byte {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
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
	packaging := map[string]int{"files": 64, "manifest_bytes": 65536, "json_file_bytes": 262144, "blob_file_bytes": 1048576, "roots": 8192, "shared_rows": 4096, "content_records": 16384, "parts_per_root": 256, "parts_total": 65536, "root_bytes": 8388608, "expanded_bytes": 16777216, "json_depth": 32, "nodes": 1048576}
	return catalogCanonicalJSON(map[string]any{"format": permission.CatalogPackageFormat, "contract_revision": 3, "catalog_version": "octo-capabilities-v1-xin2748-v4", "semantics_version": "1", "identity_index_sha256": "70cb8771d93bb38788418156029f59f35eb0031f1a8ee3b8d900eb18b673f798", "source_heads": source.SourceHeads, "normalization": map[string]string{"operation": "exact-id-v1", "resource": "first-fragment-only-v1", "origin": "exact-configured-v1", "discovery": "domain-and-validated-query-v1"}, "limits": map[string]any{"artifact_bytes": 8388608, "entries": 827, "profile_nodes": 64, "conditional_children": 8, "local_input_bytes": 16777216, "page_limit": 1000, "redirect_hops": 9, "packaging": packaging}, "files": inventory})
}

func (p *packageProducer) assemble(ctx context.Context) (*CatalogPackageProduction, error) { //nolint:gocyclo // finite deterministic partition/shard selection preserves every record
	owners := p.owners()
	entriesByModule, contentByModule := map[string][]any{}, map[string][]any{}
	for i := range p.source.Entries {
		entry := &p.source.Entries[i]
		refs := []string{}
		for ref := range p.refs[entry.ID] {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		aliases := entry.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		record := map[string]any{"id": entry.ID, "kind": entry.Kind, "target": entry.Target, "aliases": aliases, "profile": entry.Profile, "content_refs": refs}
		module := producerModule(entry)
		entriesByModule[module] = append(entriesByModule[module], record)
	}
	contentKeys := make([]string, 0, len(p.content))
	for hash := range p.content {
		contentKeys = append(contentKeys, hash)
	}
	sort.Strings(contentKeys)
	for _, hash := range contentKeys {
		module, ok := owners[hash]
		if !ok {
			return nil, fmt.Errorf("unreachable producer content %s", hash)
		}
		contentByModule[module] = append(contentByModule[module], p.content[hash])
	}
	sort.Slice(p.bindings, func(i, j int) bool {
		a, b := p.bindings[i], p.bindings[j]
		if a["entry_id"] != b["entry_id"] {
			return a["entry_id"].(string) < b["entry_id"].(string)
		}
		x, y := a["selector"].(map[string]int), b["selector"].(map[string]int)
		for _, k := range []string{"resources_bits", "managed_bits", "facade_bits"} {
			if x[k] != y[k] {
				return x[k] < y[k]
			}
		}
		return false
	})
	base := map[string][]byte{}
	base["index.json"] = catalogCanonicalJSON(map[string]any{"kind": "index", "profiles": p.profiles, "source_files": p.sources, "exceptions": []string{"fixed tools/list definitions", "initialize", "nonexecuting initialized/cancelled notifications", "nonexecuting transport acknowledgements", "ping"}, "static_bindings": p.bindings})
	for _, module := range []string{"_global", "bot", "docs", "drive", "event", "file", "group", "html", "loop", "mail", "marketplace", "message", "thread"} {
		entries := entriesByModule[module]
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].(map[string]any)["id"].(string) < entries[j].(map[string]any)["id"].(string)
		})
		if err := writeModuleShards(base, module, entries, contentByModule[module]); err != nil {
			return nil, err
		}
	}
	rows := []map[string]any{}
	rowKeys := make([]string, 0, len(p.rows))
	for key := range p.rows {
		rowKeys = append(rowKeys, key)
	}
	sort.Strings(rowKeys)
	for _, key := range rowKeys {
		rows = append(rows, p.rows[key])
	}
	whole := 0
	var best *CatalogPackageProduction
	for _, threshold := range []int{0, 192, 384, 768} {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var files map[string][]byte
		var roots []map[string]any
		var err error
		if threshold == 0 {
			files, roots, err = packageRawPools(p.raw)
		} else {
			files, roots, err = packageSlicePools(p.raw, threshold)
		}
		if err != nil {
			if threshold == 0 {
				whole = -1
			}
			continue
		}
		for path, raw := range base {
			files[path] = raw
		}
		if err = writeSharedShards(files, roots, rows); err != nil {
			continue
		}
		files["manifest.json"] = packageManifest(files, &p.source)
		stored := 0
		for _, b := range files {
			stored += len(b)
		}
		if threshold == 0 {
			whole = stored
		}
		candidate := &CatalogPackageProduction{Files: files, Pin: permission.CatalogPin{Version: "octo-capabilities-v1-xin2748-v4", Digest: sha256Sum(files["manifest.json"])}, Conformance: p.checks, WholeStoredBytes: whole, FactoringThreshold: threshold}
		candidate.Ledger = p.measure(files, roots)
		if best == nil || stored < packageStored(best.Files) {
			best = candidate
		}
		if threshold == 0 && stored <= 8388608 {
			break
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no bounded v2 recipe produced")
	}
	best.WholeStoredBytes = whole
	return best, nil
}

func packageStored(files map[string][]byte) int {
	n := 0
	for _, b := range files {
		n += len(b)
	}
	return n
}
