package permission

import (
	"strings"
	"testing"
)

func TestCatalogDepthAndThreeValueDomains(t *testing.T) {
	raw := []byte(strings.Repeat(`{"nested":`, 14) + `{"literal":null}` + strings.Repeat("}", 14))
	if _, _, err := catalogJSONValue(raw, true); err != nil {
		t.Fatal("catalog decoder rejected approved depth15 inherited null", err)
	}
	if _, _, err := catalogJSONValue([]byte(` {"literal":null,"n":1.0} `), false); err != nil {
		t.Fatal("raw JSON was subjected to metadata canonicalization", err)
	}
	if _, _, err := catalogJSONValue([]byte(`{"x":1,"\u0078":2}`), true); err == nil {
		t.Fatal("duplicate decoded metadata key accepted")
	}
	if ValidateCatalogDefinition("Condition", []byte(`{"argument":"x","literal":null,"op":"eq"}`)) != nil {
		t.Fatal("inherited eq:null rejected")
	}
	if ValidateCatalogDefinition("Condition", []byte(`{"argument":"x","op":"eq"}`)) == nil {
		t.Fatal("missing inherited eq literal accepted")
	}
	if ValidateCatalogDefinition("Part", []byte(`{"length":1,"offset":0,"path":null}`)) == nil {
		t.Fatal("new packaging path:null accepted")
	}
}

func TestRawRendererDomainDoesNotInheritMetadataFilters(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"x":null,"x":1.0}`), []byte(`{"x":"\ud800"}`), {'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}} {
		if _, _, err := catalogJSONValue(raw, false); err != nil {
			t.Fatal("source renderer JSON rejected by packaging metadata filters", err)
		}
	}
	_, work, err := catalogJSONValue([]byte(`{"x":null,"x":1.0}`), false)
	if err != nil || work.Nodes != 3 || work.Depth != 2 {
		t.Fatal("duplicate raw members must each count as a value", work, err)
	}
	slots := []CatalogSlot{{Domain: "facade_hint", ID: "hint", Pointer: "/slot", MaxLength: 65536}}
	if err := catalogValidateMarkers([]byte(`{"data":1,"data":2,"slot":{"$octo_slot":"hint"}}`), slots); err != nil {
		t.Fatal("unrelated duplicate raw member rejected", err)
	}
	for _, raw := range []string{`{"slot":{"$octo_slot":"hint"},"slot":{"$octo_slot":"hint"}}`, `{"slot":{"$octo_slot":"hint","$octo_slot":"hint"}}`} {
		if err := catalogValidateMarkers([]byte(raw), slots); err == nil {
			t.Fatal("ambiguous marker/pointer accepted")
		}
	}
}

func TestCatalogPostSlotJSONUsesExpandedCeiling(t *testing.T) {
	raw := []byte(`"` + strings.Repeat("a", 8388608) + `"`)
	if _, _, err := catalogJSONValue(raw, false); err == nil {
		t.Fatal("raw root exceeded its separate 8 MiB bound")
	}
	if work, err := catalogRawJSONWork(raw, 16777216); err != nil || work.Nodes != 1 || work.Depth != 1 {
		t.Fatal("post-slot JSON borrowed the smaller root ceiling", work, err)
	}
}
