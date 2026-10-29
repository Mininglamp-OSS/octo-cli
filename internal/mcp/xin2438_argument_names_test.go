package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestXIN2438ForcedFieldLookalikeRefused: a key that folds to a declared field
// (U+017F LATIN SMALL LETTER LONG S folds to "s") would be bound to space_id
// by a Go backend after the forced value, so the connection's Space would not
// confine the call. It must be refused on both execute and call_op, and the
// canonical call must still decode to the forced Space.
func TestXIN2438ForcedFieldLookalikeRefused(t *testing.T) {
	var hits int
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"group_no":"g1"}`))
	}))
	defer srv.Close()
	s := facadeServer(t, facadeBoth)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{SpaceID: "forced-space"}

	for _, key := range []string{"\u017fpace_id", "SPACE_ID", "Space_Id", "Members"} {
		args := map[string]any{"name": "fixture", "members": []any{"u1"}, key: "other-space"}
		for _, tool := range []string{"execute", "call_op"} {
			res := callTool(t, s, tool, map[string]any{"operation_id": "group.create", "arguments": args})
			out := toolText(t, res)
			if !res.IsError || out["status"] == "ok" || out["ok"] == true {
				t.Errorf("%s with %q must be refused, got %s", tool, key, res.Content[0].Text)
			}
		}
	}
	if hits != 0 {
		t.Fatalf("a look-alike key reached the backend %d times", hits)
	}

	res := callTool(t, s, "execute", map[string]any{"operation_id": "group.create",
		"arguments": map[string]any{"name": "fixture", "members": []any{"u1"}, "space_id": "other-space"}})
	if res.IsError {
		t.Fatalf("canonical call must succeed: %s", res.Content[0].Text)
	}
	// Decode the wire body the way the Go backend binds it.
	var bound struct {
		SpaceID string `json:"space_id"`
	}
	if err := json.Unmarshal(lastBody, &bound); err != nil {
		t.Fatal(err)
	}
	if bound.SpaceID != "forced-space" {
		t.Errorf("backend-decoded space_id = %q, want forced-space (body %s)", bound.SpaceID, lastBody)
	}
}

// TestXIN2438CheckArgumentNames isolates the name rule from the engine.
func TestXIN2438CheckArgumentNames(t *testing.T) {
	body := map[string]registry.SchemaInfo{"space_id": {}, "name": {}}
	cases := []struct {
		args map[string]any
		ok   bool
	}{
		{map[string]any{"space_id": "s", "name": "n", "extra_field": 1}, true},
		{map[string]any{"\u017fpace_id": "s"}, false},
		{map[string]any{"NAME": "n"}, false},
		{map[string]any{"foo": 1, "FOO": 2}, false},
		{map[string]any{"dry_run": true}, false},
		{map[string]any{"dryRun": true}, false},
		{map[string]any{"schema-fingerprint": "x"}, false},
	}
	for _, c := range cases {
		err := checkArgumentNames(context.Background(), c.args, map[string]bool{}, nil, nil, body)
		if (err == nil) != c.ok {
			t.Errorf("checkArgumentNames(%v) error = %v, want ok=%v", c.args, err, c.ok)
		}
	}
}
