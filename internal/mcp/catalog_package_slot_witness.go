package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// MaterializePermissionSlotWitness supplies the server's private SlotCell seam
// with joint values observed directly in source output, outside package metadata.
// This offline evidence is not a supported pin or a package-provided oracle.
func MaterializePermissionSlotWitness(ctx context.Context, pin permission.CatalogPin) ([]byte, error) { //nolint:gocyclo // observe all finite cells before grouping equal joint source values
	base, err := PermissionCatalogBaseline()
	if err != nil {
		return nil, err
	}
	s, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	groups := []any{}
	observed, bearing := 0, 0
	for i := range base.Entries {
		entry := &base.Entries[i]
		if entry.Kind != "schema" {
			continue
		}
		var target []string
		if json.Unmarshal(entry.Target, &target) != nil || len(target) != 2 {
			return nil, fmt.Errorf("invalid source slot identity")
		}
		for _, facade := range []string{"describe_op", "get_skill_full", "get_skill_summary"} {
			cells, values := map[string][]producerCell{}, map[string]map[string]any{}
			for r := 0; r < 2; r++ {
				for mask := 0; mask < 8; mask++ {
					for f := 0; f < 3; f++ {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
						sourceConfig(s, r, mask, f)
						result := s.describeOp(target[1])
						if facade != "describe_op" {
							depth := "full"
							if facade == "get_skill_summary" {
								depth = "summary"
							}
							result = s.describeSkill(target[1], depth)
						}
						joint, err := observedSlotValues([]byte(result.Content[0].Text))
						if err != nil {
							return nil, err
						}
						observed++
						if len(joint) == 0 {
							continue
						}
						bearing++
						key := string(catalogCanonicalJSON(joint))
						values[key] = joint
						cells[key] = append(cells[key], producerCell{r, mask, f})
					}
				}
			}
			keys := make([]string, 0, len(cells))
			for key := range cells {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				for _, selector := range producerSelectors(cells[key], false) {
					groups = append(groups, map[string]any{"entry_id": entry.ID, "facade": facade, "selector": selector, "values": values[key]})
				}
			}
		}
	}
	return catalogCanonicalJSON(map[string]any{"format": "permission-source-slot-witness-v1", "catalog_format": permission.CatalogPackageFormat, "catalog_version": pin.Version, "manifest_sha256": pin.Digest, "source_cli": "f8d434b6318215948ebbf67feb11f6265dee11e5", "schema_source_cells": observed, "slot_bearing_cells": bearing, "server_facades": []string{"three", "two", "both"}, "groups": groups, "status": "SOURCE_EVIDENCE_NOT_SUPPORT_APPROVAL", "static": "all selected static templates have no slots; no callback assignment is needed"}), nil
}

func observedSlotValues(raw []byte) (map[string]any, error) {
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, err
	}
	values := map[string]any{}
	if names, ok := source["server_managed_arguments"]; ok {
		values["server_managed_argument_names"] = names
	}
	if skill, ok := source["skill"].(map[string]any); ok {
		if hint, ok := skill["hint"]; ok {
			values["facade_hint"] = hint
		}
	}
	return values, nil
}
