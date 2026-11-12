package mcp

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestCatalogTemplateHashBindsDescriptorAndRejectsMetadataDrift(t *testing.T) {
	template, err := templateFor([]byte(`{"id":"synthetic","server_managed_arguments":["space_id"]}`))
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256Sum(catalogJSON(map[string]any{"kind": template.Kind, "media_type": template.Media, "renderer": template.Renderer, "value_base64": template.Value, "slots": template.Slots}))
	if template.SHA != expected {
		t.Fatal("template hash omits descriptor metadata")
	}
	s, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.trusted.SpaceID = "synthetic-space"
	if _, err = renderTemplate(&template, s, "message.send"); err != nil {
		t.Fatal("valid descriptor did not render", err)
	}
	template.Slots[0].MaxLength++
	if _, err = renderTemplate(&template, s, "message.send"); err == nil {
		t.Fatal("descriptor metadata drift accepted")
	}
	// The old raw-payload hash is not a descriptor hash.
	raw, _ := base64.StdEncoding.DecodeString(template.Value)
	template.SHA = sha256Sum(raw)
	if _, err = renderTemplate(&template, s, "message.send"); err == nil {
		t.Fatal("raw payload hash accepted as descriptor hash")
	}
}

func TestCatalogAssemblyAccountsForEveryByteAndOnlyActualMissingPageWitnesses(t *testing.T) {
	raw, err := AssemblePermissionRelease()
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Analysis struct {
			Values  map[string]int `json:"top_level_value_bytes"`
			Framing int            `json:"top_level_framing_bytes"`
		} `json:"size_analysis"`
		Bytes int `json:"assembled_bytes"`
		Pages []struct {
			ID string `json:"capability"`
		} `json:"unproved_pagination"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Analysis.Values) == 0 {
		t.Fatal("assembly has no attributable byte ledger")
	}
	total := report.Analysis.Framing
	for _, n := range report.Analysis.Values {
		total += n
	}
	if total != report.Bytes {
		t.Fatal("byte ledger does not account for complete assembly", total, report.Bytes)
	}
	if len(report.Pages) != 4 {
		t.Fatal("missing-page list ignores exact mounted media-search witness", report.Pages)
	}
	for _, p := range report.Pages {
		if p.ID == "octo-cli/message/op/message.search.media" {
			t.Fatal("mounted bounded media-search route reported absent")
		}
	}
}
