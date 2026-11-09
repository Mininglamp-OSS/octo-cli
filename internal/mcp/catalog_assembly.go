package mcp

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed catalog_sources/release-inputs-v6.json
var releaseSourceInputs []byte

//go:embed catalog_sources/page-bounds-v2.json
var sourcePageWitness []byte

// AssemblePermissionRelease constructs the complete finite engineering release shape and
// reports its actual byte size and unresolved source contracts. It deliberately
// emits a report rather than allowlisting a candidate or inventing count bounds.
func AssemblePermissionRelease() ([]byte, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	dynamic, err := MaterializePermissionContent()
	if err != nil {
		return nil, err
	}
	var data struct {
		Truths    map[string]json.RawMessage `json:"parameter_truths"`
		Templates []schemaTemplate           `json:"templates"`
		Variants  []templateSelection        `json:"schema_variants"`
		Discovery []map[string]any           `json:"discovery_indices"`
	}
	if err := json.Unmarshal(dynamic, &data); err != nil {
		return nil, err
	}
	var index sourceIndex
	var inputs struct {
		Profiles    []json.RawMessage `json:"profiles"`
		SourceFiles [][]string        `json:"source_files"`
	}
	if json.Unmarshal(catalogSourceIndex, &index) != nil || json.Unmarshal(releaseSourceInputs, &inputs) != nil {
		return nil, fmt.Errorf("invalid release inputs")
	}
	content := map[string]map[string]any{}
	entryRefs := map[string][]string{}
	put := func(record map[string]any) string {
		sha, _ := record["sha256"].(string)
		if sha == "" {
			raw := catalogCanonicalJSON(record)
			sha = sha256Sum(raw)
		}
		content[sha] = record
		return sha
	}
	for sha, truth := range data.Truths {
		put(map[string]any{"kind": "bytes", "sha256": sha, "length": len(truth), "media_type": "application/json", "data_base64": base64.StdEncoding.EncodeToString(truth)})
	}
	for _, template := range data.Templates {
		raw := catalogJSON(template)
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		put(m)
	}
	s, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	s.clientResources = true
	for _, variant := range data.Variants {
		detail, _ := s.reg.GetOperation(variant.OperationID)
		sha := put(map[string]any{"kind": "schema_variant", "operation_id": variant.OperationID, "facade": variant.Facade, "template_hash": variant.Template, "parameter_truth_hash": variant.Truth})
		entry := "octo-cli/" + detail.Service + "/schema/" + variant.OperationID
		entryRefs[entry] = append(entryRefs[entry], sha)
		operation := "octo-cli/" + detail.Service + "/op/" + variant.OperationID
		entryRefs[operation] = append(entryRefs[operation], sha)
	}
	for _, d := range data.Discovery {
		service, _ := d["scope"].(string)
		entry := "octo-cli/" + service + "/discovery/search"
		scope := map[string]any{"kind": "named", "service": service}
		if service == "" {
			entry = "octo-cli/discovery/search-all"
			scope = map[string]any{"kind": "global"}
		}
		for _, facade := range []string{"search_ops", "get_skill_search"} {
			record := map[string]any{"kind": "discovery_index", "facade": facade, "media_type": "application/json", "renderer": "discovery-search-render-v1", "rows": d["rows"], "scope": scope, "page_limit": d["page_limit"], "query_limits": d["query_limits"], "notes": map[string]any{"query_bounded_format": "query bounded to %d bytes / %d runes / %d terms; ", "more_matches_format": "more than %d matches; narrow with a more specific query or a domain filter"}}
			raw := catalogCanonicalJSON(record)
			record["sha256"] = sha256Sum(raw)
			entryRefs[entry] = append(entryRefs[entry], put(record))
		}
	}
	for ei := range index.Entries {
		entry := &index.Entries[ei]
		switch entry.Kind {
		case "resource":
			var uri string
			if err := json.Unmarshal(entry.Target, &uri); err != nil {
				return nil, err
			}
			value, err := s.readResource(uri)
			if err != nil {
				return nil, err
			}
			text := []byte(value["contents"].([]resourceContent)[0].Text)
			entryRefs[entry.ID] = append(entryRefs[entry.ID], put(map[string]any{"kind": "bytes", "sha256": sha256Sum(text), "length": len(text), "media_type": "text/markdown", "data_base64": base64.StdEncoding.EncodeToString(text)}))
		case "inventory":
			raw := catalogJSON(s.listResources())
			template, err := templateFor(raw)
			if err != nil {
				return nil, err
			}
			b := catalogJSON(template)
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				return nil, err
			}
			entryRefs[entry.ID] = append(entryRefs[entry.ID], put(m))
		case "discovery":
			if entry.ID != "octo-cli/discovery/module-map" {
				continue
			}
			for _, resources := range []bool{false, true} {
				for _, facade := range []facadeMode{facadeThree, facadeTwo, facadeBoth} {
					s.facade = facade
					s.clientResources = resources
					raw := catalogJSON(s.moduleMap())
					template, err := templateFor(raw)
					if err != nil {
						return nil, err
					}
					b := catalogJSON(template)
					var m map[string]any
					if err := json.Unmarshal(b, &m); err != nil {
						return nil, err
					}
					entryRefs[entry.ID] = append(entryRefs[entry.ID], put(m))
				}
			}
		}
	}
	entries := []map[string]any{}
	unprovedPages := []map[string]any{}
	var witness struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal(sourcePageWitness, &witness); err != nil {
		return nil, err
	}
	countWitness := map[string]bool{}
	for _, capability := range witness.Capabilities {
		countWitness[capability] = true
	}
	unboundedResponses := []map[string]any{}
	for ei := range index.Entries {
		e := &index.Entries[ei]
		refs := entryRefs[e.ID]
		sort.Strings(refs)
		unique := []string{}
		for _, ref := range refs {
			if len(unique) == 0 || unique[len(unique)-1] != ref {
				unique = append(unique, ref)
			}
		}
		entries = append(entries, map[string]any{"id": e.ID, "kind": e.Kind, "target": e.Target, "aliases": e.Aliases, "profile": e.Profile, "content_refs": unique})
		if e.Kind == "operation" {
			var target []string
			if err := json.Unmarshal(e.Target, &target); err != nil {
				return nil, err
			}
			detail, _ := s.reg.GetOperation(target[1])
			if detail.Pagination != nil && !countWitness[e.ID] {
				unprovedPages = append(unprovedPages, map[string]any{"capability": e.ID, "method": detail.Method, "path": detail.Path, "required": "source-proven maximum returned items before aggregation; page-size input maxima do not prove it", "cli_source": "cmd/service/run.go:runPaginated"})
			}
			unboundedResponses = append(unboundedResponses, map[string]any{"capability": e.ID, "method": detail.Method, "path": detail.Path, "required": "compatible finite response-acquisition/aggregate contract", "cli_source": "internal/client/client.go:attempt; MaxResponseBytes is unset by ordinary business assembly"})
		}
	}
	ordered := []map[string]any{}
	hashes := make([]string, 0, len(content))
	for sha := range content {
		hashes = append(hashes, sha)
	}
	sort.Strings(hashes)
	for _, sha := range hashes {
		ordered = append(ordered, content[sha])
	}
	sort.Slice(inputs.SourceFiles, func(i, j int) bool { return inputs.SourceFiles[i][0] < inputs.SourceFiles[j][0] })
	release := map[string]any{"format": "permission-release-manifest-v1", "catalog_version": index.CatalogVersion, "semantics_version": "1", "identity_index_sha256": "70cb8771d93bb38788418156029f59f35eb0031f1a8ee3b8d900eb18b673f798", "source_heads": index.SourceHeads, "normalization": map[string]string{"operation": "exact-id-v1", "resource": "first-fragment-only-v1", "origin": "exact-configured-v1", "discovery": "domain-and-validated-query-v1"}, "entries": entries, "profiles": inputs.Profiles, "content": ordered, "source_files": inputs.SourceFiles, "exceptions": []string{"fixed tools/list definitions", "initialize", "nonexecuting initialized/cancelled notifications", "nonexecuting transport acknowledgements", "ping"}, "limits": map[string]int{"artifact_bytes": 8388608, "entries": 827, "profile_nodes": 64, "conditional_children": 8, "redirect_hops": 9, "page_limit": 1000, "local_input_bytes": 16777216}, "contract_revision": 2}
	assembled := catalogCanonicalJSON(release)
	return catalogCanonicalJSON(map[string]any{"format": "permission-release-assembly-report-v4", "catalog_digest": nil, "status": "NOT_ALLOWLISTED_PENDING_COMPATIBLE_SOURCE_BOUNDS", "assembled_bytes": len(assembled), "artifact_limit": 8388608, "assembled_sha256_not_catalog_digest": sha256Sum(assembled), "content_records": len(content), "entries": len(entries), "source_count_witness": json.RawMessage(sourcePageWitness), "unproved_pagination": unprovedPages, "unproved_response_acquisition": unboundedResponses, "release_shape": release, "size_analysis": catalogSizeAnalysis(release, assembled), "unproved_selector_correlations": []string{"schema_variant has no resource-negotiation/managed-field selector; the report retains all distinct templates under the same facade", "discovery_index has no server-facade/resource-negotiation selector; all distinct actual navigation rows are retained", "profile exemplars do not supply selected per-entry pagination declarations or response-acquisition bounds"}}), nil
}

// catalogJSON encodes only registry-owned data during offline generation.
func catalogJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("registry content is not JSON serializable")
	}
	return b
}
