package permission

import (
	"encoding/json"
	"sort"
)

func (v *VerifiedCatalog) semanticView() []byte { //nolint:gocyclo // normalize only physical representation while retaining complete semantic closure
	normalized := map[string]string{}
	descriptors := map[string]any{}
	var normalize func(string) string
	normalize = func(hash string) string {
		if prior, ok := normalized[hash]; ok {
			return prior
		}
		m := v.content[hash]
		out := map[string]any{}
		for key, value := range m {
			if key != "sha256" && key != "selector" {
				out[key] = value
			}
		}
		switch m["kind"] {
		case "schema_variant":
			out["template_hash"] = normalize(text(m["template_hash"]))
			out["parameter_truth_hash"] = normalize(text(m["parameter_truth_hash"]))
		case "discovery_index":
			rows := []any{}
			for _, ref := range catalogArray(m["rows"]) {
				r := v.rows[text(ref)]
				rows = append(rows, map[string]any{"operation_id": r.Operation, "match_text": r.Match, "raw_sha256": r.Root})
			}
			out["rows"] = rows
		}
		key := catalogHash(canonical(out))
		normalized[hash] = key
		descriptors[key] = out
		return key
	}
	entries := []any{}
	ids := make([]string, 0, len(v.entries))
	for id := range v.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := v.entries[id]
		set := map[string]bool{}
		for _, ref := range e.ContentRefs {
			set[normalize(ref)] = true
		}
		refs := []string{}
		for ref := range set {
			refs = append(refs, ref)
		}
		sort.Strings(refs)
		entries = append(entries, map[string]any{"id": id, "kind": e.Kind, "target": json.RawMessage(e.Target), "aliases": e.Aliases, "profile": e.Profile, "content_refs": refs})
	}
	for hash := range v.content {
		normalize(hash)
	}
	selectors := map[string]any{}
	for _, family := range []struct {
		name  string
		cells map[string]string
	}{{"schema", v.schemas}, {"discovery", v.discovery}, {"static", v.static}} {
		cells := map[string]string{}
		for key, hash := range family.cells {
			cells[key] = normalize(hash)
		}
		selectors[family.name] = cells
	}
	limits := object(v.manifest["limits"])
	businessLimits := map[string]any{}
	for key, value := range limits {
		if key != "packaging" {
			businessLimits[key] = value
		}
	}
	return catalogEncode(map[string]any{"entries": entries, "content": descriptors, "selectors": selectors, "profiles": v.index["profiles"], "source_files": v.index["source_files"], "exceptions": v.index["exceptions"], "source_heads": v.manifest["source_heads"], "normalization": v.manifest["normalization"], "semantics_version": v.manifest["semantics_version"], "limits": businessLimits})
}
