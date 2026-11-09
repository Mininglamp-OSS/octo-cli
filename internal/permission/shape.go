package permission

import "encoding/json"

// These closed structural checks implement only P from the unchanged wire
// schema. Native correlations, catalog support and freshness are additional
// checks; no schema/compiler plugin or new dependency can supply that authority.
func fields(m map[string]any, names ...string) bool {
	if len(m) != len(names) {
		return false
	}
	for _, name := range names {
		if _, ok := m[name]; !ok {
			return false
		}
	}
	return true
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func text(v any) string           { s, _ := v.(string); return s }
func items(v any, lower, upper int) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok && len(a) >= lower && len(a) <= upper
}
func asciiToken(v any, upper int, rule bool) bool {
	s, ok := v.(string)
	if !ok || s == "" || len(s) > upper {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
		if rule && !ruleCharacter(c) {
			return false
		}
	}
	return true
}
func validBundleShape(m map[string]any) bool {
	if !fields(m, "schema_version", "key", "scope_incarnation", "revision", "context_epoch", "catalog_version", "catalog_digest", "bundle_digest", "invocation", "capability") || m["schema_version"] != json.Number("1") {
		return false
	}
	k := object(m["key"])
	if !fields(k, "origin", "bot_uid", "space_id") || !ValidOrigin(text(k["origin"])) || !ValidNativeID(text(k["bot_uid"])) || !ValidNativeID(text(k["space_id"])) {
		return false
	}
	if !validHex(text(m["scope_incarnation"]), 32) || !validHex(text(m["bundle_digest"]), 64) || !validHex(text(m["catalog_digest"]), 64) || !asciiToken(m["catalog_version"], 64, false) {
		return false
	}
	for _, name := range []string{"revision", "context_epoch"} {
		if _, err := U64(text(m[name])); err != nil {
			return false
		}
	}
	return validStage(object(m["invocation"]), false) && validStage(object(m["capability"]), true)
}
func validStage(m map[string]any, capability bool) bool { //nolint:gocyclo // independent U/E structure and capability-only fields are validated together
	if !fields(m, "state", "rules") || (m["state"] != "U" && m["state"] != "E") {
		return false
	}
	a, ok := items(m["rules"], 0, 128)
	if !ok || m["state"] == "U" && len(a) != 0 {
		return false
	}
	for _, v := range a {
		r := object(v)
		names := []string{"rule_id", "effect", "caller", "source"}
		if capability {
			names = append(names, "capabilities")
		}
		if !fields(r, names...) || !asciiToken(r["rule_id"], 64, true) || (r["effect"] != "allow" && r["effect"] != "deny") || !validCaller(object(r["caller"])) || !validSource(object(r["source"])) {
			return false
		}
		if capability {
			caps, ok := items(r["capabilities"], 1, 64)
			if !ok {
				return false
			}
			for _, id := range caps {
				if !asciiToken(id, 256, false) {
					return false
				}
			}
		}
	}
	return true
}
func validCaller(m map[string]any) bool {
	if m["kind"] == "all_humans" {
		return fields(m, "kind")
	}
	if m["kind"] != "uids" || !fields(m, "kind", "uids") {
		return false
	}
	a, ok := items(m["uids"], 1, 64)
	if !ok {
		return false
	}
	for _, v := range a {
		if !ValidNativeID(text(v)) {
			return false
		}
	}
	return true
}
func validSource(m map[string]any) bool { //nolint:gocyclo // each closed source variant has distinct required fields and native domains
	if m["kind"] == "all_in_space" {
		if !fields(m, "kind", "conversation_types") {
			return false
		}
		a, ok := items(m["conversation_types"], 1, 3)
		if !ok {
			return false
		}
		for _, v := range a {
			if v != "dm" && v != "group" && v != "thread" {
				return false
			}
		}
		return true
	}
	if m["kind"] != "refs" || !fields(m, "kind", "refs") {
		return false
	}
	a, ok := items(m["refs"], 1, 64)
	if !ok {
		return false
	}
	for _, v := range a {
		r := object(v)
		s := text(r["id"])
		switch r["kind"] {
		case "dm":
			if !fields(r, "kind", "id") || !validText(s, 100, 400) {
				return false
			}
		case "group":
			if !fields(r, "kind", "id", "include_descendants") || !ValidNativeID(s) {
				return false
			}
			if _, ok := r["include_descendants"].(bool); !ok {
				return false
			}
		case "thread":
			if !fields(r, "kind", "id") || !validText(s, 256, 256) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ruleCharacter(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'
}
