package mcp

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// This index is a mechanically extracted, unchanged entries/profiles projection
// of the approved source candidate. It is not the release allowlist or digest.
//
//go:embed catalog_sources/entry-index-v3.json
var catalogSourceIndex []byte

type sourceEntry struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Profile        string          `json:"profile"`
	Target         json.RawMessage `json:"target"`
	Aliases        []string        `json:"aliases"`
	Content        json.RawMessage `json:"content,omitempty"`
	SchemaContent  json.RawMessage `json:"schema_content,omitempty"`
	ContentProgram json.RawMessage `json:"content_program,omitempty"`
}
type sourceIndex struct {
	Format          string                     `json:"format"`
	CandidateSHA256 string                     `json:"candidate_sha256"`
	CatalogVersion  string                     `json:"catalog_version"`
	SourceHeads     map[string]string          `json:"source_heads"`
	Profiles        map[string]json.RawMessage `json:"profiles"`
	Entries         []sourceEntry              `json:"entries"`
}
type contentBlob struct {
	Bytes  int    `json:"bytes"`
	Base64 string `json:"data_base64"`
}
type materializedEntry struct {
	sourceEntry
	Truth         string            `json:"truth_sha256,omitempty"`
	Variants      map[string]string `json:"variant_sha256,omitempty"`
	SourceFiles   []string          `json:"source_files"`
	ManagedStatus string            `json:"managed_status"`
	Remaining     []string          `json:"remaining"`
}

