package service

import (
	"fmt"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/fracindex"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// ValidateIdempotencyKey rejects an explicitly supplied blank idempotency key.
func ValidateIdempotencyKey(label, value string) *output.ExitError {
	if strings.TrimSpace(value) == "" {
		return output.ErrValidation(label+" must not be blank", "supply a stable key and preserve it with the same body on retry")
	}
	return nil
}

// indexHint is the guidance attached to every fractional-index validation
// error so a caller (typically an agent) learns the correct way to produce a
// key instead of inventing one.
const indexHint = "every whiteboard element must carry a valid fractional-index `index` (z-order key) — generate it with the fractional-indexing / Excalidraw rules (e.g. \"a0\", \"a1\"); never omit it or fabricate an \"r\"+digits, plain-integer, or timestamp value"

// validateElementsIndex enforces that every element in a scene-edit body carries
// a valid fractional-index `index`. It is invoked only for operations that
// declare x-octo-validate-elements-index in the spec. Rejecting here — before
// the request is sent — stops index-less / garbage-index elements from reaching
// the backend, where a buggy repair path would rewrite them into an invalid key
// (XIN-792) and break the board.
//
// A body with no `elements` key (e.g. a delete-only batch) is left untouched.
func validateElementsIndex(body any) *output.ExitError {
	m, ok := body.(map[string]any)
	if !ok {
		// No structured body (or --data was not an object); nothing to check.
		return nil
	}
	raw, present := m["elements"]
	if !present {
		return nil
	}
	elems, ok := raw.([]any)
	if !ok {
		return output.ErrValidation("`elements` must be a JSON array", indexHint)
	}
	for i, e := range elems {
		el, ok := e.(map[string]any)
		if !ok {
			return output.ErrValidation(fmt.Sprintf("elements[%d] must be a JSON object", i), indexHint)
		}
		label := elementLabel(el, i)
		idxRaw, has := el["index"]
		if !has {
			return output.ErrValidation(fmt.Sprintf("%s is missing `index`", label), indexHint)
		}
		idx, ok := idxRaw.(string)
		if !ok {
			return output.ErrValidation(fmt.Sprintf("%s `index` must be a string", label), indexHint)
		}
		if err := fracindex.ValidateOrderKey(idx); err != nil {
			return output.ErrValidation(fmt.Sprintf("%s has an invalid `index`: %v", label, err), indexHint)
		}
	}
	return nil
}

// elementLabel builds a human-readable reference to an element for error
// messages, preferring its id when present.
func elementLabel(el map[string]any, i int) string {
	if id, ok := el["id"].(string); ok && id != "" {
		return fmt.Sprintf("elements[%d] (id %q)", i, id)
	}
	return fmt.Sprintf("elements[%d]", i)
}

// validateMarketplaceRelations rejects marketplace relation payloads that the
// backend would refuse, so the operator sees the exact array index and field
// instead of a round-trip error. Target existence, publication state and scope
// stay server-side decisions.
func validateMarketplaceRelations(body any, review bool) *output.ExitError {
	m, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	raw, present := m["relations"]
	if !present || raw == nil {
		// A present-null relations value is the clearing/absent spelling; the
		// generic body walker passes it through for this optional field.
		return nil
	}
	rels, ok := raw.([]any)
	if !ok {
		return output.ErrValidation("`relations` must be a JSON array", relationsHint(review))
	}
	pluginType := marketplacePluginType(m)
	seen := map[string]struct{}{}
	for i, entry := range rels {
		rel, ok := entry.(map[string]any)
		if !ok {
			return output.ErrValidation(fmt.Sprintf("relations[%d] must be a JSON object", i), relationsHint(review))
		}
		if verr := validateRelationEntry(i, pluginType, rel, seen, review); verr != nil {
			return verr
		}
	}
	return nil
}

// marketplacePluginType reads the plugin type from the upsert wrapper or, for
// review submissions, from the top-level manifest/package documents.
func marketplacePluginType(m map[string]any) string {
	if plugin, ok := m["plugin"].(map[string]any); ok {
		if t, _ := plugin["plugin_type"].(string); t != "" {
			return t
		}
	}
	for _, key := range []string{"manifest_json", "plugin_json"} {
		if doc, ok := m[key].(map[string]any); ok {
			if t, _ := doc["plugin_type"].(string); t != "" {
				return t
			}
		}
	}
	return ""
}

// validateRelationEntry checks one relation against the marketplace matrix and
// records its (type,target) and member key for duplicate detection.
func validateRelationEntry(i int, pluginType string, rel map[string]any, seen map[string]struct{}, review bool) *output.ExitError {
	relType, _ := rel["relation_type"].(string)
	switch relType {
	case "expert_skill", "expert_connector", "expert_team_expert":
	default:
		return output.ErrValidation(fmt.Sprintf("relations[%d].relation_type is invalid", i), relationsHint(review))
	}
	switch pluginType {
	case "expert_team":
		if relType != "expert_team_expert" {
			return output.ErrValidation(fmt.Sprintf("relations[%d].relation_type is invalid for an expert_team", i), relationsHint(review))
		}
	case "expert":
		if relType == "expert_team_expert" {
			return output.ErrValidation(fmt.Sprintf("relations[%d].relation_type is invalid for an expert", i), relationsHint(review))
		}
	}
	target, _ := rel["target_plugin_id"].(string)
	if target == "" {
		return output.ErrValidation(fmt.Sprintf("relations[%d].target_plugin_id is required", i), relationsHint(review))
	}
	key := relType + "\x00" + target
	if _, dup := seen[key]; dup {
		return output.ErrValidation(fmt.Sprintf("relations[%d] duplicates target %s", i, target), relationsHint(review))
	}
	seen[key] = struct{}{}
	if verr := validateRelationVersion(i, relType, rel, review); verr != nil {
		return verr
	}
	if relType != "expert_team_expert" {
		return nil
	}
	if rawData, hasData := rel["data"]; hasData && rawData != nil {
		if _, ok := rawData.(map[string]any); !ok {
			return output.ErrValidation(fmt.Sprintf("relations[%d].data must be a JSON object", i), relationsHint(review))
		}
	}
	return nil
}

func validateRelationVersion(i int, relType string, rel map[string]any, review bool) *output.ExitError {
	rawVersion, hasVersion := rel["target_version_id"]
	if !hasVersion || rawVersion == nil {
		return nil
	}
	if relType == "expert_connector" {
		// Only a real pin is forbidden: "" is the wire spelling of "no pin" that
		// a read-modify-write echo can produce.
		if s, _ := rawVersion.(string); s != "" {
			return output.ErrValidation(fmt.Sprintf("relations[%d].target_version_id is forbidden for connectors", i), relationsHint(review))
		}
		return nil
	}
	if _, ok := rawVersion.(string); !ok {
		return output.ErrValidation(fmt.Sprintf("relations[%d].target_version_id must be a string when supplied", i), relationsHint(review))
	}
	return nil
}

// relationsHint keeps upsert's destructive omission semantics separate from
// review_submit's inherit semantics; one shared string would tell review callers
// to freeze a visibility-filtered graph.
func relationsHint(review bool) string {
	if review {
		return "Omit relations to inherit the live graph; when present this list replaces the frozen review snapshot. Skill and team-member relations may set target_version_id; expert_connector must not."
	}
	return "relations is a full replacement list: send every edge, every time. Omitting the key is the same as [] and soft-deletes every live relation. Skill and team-member relations may set target_version_id; expert_connector must not."
}
