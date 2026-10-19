package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

// NormalizeResponse is the output half of the lossless-id contract: uint64 ids
// leave the CLI as decimal strings so an Agent's JSON parser cannot round them,
// and ambiguous backend field names are split into unambiguous ones.

func TestNormalizeResponse_NoMetadataIsAByteForBytePassthrough(t *testing.T) {
	in := []byte(`{"id":18446744073709551615,"nested":{"a":1}}`)
	got, err := NormalizeResponse(in, nil, nil)
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	if !bytes.Equal(got, in) {
		t.Errorf("body must be untouched without metadata:\n got %s\nwant %s", got, in)
	}
}

func TestNormalizeResponse_LosslessFields(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		fields []string
		want   map[string]string // json path expectations, as decimal strings
	}{
		{
			name:   "top-level ids",
			in:     `{"id":18446744073709551615,"parent_id":0,"name":"f"}`,
			fields: []string{"id", "parent_id"},
			want:   map[string]string{"id": "18446744073709551615", "parent_id": "0"},
		},
		{
			name:   "nested object path",
			in:     `{"root":{"id":9007199254740993,"parent_id":7},"total_rows":3}`,
			fields: []string{"root.id", "root.parent_id"},
			want:   map[string]string{"root.id": "9007199254740993", "root.parent_id": "7"},
		},
		{
			name:   "array element path",
			in:     `{"entries":[{"id":1},{"id":18446744073709551615}]}`,
			fields: []string{"entries[].id"},
			want:   map[string]string{"entries.0.id": "1", "entries.1.id": "18446744073709551615"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeResponse([]byte(tc.in), nil, tc.fields)
			if err != nil {
				t.Fatalf("NormalizeResponse: %v", err)
			}
			var doc any
			if err := json.Unmarshal(got, &doc); err != nil {
				t.Fatalf("parse result: %v (%s)", err, got)
			}
			for path, want := range tc.want {
				v := lookupPath(t, doc, path)
				s, ok := v.(string)
				if !ok {
					t.Errorf("%s: got %T %v, want a decimal string", path, v, v)
					continue
				}
				if s != want {
					t.Errorf("%s: got %q, want %q", path, s, want)
				}
			}
		})
	}
}

// A field the response does not carry, or one that is already a string, must be
// left alone rather than turned into a null or a double-quoted quote.
func TestNormalizeResponse_LosslessSkipsMissingAndNonNumeric(t *testing.T) {
	in := []byte(`{"id":"already-a-string","other":null}`)
	got, err := NormalizeResponse(in, nil, []string{"id", "other", "absent", "deep.absent", "absent[].id"})
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc["id"] != "already-a-string" {
		t.Errorf("id: got %v", doc["id"])
	}
	if doc["other"] != nil {
		t.Errorf("other: got %v, want null preserved", doc["other"])
	}
	if _, ok := doc["absent"]; ok {
		t.Error("a missing path must not be materialised")
	}
}

func TestNormalizeResponse_FieldAliases(t *testing.T) {
	// One source fanning out to two targets: the drive share DTO's single `id`
	// carries two meanings, and both must be addressable by name.
	got, err := NormalizeResponse(
		[]byte(`{"id":"tok-1","file_id":42,"permission":"view"}`),
		map[string][]string{"id": {"share_id", "share_token"}, "file_id": {"drive_file_id"}},
		[]string{"drive_file_id"},
	)
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc["share_id"] != "tok-1" || doc["share_token"] != "tok-1" {
		t.Errorf("aliases: got %v", doc)
	}
	if _, ok := doc["id"]; ok {
		t.Error("the source key must be removed once aliased")
	}
	// Aliases run before lossless, so a lossless path may name the alias.
	if doc["drive_file_id"] != "42" {
		t.Errorf("drive_file_id: got %v, want the decimal string \"42\"", doc["drive_file_id"])
	}
	if doc["permission"] != "view" {
		t.Errorf("unrelated fields must survive: %v", doc)
	}
}

// An alias that includes the source name keeps the key rather than deleting it,
// so a spec can add a second name without dropping the original.
func TestNormalizeResponse_AliasKeepingSourceName(t *testing.T) {
	got, err := NormalizeResponse(
		[]byte(`{"id":"x"}`),
		map[string][]string{"id": {"id", "share_id"}},
		nil,
	)
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc["id"] != "x" || doc["share_id"] != "x" {
		t.Errorf("got %v, want both keys present", doc)
	}
}

