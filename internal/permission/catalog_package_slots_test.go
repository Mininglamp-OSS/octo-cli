package permission

import (
	"encoding/json"
	"testing"
)

func TestCatalogTemplateReservedKeysAndInheritedSlotDomains(t *testing.T) {
	t.Run("reserved_key", func(t *testing.T) {
		if err := catalogValidateMarkers([]byte(`{"data":{"$octo_slot":"unused","extra":1}}`), nil); err == nil {
			t.Fatal("reserved template key outside declared marker accepted")
		}
	})
	raw := []byte(`{"arguments":{"$octo_slot":"arguments"}}`)
	t.Run("managed_names", func(t *testing.T) {
		slot := CatalogSlot{Domain: "server_managed_argument_names", ID: "arguments", Pointer: "/arguments", MaxLength: 65536}
		if _, err := catalogRenderTemplate(raw, []CatalogSlot{slot}, map[string]any{"arguments": []string{}}); err == nil {
			t.Fatal("empty managed-name slot escaped inherited nonempty domain")
		}
		if _, err := catalogRenderTemplate(raw, []CatalogSlot{slot}, map[string]any{"arguments": []string{"space_id"}}); err != nil {
			t.Fatal("valid managed-name slot refused", err)
		}
	})
	t.Run("hint_type", func(t *testing.T) {
		slot := CatalogSlot{Domain: "facade_hint", ID: "arguments", Pointer: "/arguments", MaxLength: 65536}
		if _, err := catalogRenderTemplate(raw, []CatalogSlot{slot}, map[string]any{"arguments": []string{"hint"}}); err == nil {
			t.Fatal("facade hint accepted a non-string value")
		}
	})
	t.Run("source_value_type", func(t *testing.T) {
		slot := CatalogSlot{Domain: "facade_hint", ID: "arguments", Pointer: "/arguments", MaxLength: 65536}
		for _, value := range []any{string([]byte{0xff}), json.RawMessage(`"hint"`)} {
			if _, err := catalogRenderTemplate(raw, []CatalogSlot{slot}, map[string]any{"arguments": value}); err == nil {
				t.Fatal("source slot value was coerced to its JSON rendering")
			}
		}
	})
}
