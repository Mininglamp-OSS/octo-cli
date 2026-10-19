package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// The lossless-uint64 contract used to hold on the promoted-flag path and lapse
// on --data: resolveBody decoded with a plain json.Unmarshal, so every number
// became a float64 and an id above 2^53 was rounded before anything could check
// it. A rounded id is a *valid* id pointing at a different row, so for
// parent_id — which selects the destination folder of drive file move / folder
// move / doc mount — the request succeeds and the file lands somewhere the
// caller did not ask for. These tests pin both halves of the fix: UseNumber in
// resolveBody, and the format:uint64 branch in the body schema walker.

// TestBodyUint64_DataPathIsLossless asserts an id above 2^53 supplied through
// --data reaches the wire with its exact digits, matching the promoted flag.
func TestBodyUint64_DataPathIsLossless(t *testing.T) {
	// 2^53+1: the smallest integer a float64 cannot represent, so a rounding
	// decode turns it into ...992.
	const beyondFloat64 = "9007199254740993"

	for _, tc := range []struct {
		name string
		args []string
	}{
		{"via --data", []string{"drive", "file", "move", "42", "--data", `{"parent_id":` + beyondFloat64 + `}`}},
		{"via the promoted flag", []string{"drive", "file", "move", "42", "--parent-id", beyondFloat64}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				_, _ = w.Write([]byte(`{"id":42}`))
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if want := `"parent_id":` + beyondFloat64; !strings.Contains(gotBody, want) {
				t.Errorf("request body: got %s, want it to contain %s", gotBody, want)
			}
		})
	}
}

// TestBodyUint64_DataPathRejectsBadIDs asserts every id shape the flag path
// already refused is refused through --data too, with zero HTTP. The nested and
// array cases matter because the walker is the only place that sees them: there
// is no promoted flag for a field below the root.
func TestBodyUint64_DataPathRejectsBadIDs(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"above the uint64 max", `{"parent_id":99999999999999999999999}`},
		{"negative", `{"parent_id":-5}`},
		{"decimal point", `{"parent_id":1.5}`},
		{"exponent form", `{"parent_id":1e3}`},
		{"quoted decimal string", `{"parent_id":"42"}`},
		{"boolean", `{"parent_id":true}`},
		{"object where an id belongs", `{"parent_id":{"id":1}}`},
		{"array where an id belongs", `{"parent_id":[1]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			root.SetArgs([]string{"drive", "file", "move", "42", "--data", tc.data})
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a local validation error")
			}
			if called {
				t.Error("the request must not be sent when an id fails validation")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
				t.Errorf("taxonomy: got %v, want a validation error", err)
			}
		})
	}
}

// TestBodyUint64_WalkerReachesEveryDepth drives the walker directly against a
// synthetic schema, because no embedded spec nests a uint64 id inside an object
// or an array today. The walker is shared, so covering the depths here is what
// makes the --data guarantee hold for the first spec that does.
func TestBodyUint64_WalkerReachesEveryDepth(t *testing.T) {
	idSchema := registry.SchemaInfo{Type: "integer", Format: uint64Format}
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"top": idSchema,
			"nested": {
				Type:       "object",
				Properties: map[string]registry.SchemaInfo{"inner": idSchema},
			},
			"list": {Type: "array", Items: &idSchema},
		},
	}

	cases := []struct {
		name    string
		body    map[string]any
		wantErr bool
	}{
		{"top level ok", map[string]any{"top": json.Number("18446744073709551615")}, false},
		{"top level out of range", map[string]any{"top": json.Number("18446744073709551616")}, true},
		{"nested ok", map[string]any{"nested": map[string]any{"inner": json.Number("7")}}, false},
		{"nested negative", map[string]any{"nested": map[string]any{"inner": json.Number("-1")}}, true},
		{"array element ok", map[string]any{"list": []any{json.Number("1"), json.Number("2")}}, false},
		{"array element beyond 2^53 is fine", map[string]any{"list": []any{json.Number("9007199254740993")}}, false},
		{"array element out of range", map[string]any{"list": []any{json.Number("1"), json.Number("-3")}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := bodySchemaValidator{}
			err := v.validate(schema, tc.body, "", "")
			if tc.wantErr && err == nil {
				t.Fatalf("body %v: expected a validation error", tc.body)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("body %v: unexpected error %v", tc.body, err)
			}
		})
	}
}

// TestBodyUint64_DataStaysAsStrictAsUnmarshal guards the one behaviour a
// json.Decoder does not share with json.Unmarshal: a Decoder stops after the
// first value instead of rejecting trailing bytes, so swapping one for the other
// would have quietly accepted two concatenated JSON documents.
func TestBodyUint64_DataStaysAsStrictAsUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"trailing object", `{"parent_id":1}{"parent_id":2}`},
		{"trailing junk", `{"parent_id":1} nonsense`},
		{"array instead of object", `[{"parent_id":1}]`},
		// A stray closing delimiter is the case dec.More() missed: at top level
		// More reports whether another element follows *inside* the current array
		// or object, so it answers false for "]" and "}" and let these through.
		{"trailing close bracket", `{"parent_id":1}]`},
		{"trailing close brace", `{"parent_id":1}}`},
		{"trailing comma", `{"parent_id":1},`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			root.SetArgs([]string{"drive", "file", "move", "42", "--data", tc.data})
			if err := root.Execute(); err == nil {
				t.Fatal("expected a local validation error")
			}
			if called {
				t.Error("the request must not be sent when --data is malformed")
			}
		})
	}
}
