package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func FuzzObjectMembers(f *testing.F) {
	for _, seed := range []string{
		`{}`, ` { "id" : 1, "params": {"a":[true,null,-1.2e+3,"quoted\"}\\","line\r\nbreak"]}} `,
		`{"key":{},"key":[],"\u006bey":null}`, `{"quoted\"key":"\\\\\"","Params":{"a":[{"b":2}]}}`,
		`{"a":true,"b":null,"c":false,"d":-1.234e-12}`, `{"x":"界ſK"}`, `{"x":1} {}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if !json.Valid(raw) || !isJSONObject(raw) {
			if visitObjectMembers(raw, func(string, json.RawMessage) error { return nil }) == nil {
				t.Fatal("invalid object accepted")
			}
			return
		}
		type member struct {
			key   string
			value json.RawMessage
		}
		var want, got []member
		d := json.NewDecoder(bytes.NewReader(raw))
		_, _ = d.Token()
		for d.More() {
			key, err := d.Token()
			if err != nil {
				t.Fatal(err)
			}
			var value json.RawMessage
			if err := d.Decode(&value); err != nil {
				t.Fatal(err)
			}
			want = append(want, member{key.(string), value})
		}
		if err := visitObjectMembers(raw, func(key string, value json.RawMessage) error { got = append(got, member{key, value}); return nil }); err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatal("member count differs from encoding/json")
		}
		for i := range want {
			if want[i].key != got[i].key || !bytes.Equal(want[i].value, got[i].value) {
				t.Fatal("member differs from encoding/json")
			}
		}
	})
}
