package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// ComparePermissionPackageSource independently compares the consumer's output
// against actual source renderers for every finite cell, with no business IO.
func ComparePermissionPackageSource(ctx context.Context, v *permission.VerifiedCatalog) (map[string]int, error) { //nolint:gocyclo // keep all finite source dimensions and exact byte comparisons visible
	source, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	var index sourceIndex
	if json.Unmarshal(catalogSourceIndex, &index) != nil {
		return nil, fmt.Errorf("invalid pinned identities")
	}
	counts := map[string]int{}
	for i := range index.Entries {
		entry := &index.Entries[i]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if entry.Kind == "schema" {
			var target []string
			if json.Unmarshal(entry.Target, &target) != nil {
				return nil, fmt.Errorf("invalid schema identity")
			}
			op := target[1]
			for r := 0; r < 2; r++ {
				for mask := 0; mask < 8; mask++ {
					for f := 0; f < 3; f++ {
						sourceConfig(source, r, mask, f)
						config := permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f}
						for _, facade := range []string{"describe_op", "get_skill_full", "get_skill_summary"} {
							var expected toolResult
							switch facade {
							case "describe_op":
								expected = source.describeOp(op)
							case "get_skill_full":
								expected = source.describeSkill(op, "full")
							default:
								expected = source.describeSkill(op, "summary")
							}
							actual, err := v.Schema(op, facade, config)
							if err != nil || !bytes.Equal(actual, []byte(expected.Content[0].Text)) {
								return nil, fmt.Errorf("independent consumer/schema source mismatch %s/%s/%d/%d/%d", op, facade, r, mask, f)
							}
							counts["schema_cells"]++
						}
					}
				}
			}
		} else if entry.Kind == "resource" || entry.Kind == "inventory" || entry.ID == "octo-cli/discovery/module-map" {
			for r := 0; r < 2; r++ {
				for mask := 0; mask < 8; mask++ {
					for f := 0; f < 3; f++ {
						sourceConfig(source, r, mask, f)
						var expected []byte
						if entry.Kind == "resource" {
							var uri string
							if json.Unmarshal(entry.Target, &uri) != nil {
								return nil, fmt.Errorf("invalid resource")
							}
							resource, err := source.readResource(uri)
							if err != nil {
								return nil, err
							}
							expected = []byte(resource["contents"].([]resourceContent)[0].Text)
							media, err := v.StaticMedia(entry.ID, permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f})
							if err != nil || media != resource["contents"].([]resourceContent)[0].MimeType {
								return nil, fmt.Errorf("independent resource media/source mismatch")
							}
						} else {
							var value any
							if entry.Kind == "inventory" {
								value = source.listResources()
							} else {
								value = source.moduleMap()
							}
							expected, err = json.MarshalIndent(value, "", "  ")
							if err != nil {
								return nil, err
							}
						}
						actual, err := v.Static(entry.ID, permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f})
						if err != nil || !bytes.Equal(actual, expected) {
							return nil, fmt.Errorf("independent consumer/static source mismatch %s", entry.ID)
						}
						counts["static_cells"]++
					}
				}
			}
		}
	}
	for _, service := range append([]string{""}, source.reg.EnabledServices()...) {
		for r := 0; r < 2; r++ {
			for mask := 0; mask < 8; mask++ {
				for f := 0; f < 3; f++ {
					sourceConfig(source, r, mask, f)
					expected := [][]byte{}
					matches := []string{}
					ops := source.reg.ListOperations(service)
					if service == "" {
						ops = source.reg.EnabledOperations()
					}
					for _, op := range ops {
						if !mcpExposed(op.ID) {
							continue
						}
						if _, bad := divergentMCPOps[op.ID]; bad {
							continue
						}
						expected = append(expected, catalogJSON(opSearchResult{OperationInfo: op, Skill: source.navFor(op, false)}))
						matches = append(matches, strings.ToLower(op.ID+" "+op.Summary+" "+op.Path))
					}
					for _, facade := range []string{"search_ops", "get_skill_search"} {
						contract, err := v.DiscoveryContract(service, facade, permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f})
						if err != nil || contract != (permission.CatalogDiscoveryContract{PageLimit: searchOpsPageLimit, QueryBytes: maxSearchQueryBytes, QueryRunes: maxSearchQueryRunes, QueryTerms: maxSearchTerms, QueryNote: "query bounded to %d bytes / %d runes / %d terms; ", MoreNote: "more than %d matches; narrow with a more specific query or a domain filter"}) {
							return nil, fmt.Errorf("independent discovery matcher/notes source mismatch")
						}
						actual, terms, err := v.Discovery(service, facade, permission.CatalogConfig{Resources: r == 1, ManagedMask: mask, Facade: f})
						if err != nil || len(actual) != len(expected) || len(terms) != len(matches) {
							return nil, fmt.Errorf("independent discovery count mismatch")
						}
						for i := range expected {
							if !bytes.Equal(actual[i], expected[i]) || terms[i] != matches[i] {
								return nil, fmt.Errorf("independent discovery row/source mismatch")
							}
						}
						counts["discovery_source_observations"]++
					}
				}
			}
		}
	}
	return counts, nil
}