// MaterializePermissionSources produces actual finite content, not just IDs:
// the registry's OperationDetail truth, three base facade responses per schema,
// filtered resource bytes/envelopes, resource inventory and discovery baselines.
// This is an engineering source artifact. Conditional dynamic templates,
// compatible per-response bounds, and every helper/publication/retry sink still
// need integration; no supported release or catalog_digest is asserted.
func MaterializePermissionSources() ([]byte, error) { //nolint:gocyclo // finite candidate kinds each require distinct content and source-reference rendering
	var index sourceIndex
	if err := json.Unmarshal(catalogSourceIndex, &index); err != nil {
		return nil, err
	}
	s, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	s.facade = facadeThree
	blobs := map[string]contentBlob{}
	put := func(b []byte) string {
		sum := sha256Sum(b)
		blobs[sum] = contentBlob{len(b), base64.StdEncoding.EncodeToString(b)}
		return sum
	}
	marshal := func(v any) string {
		b, e := json.Marshal(v)
		if e != nil {
			panic(e)
		}
		return put(b)
	}
	out := make([]materializedEntry, 0, len(index.Entries))
	truths, variants, resources := 0, 0, 0
	seen := map[string]bool{}
	for idx := range index.Entries {
		entry := index.Entries[idx]
		if seen[entry.ID] {
			return nil, fmt.Errorf("duplicate candidate identity %s", entry.ID)
		}
		seen[entry.ID] = true
		e := materializedEntry{sourceEntry: entry, ManagedStatus: "NOT_WIRED", Remaining: []string{"owned sink integration and positive/negative receiving evidence"}}
		switch entry.Kind {
		case "operation", "schema":
			var target []string
			if json.Unmarshal(entry.Target, &target) != nil || len(target) < 2 {
				return nil, fmt.Errorf("bad target %s", entry.ID)
			}
			detail, ok := s.reg.GetOperation(target[1])
			if !ok || detail.Service != target[0] || !mcpExposed(target[1]) {
				return nil, fmt.Errorf("unreachable candidate %s", entry.ID)
			}
			e.Truth = marshal(detail)
			e.SourceFiles = []string{"internal/registry/specs/" + detail.Service + ".json", "internal/registry/loader.go", "internal/mcp/exposure.json"}
			if entry.Kind == "operation" {
				truths++
				e.SourceFiles = append(e.SourceFiles, "internal/mcp/exec.go", "cmd/service/run.go", "internal/client/client.go")
				e.Remaining = append(e.Remaining, "source-compatible finite acquisition/aggregate bounds; conditional local reads, helpers, pages, retries, publication")
			} else {
				e.Variants = map[string]string{}
				tr := s.describeOp(target[1])
				if tr.IsError {
					return nil, fmt.Errorf("describe failed %s", entry.ID)
				}
				e.Variants["describe_op"] = marshal(tr)
				s.facade = facadeTwo
				for _, depth := range []string{"full", "summary"} {
					tr = s.describeSkill(target[1], depth)
					if tr.IsError {
						return nil, fmt.Errorf("describe failed %s", entry.ID)
					}
					e.Variants["get_skill.describe."+depth] = marshal(tr)
					variants++
				}
				s.facade = facadeThree
				variants++
				e.SourceFiles = append(e.SourceFiles, "internal/mcp/tools.go", "internal/mcp/tools_two.go", "internal/mcp/mapping.go", "internal/mcp/exec_authz.go")
				e.Remaining = append(e.Remaining, "base facade bytes only; resource-negotiation and forced-context whole-value templates remain unmaterialized")
			}
		case "resource":
			var uri string
			if json.Unmarshal(entry.Target, &uri) != nil {
				return nil, fmt.Errorf("bad resource target")
			}
			content, err := s.readResource(uri)
			if err != nil {
				return nil, err
			}
			items := content["contents"].([]resourceContent)
			e.Truth = put([]byte(items[0].Text))
			e.Variants = map[string]string{"resources/read": marshal(content)}
			resources++
			name := strings.TrimPrefix(uri, skillURIScheme)
			e.SourceFiles = []string{"skills/" + name, "internal/mcp/resources.go"}
		case "inventory":
			e.Truth = marshal(s.listResources())
			e.SourceFiles = []string{"internal/mcp/resources.go", "skills/skills.go", "skills/manifest.json"}
		case "discovery":
			domain, query := "", ""
			if strings.Contains(entry.ID, "/discovery/search") && entry.ID != "octo-cli/discovery/search-all" {
				if err := json.Unmarshal(entry.Target, &domain); err != nil {
					return nil, err
				}
			}
			if entry.ID == "octo-cli/discovery/search-all" {
				query = " "
			} // whitespace is search, not module-map
			e.Truth = marshal(s.searchOps(context.Background(), domain, query))
			e.SourceFiles = []string{"internal/mcp/tools.go", "internal/mcp/mapping.go", "internal/mcp/tools_two.go"}
			e.Remaining = append(e.Remaining, "Go lowercase/Fields matching, ordered rows, truncation and dynamic hint templates are retained code, not a complete release content program")
		case "secret":
			e.SourceFiles = []string{"internal/mcp/secret_download.go", "internal/registry/specs/drive.json"}
			e.Remaining = append(e.Remaining, "separate resolver, Redis issuance and model publication checks")
		case "derived":
			e.SourceFiles = []string{"cmd/docs_excalidraw_export.go", "cmd/docs_scene_helpers.go"}
			e.Remaining = append(e.Remaining, "item-specific resolver binding, 100 live fileIds, serial fanout, original plus nine followed redirects and aggregate budgets")
		default:
			return nil, fmt.Errorf("unclassified catalog entry %s", entry.ID)
		}
		out = append(out, e)
	}
	// Ensure the existing registry/exposure has no positive path absent from the
	// candidate. New source operations do not silently extend the candidate.
	for _, op := range s.reg.EnabledOperations() {
		if mcpExposed(op.ID) {
			if _, div := divergentMCPOps[op.ID]; !div && !seen["octo-cli/"+op.Service+"/op/"+op.ID] {
				return nil, fmt.Errorf("uncatalogued exposed operation %s", op.ID)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return json.Marshal(map[string]any{
		"format": "permission-source-materialization-v1", "status": "PARTIAL_ENGINEERING_ARTIFACT_NOT_A_SUPPORTED_RELEASE", "catalog_version": index.CatalogVersion, "catalog_digest": nil,
		"candidate_sha256": index.CandidateSHA256, "source_heads": index.SourceHeads, "source_entry_index_sha256": sha256Sum(catalogSourceIndex),
		"counts":             map[string]int{"entries": len(out), "operation_truths": truths, "base_facade_variants": variants, "filtered_resources": resources},
		"base_configuration": map[string]any{"resources_negotiated": false, "trusted_context": "empty", "facades": []string{"three", "two"}},
		"profiles":           index.Profiles, "entries": out, "blobs": blobs,
	})
}
