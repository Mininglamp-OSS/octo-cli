package permission

import (
	"context"
	"encoding/json"
)

func catalogEntryOperation(e CatalogEntry) string { //nolint:gocritic // read a detached immutable identity header
	var tuple []string
	if json.Unmarshal(e.Target, &tuple) == nil && len(tuple) > 1 {
		return tuple[1]
	}
	return ""
}

func (v *VerifiedCatalog) verifyGraph(ctx context.Context, baseline CatalogBaseline) error { //nolint:gocyclo // typed traversal includes inherited actions and index-only roots
	for _, item := range catalogArray(v.index["profiles"]) {
		p := object(item)
		name := text(p["name"])
		if _, ok := v.profiles[name]; ok {
			return catalogInvalid("duplicate profile")
		}
		v.profiles[name] = p
		if err := v.verifyProfile(p); err != nil {
			return err
		}
	}
	deps := map[string][]string{}
	for id, e := range v.entries {
		profile, ok := v.profiles[e.Profile]
		if !ok {
			return catalogInvalid("unresolved profile")
		}
		targets := map[string]bool{}
		for _, child := range catalogArray(profile["children"]) {
			targets[text(child)] = true
		}
		for _, phase := range []string{"prepare", "dispatch", "publish"} {
			for _, item := range catalogArray(profile[phase]) {
				a := object(item)
				if a["capability"] != "self" {
					targets[text(a["capability"])] = true
				}
				binding := object(object(a["target_template"])["binding"])
				if binding["kind"] == "capability" {
					targets[text(binding["id"])] = true
				}
			}
		}
		for target := range targets {
			if _, ok := v.entries[target]; !ok {
				return catalogInvalid("profile capability reference")
			}
			deps[id] = append(deps[id], target)
		}
		for _, ref := range e.ContentRefs {
			record, ok := v.content[ref]
			if !ok {
				return catalogInvalid("entry content reference")
			}
			if (e.Kind == "operation" || e.Kind == "schema") && (record["kind"] != "schema_variant" || record["operation_id"] != catalogEntryOperation(e)) {
				return catalogInvalid("entry schema binding")
			}
		}
	}
	state := map[string]int{}
	var acyclic func(string) bool
	acyclic = func(id string) bool {
		if state[id] == 1 {
			return false
		}
		if state[id] == 2 {
			return true
		}
		state[id] = 1
		for _, next := range deps[id] {
			if !acyclic(next) {
				return false
			}
		}
		state[id] = 2
		return true
	}
	for id := range v.entries {
		if !acyclic(id) {
			return catalogInvalid("capability dependency cycle")
		}
	}
	owners := map[string]string{}
	rootSeen, rowSeen := map[string]bool{}, map[string]bool{}
	var content func(string, string) error
	content = func(hash, label string) error {
		record, ok := v.content[hash]
		if !ok {
			return catalogInvalid("typed content edge")
		}
		if owner, seen := owners[hash]; seen && owner <= label {
			return nil
		}
		owners[hash] = label
		switch record["kind"] {
		case "bytes":
			root := text(record["data_ref"])
			r, ok := v.roots[root]
			if !ok || root != hash || catalogNumber(record["length"]) != r.Length {
				return catalogInvalid("bytes root identity")
			}
			rootSeen[root] = true
		case "template":
			root := text(record["value_ref"])
			if _, ok := v.roots[root]; !ok {
				return catalogInvalid("template root type")
			}
			rootSeen[root] = true
		case "schema_variant":
			template, truth := text(record["template_hash"]), text(record["parameter_truth_hash"])
			if v.content[template]["kind"] != "template" || v.content[truth]["kind"] != "bytes" || v.content[truth]["media_type"] != "application/json" {
				return catalogInvalid("schema transitive types")
			}
			if err := content(template, label); err != nil {
				return err
			}
			if err := content(truth, label); err != nil {
				return err
			}
		case "discovery_index":
			for _, item := range catalogArray(record["rows"]) {
				row, ok := v.rows[text(item)]
				if !ok {
					return catalogInvalid("ordered row reference")
				}
				if _, ok := v.roots[row.Root]; !ok {
					return catalogInvalid("row root type")
				}
				rowSeen[row.SHA] = true
				rootSeen[row.Root] = true
			}
		default:
			return catalogInvalid("content type")
		}
		return nil
	}
	for _, source := range baseline.SourceFiles {
		if v.content[source[1]]["kind"] != "bytes" {
			return catalogInvalid("index-only source descriptor")
		}
		if err := content(source[1], "_global"); err != nil {
			return err
		}
	}
	for id, e := range v.entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		label := catalogEntryModule(e)
		visited := map[string]bool{}
		var walk func(string) error
		walk = func(next string) error {
			if visited[next] {
				return nil
			}
			visited[next] = true
			for _, ref := range v.entries[next].ContentRefs {
				if err := content(ref, label); err != nil {
					return err
				}
			}
			for _, child := range deps[next] {
				if err := walk(child); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(id); err != nil {
			return err
		}
	}
	if len(owners) != len(v.content) || len(rootSeen) != len(v.roots) || len(rowSeen) != len(v.rows) {
		return catalogInvalid("orphan content/root/row")
	}
	for hash, owner := range owners {
		if v.owners[hash] != owner {
			return catalogInvalid("transitive content owner")
		}
	}
	return v.verifySelectors()
}

func (v *VerifiedCatalog) verifyProfile(p map[string]any) error { //nolint:gocyclo // phase/reference/condition/page invariants are one closed fixed-program check
	actions := map[string]string{}
	conditionNodes := 0
	var condition func(map[string]any, int) bool
	condition = func(c map[string]any, depth int) bool {
		conditionNodes++
		if depth > 4 || conditionNodes > 64 {
			return false
		}
		for _, child := range catalogArray(c["conditions"]) {
			if !condition(object(child), depth+1) {
				return false
			}
		}
		return true
	}
	for _, phase := range []string{"prepare", "dispatch", "publish"} {
		for _, item := range catalogArray(p[phase]) {
			a := object(item)
			id := text(a["id"])
			if _, ok := actions[id]; ok {
				return catalogInvalid("duplicate action")
			}
			actions[id] = phase
			if !condition(object(a["condition"]), 1) {
				return catalogInvalid("profile condition work")
			}
		}
	}
	if len(actions) > 64 {
		return catalogInvalid("profile actions")
	}
	branches := map[string]bool{}
	used := map[string]bool{}
	for _, item := range catalogArray(p["branches"]) {
		b := object(item)
		id := text(b["id"])
		if branches[id] {
			return catalogInvalid("duplicate branch")
		}
		branches[id] = true
		if !condition(object(b["condition"]), 1) {
			return catalogInvalid("branch condition work")
		}
		for _, phase := range []string{"prepare", "dispatch", "publish"} {
			for _, action := range catalogArray(b[phase]) {
				if actions[text(action)] != phase {
					return catalogInvalid("branch phase reference")
				}
				used[text(action)] = true
			}
		}
	}
	if len(used) != len(actions) {
		return catalogInvalid("unused profile action")
	}
	pages := map[string]bool{}
	pageActions := map[string]bool{}
	limits := object(p["limits"])
	for _, item := range catalogArray(p["pagination"]) {
		page := object(item)
		id, action := text(page["id"]), text(page["action_id"])
		if pages[id] || pageActions[action] || actions[action] != "dispatch" || catalogNumber(page["items_per_page"]) > catalogNumber(limits["items_per_page"]) || catalogNumber(page["page_limit"]) > catalogNumber(limits["page_limit"]) {
			return catalogInvalid("pagination action/bounds")
		}
		pages[id] = true
		pageActions[action] = true
	}
	return nil
}

func (v *VerifiedCatalog) verifySelectors() error { //nolint:gocyclo // complete finite partitions have no fallback or priority
	ops := map[string]bool{}
	services := map[string]string{}
	static := map[string]bool{}
	for id, e := range v.entries {
		if e.Kind == "schema" {
			ops[catalogEntryOperation(e)] = true
			services[catalogEntryOperation(e)] = catalogEntryModule(e)
		}
		if e.Kind == "resource" || e.Kind == "inventory" || id == "octo-cli/discovery/module-map" {
			static[id] = true
		}
	}
	for hash, m := range v.content {
		switch m["kind"] {
		case "schema_variant":
			op, facade := text(m["operation_id"]), text(m["facade"])
			if !ops[op] {
				return catalogInvalid("unknown schema operation")
			}
			var selector catalogSelector
			if catalogDecodeInto(m["selector"], &selector) != nil {
				return ErrInvalid
			}
			for _, cell := range catalogCells(selector, false) {
				key := catalogCell(op+"|"+facade, cell)
				if _, exists := v.schemas[key]; exists {
					return catalogInvalid("schema selector overlap")
				}
				v.schemas[key] = hash
				v.uses[text(m["template_hash"])] = append(v.uses[text(m["template_hash"])], catalogUse{op, cell})
			}
		case "discovery_index":
			scope := object(m["scope"])
			service := text(scope["service"])
			if scope["kind"] == "global" {
				service = ""
			}
			var selector catalogSelector
			if catalogDecodeInto(m["selector"], &selector) != nil {
				return ErrInvalid
			}
			for _, cell := range catalogCells(selector, true) {
				key := catalogCell(service+"|"+text(m["facade"]), cell)
				if _, exists := v.discovery[key]; exists {
					return catalogInvalid("discovery selector overlap")
				}
				v.discovery[key] = hash
			}
			seen := map[string]bool{}
			for _, ref := range catalogArray(m["rows"]) {
				row := v.rows[text(ref)]
				if !ops[row.Operation] || seen[row.Operation] || service != "" && services[row.Operation] != service {
					return catalogInvalid("discovery operation partition")
				}
				seen[row.Operation] = true
			}
			for operation := range ops {
				if (service == "" || services[operation] == service) && !seen[operation] {
					return catalogInvalid("incomplete discovery operation inventory")
				}
			}
		}
	}
	for op := range ops {
		for _, facade := range []string{"describe_op", "get_skill_full", "get_skill_summary"} {
			for _, cell := range catalogCells(catalogSelector{3, 255, 7}, false) {
				if _, ok := v.schemas[catalogCell(op+"|"+facade, cell)]; !ok {
					return catalogInvalid("schema selector gap")
				}
			}
		}
	}
	for _, service := range append([]string{""}, catalogModules()[1:]...) {
		for _, facade := range []string{"search_ops", "get_skill_search"} {
			for _, cell := range catalogCells(catalogSelector{Resources: 3, Facade: 7}, true) {
				if _, ok := v.discovery[catalogCell(service+"|"+facade, cell)]; !ok {
					return catalogInvalid("discovery selector gap")
				}
			}
		}
	}
	previous := ""
	var previousSelector catalogSelector
	for _, item := range catalogArray(v.index["static_bindings"]) {
		var b catalogBinding
		if catalogDecodeInto(item, &b) != nil || !static[b.Entry] || !catalogStringSet(v.entries[b.Entry].ContentRefs)[b.Content] {
			return catalogInvalid("static binding reference")
		}
		if b.Entry < previous || b.Entry == previous && !catalogSelectorLess(previousSelector, b.Selector) {
			return catalogInvalid("static binding order")
		}
		previous, previousSelector = b.Entry, b.Selector
		record := v.content[b.Content]
		if record["kind"] != "bytes" && record["kind"] != "template" {
			return catalogInvalid("static binding type")
		}
		for _, cell := range catalogCells(b.Selector, false) {
			key := catalogCell(b.Entry, cell)
			if _, exists := v.static[key]; exists {
				return catalogInvalid("static selector overlap")
			}
			v.static[key] = b.Content
			if record["kind"] == "template" {
				v.uses[b.Content] = append(v.uses[b.Content], catalogUse{"", cell})
			}
		}
	}
	for id := range static {
		for _, cell := range catalogCells(catalogSelector{3, 255, 7}, false) {
			if _, ok := v.static[catalogCell(id, cell)]; !ok {
				return catalogInvalid("static selector gap")
			}
		}
	}
	if len(ops) != 393 || len(static) != 26 || len(v.schemas) != 56592 || len(v.discovery) != 156 || len(v.static) != 1248 {
		return catalogInvalid("finite selection totals")
	}
	v.ledger.SchemaCells = len(v.schemas)
	v.ledger.DiscoveryCells = len(v.discovery)
	v.ledger.StaticCells = len(v.static)
	return nil
}

func catalogSelectorLess(a, b catalogSelector) bool {
	if a.Resources != b.Resources {
		return a.Resources < b.Resources
	}
	if a.Managed != b.Managed {
		return a.Managed < b.Managed
	}
	return a.Facade < b.Facade
}
