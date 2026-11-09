package permission

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestManagedIdentityWidthsAndNoLegacyFallback(t *testing.T) {
	for _, width := range []int{39, 40, 41} {
		uid := strings.Repeat("界", width)
		raw := []byte(`{"identity_version":2,"principal_kind":"user_bot","bot_uid":"` + uid + `"}`)
		v, err := ParseIdentityV2(raw, false)
		if width <= 40 {
			if err != nil || v.BotUID != uid {
				t.Fatal(width, err)
			}
		} else if err == nil {
			t.Fatal("oversized native identity")
		}
	}
	for _, raw := range []string{
		`{"identity_version":2.0,"principal_kind":"user_bot","bot_uid":"bot"}`,
		`{"identity_version":2,"principal_kind":"user_bot","bot_uid":"bot","owner_uid":"owner"}`,
		`{"bot_uid":"bot","bot_name":"legacy","owner_uid":"owner","space_id":"space"}`,
		`{"identity_version":2,"principal_kind":"user_bot","bot_uid":"bot","bot_\u0075id":"other"}`,
		`{"identity_version":2,"principal_kind":"user_bot","bot_uid":"bot","mcp_policy":null}`,
	} {
		if _, err := ParseIdentityV2([]byte(raw), false); err == nil {
			t.Fatal("accepted", raw)
		}
	}
}

func TestManagedDeclarationWideNativeAndExactKeys(t *testing.T) {
	obj := map[string]any{"version": 2, "im": map[string]any{"sender": strings.Repeat("界", 40), "space_id": strings.Repeat("界", 40), "channel": strings.Repeat("界", 40) + "@" + strings.Repeat("界", 40), "channel_type": 1, "message_id": "9223372036854775807"}}
	raw, _ := json.Marshal(obj)
	if _, err := ParseDeclarationV2(raw); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["message_id"] = "9223372036854775808" },
		func(m map[string]any) { m["message_id"] = "01" },
		func(m map[string]any) { m["sender"] = strings.Repeat("界", 41) },
		func(m map[string]any) { m["sender"] = "\u0001" },
		func(m map[string]any) { m["channel_type"] = 3 },
		func(m map[string]any) { m["context_trusted"] = true },
	} {
		var cloned map[string]any
		json.Unmarshal(raw, &cloned)
		mutate(cloned["im"].(map[string]any))
		bad, _ := json.Marshal(cloned)
		if _, err := ParseDeclarationV2(bad); err == nil {
			t.Fatal("accepted", string(bad))
		}
	}
	if _, err := ParseDeclarationV2([]byte(strings.Replace(string(raw), `"channel_type":1`, `"channel_type":1.0`, 1))); err == nil {
		t.Fatal("coerced type")
	}
}
