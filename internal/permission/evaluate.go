package permission

// Subject is the exact caller/original-source tuple returned by a trusted
// native provider. Parsing a declaration or thread ID cannot supply Parent.
// Eligibility, original visibility and complete context provenance must be
// independently established before evaluating a rule.
type Subject struct {
	Caller string
	Kind   string
	Source string
	Parent string
}

func contains(a any, s string) bool {
	for _, v := range a.([]any) {
		if v == s {
			return true
		}
	}
	return false
}

// stageAllows is only called on a complete, strictly parsed bundle. U retains
// its legacy path; it does not exempt binding/native ACL/no-OBO checks.
func stageAllows(stage Stage, subject Subject, capability string) bool {
	if stage.State == "U" {
		return true
	}
	if stage.State != "E" {
		return false
	}
	allowed := false
	for _, rule := range stage.Rules {
		caller := rule["caller"].(map[string]any)
		if caller["kind"] != "all_humans" && !contains(caller["uids"], subject.Caller) {
			continue
		}
		if !sourceMatches(rule["source"].(map[string]any), subject) {
			continue
		}
		if capability != "" && !contains(rule["capabilities"], capability) {
			continue
		}
		if rule["effect"] == "deny" {
			return false
		}
		if rule["effect"] == "allow" {
			allowed = true
		}
	}
	return allowed
}

func sourceMatches(source map[string]any, subject Subject) bool {
	if source["kind"] == "all_in_space" {
		return contains(source["conversation_types"], subject.Kind)
	}
	for _, v := range source["refs"].([]any) {
		ref := v.(map[string]any)
		if ref["kind"] == subject.Kind && ref["id"] == subject.Source {
			return true
		}
		if subject.Kind == "thread" && subject.Parent != "" && ref["kind"] == "group" &&
			ref["id"] == subject.Parent && ref["include_descendants"] == true {
			return true
		}
	}
	return false
}
