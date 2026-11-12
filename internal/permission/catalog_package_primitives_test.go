package permission

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestPackageOccurrenceEquationAndRawDomains(t *testing.T) {
	raw := []byte(" {\"id\":\"op\",\"path\":\"/source\",\"value\":null,\"number\":1.0} \n")
	root := catalogHash(raw)
	template, row := "template", "row"
	v := &VerifiedCatalog{files: map[string][]byte{"blobs/0.bin": raw}, roots: map[string]catalogRoot{root: {SHA: root, Length: len(raw), Parts: []catalogPart{{Path: "blobs/0.bin", Length: len(raw)}}}}, rows: map[string]catalogRow{row: {SHA: row, Operation: "op", Match: "op  /source", Root: root}}, raw: map[string][]byte{}, uses: map[string][]catalogUse{template: {{Config: CatalogConfig{}}}}, ledger: CatalogLedger{MetadataNodes: 42}, content: map[string]map[string]any{root: {"kind": "bytes", "data_ref": root, "media_type": "application/json"}, template: {"kind": "template", "value_ref": root, "slots": []any{}}}}
	for _, key := range []string{"d1", "d2", "d3"} {
		v.content[key] = map[string]any{"kind": "discovery_index", "rows": []any{row}}
	}
	if err := v.verifyExpansion(context.Background()); err != nil {
		t.Fatal(err)
	}
	rendered, err := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// Four scalar values plus one object =5 JSON nodes. U has five uses:
	// one bytes, one template and three ordered row occurrences.
	if v.ledger.PreBytes != 5*len(raw) || v.ledger.PreNodes != 67 || v.ledger.PostBytes != 4*len(raw)+len(rendered) || v.ledger.PostNodes != 67 {
		t.Fatal("shared row/descriptor occurrence equation drift", v.ledger)
	}
	if !bytes.Equal(v.raw[root], raw) {
		t.Fatal("raw null/numeric spelling/whitespace was normalized")
	}
	binary := []byte{0xff, 0, 0xc3, 0xa9}
	hash := catalogHash(binary)
	opaque := &VerifiedCatalog{files: map[string][]byte{"blobs/0.bin": binary}, roots: map[string]catalogRoot{hash: {SHA: hash, Length: len(binary), Parts: []catalogPart{{Path: "blobs/0.bin", Length: 3}, {Path: "blobs/0.bin", Offset: 3, Length: 1}}}}, rows: map[string]catalogRow{}, raw: map[string][]byte{}, content: map[string]map[string]any{hash: {"kind": "bytes", "data_ref": hash, "media_type": "application/octet-stream"}}}
	if err = opaque.verifyExpansion(context.Background()); err != nil || !bytes.Equal(opaque.raw[hash], binary) || opaque.ledger.PreNodes != 0 {
		t.Fatal("opaque bytes or split UTF8 were treated as metadata JSON", err)
	}
}
