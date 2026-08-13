package cmd

import "testing"

// TestParams_NullIsRejected is the sibling of the --data null defect. parseParamsJSON
// only ranges over the decoded map, and ranging a nil map is a no-op, so `--params
// null` silently meant "no query parameters" instead of being refused as not-an-object.
// No panic here, same root cause: a decode that succeeds and yields nil was unchecked.
func TestParams_NullIsRejected(t *testing.T) {
	if _, err := parseParamsJSON("null"); err == nil {
		t.Error("--params null is not a JSON object and must be refused rather than treated as absent")
	}
	if _, err := parseParamsJSON(`{"status":"open"}`); err != nil {
		t.Errorf("an object must still be accepted: %v", err)
	}
	// An absent value stays absent: "" is how the flag reports not being set.
	if v, err := parseParamsJSON(""); err != nil || v != nil {
		t.Errorf("an unset --params must stay absent, got %v / %v", v, err)
	}
}
