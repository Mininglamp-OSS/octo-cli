package mcp

// catalogSizeAnalysis accounts for the complete emitted shape, without
// treating transport compression or candidate hashes as a supported release.
func catalogSizeAnalysis(release map[string]any, assembled []byte) map[string]any {
	values := map[string]int{}
	framing := len(assembled)
	for key, value := range release {
		values[key] = len(catalogCanonicalJSON(value))
		framing -= values[key]
	}
	kindBytes, kindCounts := map[string]int{}, map[string]int{}
	inline := map[string]int{}
	rows := map[string]bool{}
	rowOccurrences := 0
	templatePayloads := map[string]bool{}
	variantKeys, discoveryKeys := map[string]int{}, map[string]int{}
	for _, record := range release["content"].([]map[string]any) {
		kind := record["kind"].(string)
		kindBytes[kind] += len(catalogCanonicalJSON(record))
		kindCounts[kind]++
		switch kind {
		case "template":
			payload := record["value_base64"].(string)
			inline[kind] += len(payload)
			templatePayloads[payload] = true
		case "bytes":
			inline[kind] += len(record["data_base64"].(string))
		case "schema_variant":
			variantKeys[record["operation_id"].(string)+"/"+record["facade"].(string)]++
		case "discovery_index":
			discoveryKeys[string(catalogCanonicalJSON(record["scope"]))+"/"+record["facade"].(string)]++
			for _, row := range record["rows"].([]any) {
				payload := row.(map[string]any)["row_base64"].(string)
				inline[kind] += len(payload)
				rows[payload] = true
				rowOccurrences++
			}
		}
	}
	uniqueRowBytes := 0
	for payload := range rows {
		uniqueRowBytes += len(payload)
	}
	ambiguousVariants, ambiguousDiscovery := 0, 0
	for _, count := range variantKeys {
		if count > 1 {
			ambiguousVariants++
		}
	}
	for _, count := range discoveryKeys {
		if count > 1 {
			ambiguousDiscovery++
		}
	}
	return map[string]any{
		"encoding":              "permission-canonical-json-v1; uncompressed; complete shape, not a supported release",
		"top_level_value_bytes": values, "top_level_framing_bytes": framing,
		"content_record_bytes_by_kind": kindBytes, "content_records_by_kind": kindCounts,
		"inline_base64_bytes_by_kind": inline, "distinct_template_payloads": len(templatePayloads),
		"discovery_row_occurrences": rowOccurrences, "distinct_discovery_row_payloads": len(rows),
		"distinct_discovery_row_base64_bytes":           uniqueRowBytes,
		"repeated_discovery_row_base64_bytes":           inline["discovery_index"] - uniqueRowBytes,
		"schema_selector_keys_with_multiple_templates":  ambiguousVariants,
		"discovery_selector_keys_with_multiple_indices": ambiguousDiscovery,
		"fixed_template_and_discovery_literal_bytes":    inline["template"] + inline["discovery_index"],
		"interpretation": "Inline literals in the currently enumerated complete source shapes alone exceed the artifact limit. Exact whole-record/payload deduplication is already applied. This is not an impossibility proof for every approved equivalent representation. Static fragment references, selector fields, compression and new slot domains are absent from the closed grammar and are not selected here.",
	}
}
