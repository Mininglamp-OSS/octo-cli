package permission

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) Bundle {
	t.Helper()
	b, err := os.ReadFile("contracts/server-synthetic-examples-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Bundle json.RawMessage `json:"bundle"`
	}
	if err = json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	p, err := ParseBundle(bytes.NewReader(v.Bundle))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSharedServerBundleAndStrictLexicalFailures(t *testing.T) {
	p := fixture(t)
	_, raw, err := Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Digest != "4651e0f02e04ccb5e3e2d622d74656a1b4e1429bbebdf6ade5c7c1088a79b7bf" {
		t.Fatal("server digest drift")
	}
	for _, raw := range []string{
		strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1.0`, 1),
		strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_versio\u006e":1`, 1),
		strings.Replace(string(raw), `"revision":"0"`, `"revision":"18446744073709551616"`, 1),
		strings.Replace(string(raw), `"bot_uid":"bot"`, `"bot_uid":"\ud800"`, 1),
		strings.Replace(string(raw), `"rules":[]`, `"rules":null`, 1),
		string(raw) + `{}`, string(raw[:len(raw)-1]) + `,"extra":false}`,
	} {
		if _, err = ParseBundle(strings.NewReader(raw)); err == nil {
			t.Fatalf("accepted invalid wire %.200s", raw)
		}
	}
}

func rule(id, effect, caller, source, capability string, desc bool) Rule {
	r := Rule{"rule_id": id, "effect": effect,
		"caller": map[string]any{"kind": "uids", "uids": []any{caller}},
		"source": map[string]any{"kind": "refs", "refs": []any{map[string]any{"kind": "group", "id": source, "include_descendants": desc}}}}
	if capability != "" {
		r["capabilities"] = []any{capability}
	}
	return r
}

func TestWholeRuleAndExplicitDescendantSemantics(t *testing.T) {
	s := Subject{Caller: "alice", Kind: "group", Source: "group-a"}
	a := rule("a", "allow", "alice", "group-b", "", false)
	b := rule("b", "allow", "bob", "group-a", "", false)
	if stageAllows(Stage{"E", []Rule{a, b}}, s, "") {
		t.Fatal("cross product granted")
	}
	a = rule("a", "allow", "alice", "group-a", "", false)
	d := rule("d", "deny", "alice", "group-a", "", false)
	if stageAllows(Stage{"E", []Rule{a, d}}, s, "") {
		t.Fatal("deny must win")
	}
	s.Kind = "thread"
	s.Source = "thread-a"
	s.Parent = "group-a"
	a = rule("a", "allow", "alice", "group-a", "", true)
	if !stageAllows(Stage{"E", []Rule{a, d}}, s, "") {
		t.Fatal("non-descendant parent deny applied")
	}
	d = rule("d", "deny", "alice", "group-a", "", true)
	if stageAllows(Stage{"E", []Rule{a, d}}, s, "") {
		t.Fatal("descendant deny missed")
	}
	if !stageAllows(Stage{"U", []Rule{}}, s, "") || stageAllows(Stage{"E", []Rule{}}, s, "") {
		t.Fatal("U/E empty semantics")
	}
}

func TestCanonicalScalarOrderAndGroupDefault(t *testing.T) {
	p := fixture(t)
	p.Invocation = Stage{"E", []Rule{rule("a", "allow", "\U00010000", "group", "", false)}}
	p.Invocation.Rules[0]["caller"].(map[string]any)["uids"] = []any{"\U00010000", "\ue000"}
	ref := p.Invocation.Rules[0]["source"].(map[string]any)["refs"].([]any)[0].(map[string]any)
	delete(ref, "include_descendants")
	sealed, raw, err := Seal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"uids":["`+"\ue000"+`","`+"\U00010000"+`"]`)) {
		t.Fatal("UTF8 set order", string(raw))
	}
	if ref["include_descendants"] != nil {
		t.Fatal("seal mutated caller state")
	}
	if _, err = ParseBundle(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if sealed.Digest == p.Digest {
		t.Fatal("digest did not include stage")
	}
}
