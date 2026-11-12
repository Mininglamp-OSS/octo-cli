package mcp

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// PermissionCatalogBaseline returns detached exact pinned identity/source evidence.
func PermissionCatalogBaseline() (permission.CatalogBaseline, error) {
	var source sourceIndex
	if err := json.Unmarshal(catalogSourceIndex, &source); err != nil {
		return permission.CatalogBaseline{}, err
	}
	var input struct {
		Sources [][]string `json:"source_files"`
	}
	if err := json.Unmarshal(releaseSourceInputs, &input); err != nil {
		return permission.CatalogBaseline{}, err
	}
	base := permission.CatalogBaseline{SourceFiles: input.Sources, Entries: []permission.CatalogEntry{}}
	for i := range source.Entries {
		e := &source.Entries[i]
		aliases := e.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		base.Entries = append(base.Entries, permission.CatalogEntry{ID: e.ID, Kind: e.Kind, Target: e.Target, Aliases: aliases, Profile: e.Profile})
	}
	return base, nil
}

// PermissionCatalogSlotValues derives the two closed domains from existing
// trusted-context source functions. It performs no lookup, business action or IO.
func PermissionCatalogSlotValues(operation string, c permission.CatalogConfig, raw []byte, slots []permission.CatalogSlot) (map[string]any, error) {
	if c.ManagedMask < 0 || c.ManagedMask > 7 || c.Facade < 0 || c.Facade > 2 {
		return nil, fmt.Errorf("invalid private catalog configuration")
	}
	s := &Server{}
	sourceConfig(s, 0, c.ManagedMask, c.Facade)
	s.clientResources = c.Resources
	values := map[string]any{}
	for _, slot := range slots {
		switch slot.Domain {
		case "server_managed_argument_names":
			names := []string{}
			for name := range s.trusted.serverManaged(operation) {
				names = append(names, name)
			}
			sort.Strings(names)
			values[slot.ID] = names
		case "facade_hint":
			var root struct {
				Skill struct {
					Name string `json:"name"`
				} `json:"skill"`
			}
			if json.Unmarshal(raw, &root) != nil || root.Skill.Name == "" {
				return nil, fmt.Errorf("missing source skill name")
			}
			values[slot.ID] = s.resourceDegradeHint(root.Skill.Name)
		default:
			return nil, fmt.Errorf("unsupported source slot")
		}
	}
	return values, nil
}
