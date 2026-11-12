package permission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CatalogPackageFormat is the approved closed v42 discriminator.
const CatalogPackageFormat = "permission-catalog-package-v2"

// CatalogPin commits one immutable manifest; matching a hash is not support approval.
type CatalogPin struct{ Version, Digest string }

// CatalogEntry retains the unchanged typed identity and semantic references.
type CatalogEntry struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Target      json.RawMessage `json:"target"`
	Aliases     []string        `json:"aliases"`
	Profile     string          `json:"profile"`
	ContentRefs []string        `json:"content_refs"`
}

// CatalogBaseline is private compiled source evidence, independent of a package.
type CatalogBaseline struct {
	Entries     []CatalogEntry
	SourceFiles [][]string
}

// CatalogConfig names only finite, privately verified configuration choices.
type CatalogConfig struct {
	Resources           bool
	ManagedMask, Facade int
}

// CatalogSlot is the unchanged whole-value slot contract.
type CatalogSlot struct {
	Domain    string `json:"domain"`
	ID        string `json:"id"`
	MaxLength int    `json:"max_length"`
	Pointer   string `json:"pointer"`
}

// CatalogSlotValues supplies joint source-valid values, never model input.
type CatalogSlotValues func(operation string, config CatalogConfig, raw []byte, slots []CatalogSlot) (map[string]any, error)

type catalogPart struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}
type catalogRoot struct {
	SHA    string        `json:"sha256"`
	Length int           `json:"length"`
	Parts  []catalogPart `json:"parts"`
}
type catalogRow struct {
	SHA       string `json:"sha256"`
	Operation string `json:"operation_id"`
	Match     string `json:"match_text"`
	Root      string `json:"row_ref"`
}
type catalogSelector struct {
	Resources int `json:"resources_bits"`
	Managed   int `json:"managed_bits"`
	Facade    int `json:"facade_bits"`
}
type catalogBinding struct {
	Entry    string          `json:"entry_id"`
	Selector catalogSelector `json:"selector"`
	Content  string          `json:"content_ref"`
}
type catalogUse struct {
	Operation string
	Config    CatalogConfig
}

// CatalogLedger implements the approved occurrence equations; no value is a
// business-response, local-input or relay limit.
type CatalogLedger struct {
	StoredBytes    int `json:"stored_bytes"`
	PreBytes       int `json:"pre_bytes"`
	PostBytes      int `json:"post_bytes"`
	MetadataNodes  int `json:"metadata_nodes"`
	PreNodes       int `json:"pre_nodes"`
	PostNodes      int `json:"post_nodes"`
	MaxDepth       int `json:"max_depth"`
	Files          int `json:"files"`
	Roots          int `json:"roots"`
	Rows           int `json:"shared_rows"`
	Content        int `json:"content_records"`
	Parts          int `json:"parts_total"`
	MaxParts       int `json:"max_parts_per_root"`
	SchemaCells    int `json:"schema_cells"`
	DiscoveryCells int `json:"discovery_cells"`
	StaticCells    int `json:"static_cells"`
}

// VerifiedCatalog is an immutable, complete structural snapshot. A private
// support registry additionally requires publisher/source and bound evidence.
type VerifiedCatalog struct {
	pin           CatalogPin
	files         map[string][]byte
	entries       map[string]CatalogEntry
	content       map[string]map[string]any
	owners        map[string]string
	roots         map[string]catalogRoot
	rows          map[string]catalogRow
	raw           map[string][]byte
	profiles      map[string]map[string]any
	schemas       map[string]string
	discovery     map[string]string
	static        map[string]string
	uses          map[string][]catalogUse
	index         map[string]any
	manifest      map[string]any
	ledger        CatalogLedger
	semantic      []byte
	renderedMax   map[string]int
	slots         CatalogSlotValues
	frozenSlots   map[string][]byte
	renderedSlots map[string]string
}

// Pin returns the exact immutable package commitment.
func (v *VerifiedCatalog) Pin() CatalogPin { return v.pin }

// Ledger returns a detached complete work ledger.
func (v *VerifiedCatalog) Ledger() CatalogLedger { return v.ledger }

// File returns a copy of exact verified bytes from the closed inventory.
func (v *VerifiedCatalog) File(path string) ([]byte, bool) {
	b, ok := v.files[path]
	return append([]byte(nil), b...), ok
}

// Catalog returns a detached coordinator identity set, not an authority grant.
func (v *VerifiedCatalog) Catalog() Catalog {
	caps := make([]string, 0, len(v.entries))
	for id := range v.entries {
		caps = append(caps, id)
	}
	sort.Strings(caps)
	return Catalog{v.pin.Version, v.pin.Digest, caps}
}

// SameSemantics compares the complete representation-neutral validated views.
func (v *VerifiedCatalog) SameSemantics(other *VerifiedCatalog) bool {
	return other != nil && bytes.Equal(v.semantic, other.semantic)
}

// SemanticDigest accelerates comparison without overriding full view equality.
func (v *VerifiedCatalog) SemanticDigest() string { return catalogHash(v.semantic) }

func catalogHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func catalogEncode(value any) []byte {
	raw, err := CanonicalSourceValue(value)
	if err != nil {
		panic(err)
	}
	return raw
}
func catalogDecodeInto(value, target any) error { return json.Unmarshal(canonical(value), target) }
func catalogDescriptorHash(record map[string]any) string {
	m := map[string]any{}
	for k, v := range record {
		if k != "sha256" {
			m[k] = v
		}
	}
	return catalogHash(canonical(m))
}
func catalogCell(prefix string, c CatalogConfig) string {
	r := 0
	if c.Resources {
		r = 1
	}
	return fmt.Sprintf("%s/%d/%d/%d", prefix, r, c.ManagedMask, c.Facade)
}

func catalogSlotCell(template, operation string, c CatalogConfig) string {
	return catalogCell(template+"|"+operation, c)
}
func catalogCells(selector catalogSelector, discovery bool) []CatalogConfig {
	out := []CatalogConfig{}
	for r := 0; r < 2; r++ {
		for m := 0; m < 8; m++ {
			for f := 0; f < 3; f++ {
				if selector.Resources&(1<<r) != 0 && selector.Facade&(1<<f) != 0 && (discovery || selector.Managed&(1<<m) != 0) {
					out = append(out, CatalogConfig{r == 1, m, f})
				}
			}
			if discovery {
				break
			}
		}
	}
	return out
}

// VerifyCatalogPackage verifies every byte/typed edge before returning a snapshot.
// The expected pin and baseline come from private trusted composition.
func VerifyCatalogPackage(ctx context.Context, files map[string][]byte, expected CatalogPin, baseline CatalogBaseline, slots CatalogSlotValues) (*VerifiedCatalog, error) {
	v := &VerifiedCatalog{pin: expected, files: map[string][]byte{}, entries: map[string]CatalogEntry{}, content: map[string]map[string]any{}, owners: map[string]string{}, roots: map[string]catalogRoot{}, rows: map[string]catalogRow{}, raw: map[string][]byte{}, profiles: map[string]map[string]any{}, schemas: map[string]string{}, discovery: map[string]string{}, static: map[string]string{}, uses: map[string][]catalogUse{}, slots: slots}
	if err := v.verifyFiles(ctx, files, baseline); err != nil {
		return nil, err
	}
	if err := v.verifyGraph(ctx, baseline); err != nil {
		return nil, err
	}
	if err := v.verifyExpansion(ctx); err != nil {
		return nil, err
	}
	v.slots = nil // Published snapshots use detached verified values only.
	v.semantic = v.semanticView()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return v, nil
}