func TestNormalizeResponse_AliasesInsideArrays(t *testing.T) {
	got, err := NormalizeResponse(
		[]byte(`{"shares":[{"id":"a","file_id":1},{"id":"b","file_id":2}]}`),
		map[string][]string{"shares[].id": {"share_id", "share_token"}, "shares[].file_id": {"drive_file_id"}},
		[]string{"shares[].drive_file_id"},
	)
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	var doc struct {
		Shares []map[string]any `json:"shares"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(doc.Shares) != 2 {
		t.Fatalf("got %v", doc.Shares)
	}
	for i, want := range []string{"a", "b"} {
		if doc.Shares[i]["share_id"] != want || doc.Shares[i]["share_token"] != want {
			t.Errorf("shares[%d]: got %v", i, doc.Shares[i])
		}
		if _, ok := doc.Shares[i]["id"]; ok {
			t.Errorf("shares[%d]: source key must be removed", i)
		}
	}
	if doc.Shares[0]["drive_file_id"] != "1" {
		t.Errorf("shares[0].drive_file_id: got %v", doc.Shares[0]["drive_file_id"])
	}
}

// A non-JSON body (a binary describe envelope, say) must pass through rather
// than fail the command.
func TestNormalizeResponse_NonJSONPassesThrough(t *testing.T) {
	in := []byte("not json at all")
	got, err := NormalizeResponse(in, map[string][]string{"id": {"x"}}, []string{"id"})
	if err != nil {
		t.Fatalf("NormalizeResponse: %v", err)
	}
	if !bytes.Equal(got, in) {
		t.Errorf("got %q, want the input unchanged", got)
	}
}

// --- uint64 input validation ---

func TestParseUint64Decimal_Accepts(t *testing.T) {
	cases := map[string]uint64{
		"0":                    0,
		"1":                    1,
		"9007199254740993":     9007199254740993, // above 2^53: a float64 would round this
		MaxUint64Decimal:       18446744073709551615,
		"00000000000000000042": 42, // leading zeros are still plain decimal
	}
	for in, want := range cases {
		got, err := ParseUint64Decimal("--parent-id", in)
		if err != nil {
			t.Errorf("ParseUint64Decimal(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseUint64Decimal(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseUint64Decimal_Rejects(t *testing.T) {
	// Everything a JavaScript-based Agent might hand over after mangling an id.
	for _, in := range []string{
		"",
		"-1",
		"1.0",
		"1.5",
		"1e3",
		"1E3",
		"0x2a",
		" 42",
		"42 ",
		"4_2",
		"+42",
		"18446744073709551616", // 2^64, one past the maximum
		"99999999999999999999999",
		"abc",
		"1,000",
		"NaN",
		"Infinity",
	} {
		got, err := ParseUint64Decimal("--parent-id", in)
		if err == nil {
			t.Errorf("ParseUint64Decimal(%q) = %d, want a validation error", in, got)
			continue
		}
		if err.Type != "validation" || err.ExitCode() != 2 {
			t.Errorf("ParseUint64Decimal(%q): taxonomy %s/%d, want validation/2", in, err.Type, err.ExitCode())
		}
	}
}

// Uint64JSONNumber must marshal as a bare JSON integer so the wire contract is
// unchanged even though the CLI surface is a decimal string.
func TestUint64JSONNumber_MarshalsAsInteger(t *testing.T) {
	body := map[string]any{"parent_id": Uint64JSONNumber(18446744073709551615)}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if want := `{"parent_id":18446744073709551615}`; string(raw) != want {
		t.Errorf("got %s, want %s", raw, want)
	}
}

// --- helpers ---

// lookupPath walks a dotted path where numeric segments index arrays.
func lookupPath(t *testing.T, doc any, path string) any {
	t.Helper()
	cur := doc
	for _, seg := range splitDots(path) {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[seg]
		case []any:
			idx := 0
			for _, c := range seg {
				idx = idx*10 + int(c-'0')
			}
			if idx >= len(node) {
				t.Fatalf("path %q: index %d out of range", path, idx)
			}
			cur = node[idx]
		default:
			t.Fatalf("path %q: cannot descend into %T", path, cur)
		}
	}
	return cur
}

func splitDots(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
