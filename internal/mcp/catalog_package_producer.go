package mcp

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

//go:embed catalog_sources/source-files-v6.tar
var catalogPinnedSourceFiles []byte

// CatalogPackageProduction is an offline complete candidate, never a support grant.
type CatalogPackageProduction struct {
	Files              map[string][]byte
	Pin                permission.CatalogPin
	Conformance        map[string]int
	WholeStoredBytes   int
	FactoringThreshold int
	Ledger             permission.CatalogLedger
}

type packageProducer struct {
	content  map[string]map[string]any
	raw      map[string][]byte
	rows     map[string]map[string]any
	refs     map[string]map[string]bool
	bindings []map[string]any
	source   sourceIndex
	profiles []map[string]any
	sources  [][]string
	checks   map[string]int
	post     map[string]sourceJSONWork
}

func (p *packageProducer) root(raw []byte) string {
	h := sha256Sum(raw)
	p.raw[h] = append([]byte(nil), raw...)
	return h
}
func (p *packageProducer) put(m map[string]any) string {
	key, ok := m["sha256"].(string)
	if !ok {
		key = sha256Sum(catalogCanonicalJSON(m))
		if m["kind"] != "schema_variant" {
			m["sha256"] = key
		}
	}
	if prior, exists := p.content[key]; exists && !bytes.Equal(catalogCanonicalJSON(prior), catalogCanonicalJSON(m)) {
		panic("catalog cross-kind/content collision")
	}
	p.content[key] = m
	return key
}
func (p *packageProducer) bind(entry, hash string) {
	if p.refs[entry] == nil {
		p.refs[entry] = map[string]bool{}
	}
	p.refs[entry][hash] = true
}
func decodeTemplateBytes(value string) ([]byte, error) { return base64.StdEncoding.DecodeString(value) }
func (p *packageProducer) template(raw []byte) (string, error) {
	t, err := templateFor(raw)
	if err != nil {
		return "", err
	}
	payload, err := decodeTemplateBytes(t.Value)
	if err != nil {
		return "", err
	}
	hash := p.put(map[string]any{"kind": "template", "media_type": t.Media, "renderer": t.Renderer, "value_ref": p.root(payload), "slots": t.Slots})
	actual, err := json.MarshalIndent(json.RawMessage(raw), "", "  ")
	if err != nil {
		return "", err
	}
	work := sourceJSONStats(actual)
	prior := p.post[hash]
	prior.Bytes = max(prior.Bytes, len(actual))
	prior.Nodes = max(prior.Nodes, work.Nodes)
	prior.Depth = max(prior.Depth, work.Depth)
	p.post[hash] = prior
	return hash, nil
}

type producerCell struct{ resources, mask, facade int }

// Rectangles are merged only when every cell has the same exact descriptor.
// This is finite grouping, without precedence, wildcards or inferred invariance.
func producerSelectors(cells []producerCell, discovery bool) []map[string]int {
	grid := [2][3]int{}
	for _, c := range cells {
		grid[c.resources][c.facade] |= 1 << c.mask
	}
	byShape := map[[2]int]int{}
	for r := 0; r < 2; r++ {
		byMask := map[int]int{}
		for f := 0; f < 3; f++ {
			if grid[r][f] != 0 {
				byMask[grid[r][f]] |= 1 << f
			}
		}
		for mask, facades := range byMask {
			byShape[[2]int{mask, facades}] |= 1 << r
		}
	}
	out := []map[string]int{}
	for shape, resources := range byShape {
		m := map[string]int{"resources_bits": resources, "facade_bits": shape[1]}
		if !discovery {
			m["managed_bits"] = shape[0]
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a["resources_bits"] != b["resources_bits"] {
			return a["resources_bits"] < b["resources_bits"]
		}
		if a["managed_bits"] != b["managed_bits"] {
			return a["managed_bits"] < b["managed_bits"]
		}
		return a["facade_bits"] < b["facade_bits"]
	})
	return out
}

func sourceConfig(s *Server, r, mask, f int) {
	s.clientResources = r == 1
	s.facade = []facadeMode{facadeThree, facadeTwo, facadeBoth}[f]
	s.trusted = TrustedContext{}
	if mask&1 != 0 {
		s.trusted.SpaceID = "bound-space"
	}
	if mask&2 != 0 {
		s.trusted.ChannelID = "bound-channel"
	}
	if mask&4 != 0 {
		s.trusted.ChannelType = "2"
	}
}

// MaterializePermissionPackage emits every approved identity/content/configuration.
// Packaging success is separate from source-bound/support and native acceptance.
func MaterializePermissionPackage(ctx context.Context) (*CatalogPackageProduction, error) { //nolint:gocyclo // finite source traversal preserves every independent configuration observation
	p := &packageProducer{content: map[string]map[string]any{}, raw: map[string][]byte{}, rows: map[string]map[string]any{}, refs: map[string]map[string]bool{}, bindings: []map[string]any{}, checks: map[string]int{}, post: map[string]sourceJSONWork{}}
	if err := json.Unmarshal(catalogSourceIndex, &p.source); err != nil {
		return nil, err
	}
	var input struct {
		Profiles []map[string]any `json:"profiles"`
		Sources  [][]string       `json:"source_files"`
	}
	if err := json.Unmarshal(releaseSourceInputs, &input); err != nil {
		return nil, err
	}
	p.profiles = input.Profiles
	p.sources = input.Sources
	s, err := NewServer(nil)
	if err != nil {
		return nil, err
	}
	for i := range p.source.Entries {
		entry := &p.source.Entries[i]
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if entry.Kind != "schema" {
			continue
		}
		var target []string
		if json.Unmarshal(entry.Target, &target) != nil || len(target) != 2 {
			return nil, fmt.Errorf("invalid operation identity")
		}
		op := target[1]
		detail, ok := s.reg.GetOperation(op)
		if !ok {
			return nil, fmt.Errorf("missing source operation")
		}
		truth := catalogCanonicalJSON(detail)
		truthHash := p.root(truth)
		p.put(map[string]any{"kind": "bytes", "sha256": truthHash, "length": len(truth), "media_type": "application/json", "data_ref": truthHash})
		for _, facade := range []string{"describe_op", "get_skill_full", "get_skill_summary"} {
			groups := map[string][]producerCell{}
			for r := 0; r < 2; r++ {
				for mask := 0; mask < 8; mask++ {
					for f := 0; f < 3; f++ {
						sourceConfig(s, r, mask, f)
						var result toolResult
						switch facade {
						case "describe_op":
							result = s.describeOp(op)
						case "get_skill_full":
							result = s.describeSkill(op, "full")
						default:
							result = s.describeSkill(op, "summary")
						}
						template, err := templateFor([]byte(result.Content[0].Text))
						if err != nil {
							return nil, err
						}
						rendered, err := renderTemplate(&template, s, op)
						if err != nil || !bytes.Equal(rendered, []byte(result.Content[0].Text)) {
							return nil, fmt.Errorf("source schema rendering mismatch %s", op)
						}
						hash, err := p.template([]byte(result.Content[0].Text))
						if err != nil {
							return nil, err
						}
						groups[hash] = append(groups[hash], producerCell{r, mask, f})
						p.checks["schema_cells"]++
					}
				}
			}
			for template, cells := range groups {
				for _, selector := range producerSelectors(cells, false) {
					hash := p.put(map[string]any{"kind": "schema_variant", "operation_id": op, "facade": facade, "template_hash": template, "parameter_truth_hash": truthHash, "selector": selector})
					p.bind(entry.ID, hash)
					p.bind("octo-cli/"+detail.Service+"/op/"+op, hash)
				}
			}
		}
	}
	for _, service := range append([]string{""}, s.reg.EnabledServices()...) {
		groups := map[string][]producerCell{}
		descriptors := map[string]map[string]any{}
		for r := 0; r < 2; r++ {
			for f := 0; f < 3; f++ {
				var expected []byte
				for mask := 0; mask < 8; mask++ {
					sourceConfig(s, r, mask, f)
					rows := []string{}
					ops := s.reg.ListOperations(service)
					if service == "" {
						ops = s.reg.EnabledOperations()
					}
					for _, op := range ops {
						if !mcpExposed(op.ID) {
							continue
						}
						if _, bad := divergentMCPOps[op.ID]; bad {
							continue
						}
						raw := catalogJSON(opSearchResult{OperationInfo: op, Skill: s.navFor(op, false)})
						row := map[string]any{"operation_id": op.ID, "match_text": strings.ToLower(op.ID + " " + op.Summary + " " + op.Path), "row_ref": p.root(raw)}
						hash := sha256Sum(catalogCanonicalJSON(row))
						row["sha256"] = hash
						p.rows[hash] = row
						rows = append(rows, hash)
					}
					scope := map[string]any{"kind": "named", "service": service}
					if service == "" {
						scope = map[string]any{"kind": "global"}
					}
					base := map[string]any{"kind": "discovery_index", "media_type": "application/json", "renderer": "discovery-search-render-v1", "rows": rows, "scope": scope, "page_limit": searchOpsPageLimit, "query_limits": map[string]int{"bytes": maxSearchQueryBytes, "runes": maxSearchQueryRunes, "terms": maxSearchTerms}, "notes": map[string]string{"query_bounded_format": "query bounded to %d bytes / %d runes / %d terms; ", "more_matches_format": "more than %d matches; narrow with a more specific query or a domain filter"}}
					encoded := catalogCanonicalJSON(base)
					if mask == 0 {
						expected = encoded
					} else if !bytes.Equal(expected, encoded) {
						return nil, fmt.Errorf("discovery managed-mask invariance failed")
					}
					p.checks["discovery_source_observations"] += 2
					if mask == 0 {
						key := sha256Sum(encoded)
						descriptors[key] = base
						groups[key] = append(groups[key], producerCell{r, 0, f})
					}
				}
			}
		}
		entry := "octo-cli/" + service + "/discovery/search"
		if service == "" {
			entry = "octo-cli/discovery/search-all"
		}
		for key, cells := range groups {
			for _, selector := range producerSelectors(cells, true) {
				for _, facade := range []string{"search_ops", "get_skill_search"} {
					m := map[string]any{}
					for k, v := range descriptors[key] {
						m[k] = v
					}
					m["facade"] = facade
					m["selector"] = selector
					p.bind(entry, p.put(m))
				}
			}
		}
		p.checks["discovery_cells"] += 12
	}
	for i := range p.source.Entries {
		entry := &p.source.Entries[i]
		if entry.Kind != "resource" && entry.Kind != "inventory" && entry.ID != "octo-cli/discovery/module-map" {
			continue
		}
		groups := map[string][]producerCell{}
		for r := 0; r < 2; r++ {
			for mask := 0; mask < 8; mask++ {
				for f := 0; f < 3; f++ {
					sourceConfig(s, r, mask, f)
					var hash string
					if entry.Kind == "resource" {
						var uri string
						if json.Unmarshal(entry.Target, &uri) != nil {
							return nil, fmt.Errorf("invalid resource")
						}
						resource, err := s.readResource(uri)
						if err != nil {
							return nil, err
						}
						raw := []byte(resource["contents"].([]resourceContent)[0].Text)
						root := p.root(raw)
						hash = p.put(map[string]any{"kind": "bytes", "sha256": root, "length": len(raw), "media_type": "text/markdown", "data_ref": root})
					} else {
						var raw []byte
						if entry.Kind == "inventory" {
							raw = catalogJSON(s.listResources())
						} else {
							raw = catalogJSON(s.moduleMap())
						}
						hash, err = p.template(raw)
						if err != nil {
							return nil, err
						}
					}
					p.bind(entry.ID, hash)
					groups[hash] = append(groups[hash], producerCell{r, mask, f})
					p.checks["static_cells"]++
				}
			}
		}
		for hash, cells := range groups {
			for _, selector := range producerSelectors(cells, false) {
				p.bindings = append(p.bindings, map[string]any{"entry_id": entry.ID, "selector": selector, "content_ref": hash})
			}
		}
	}
	reader := tar.NewReader(bytes.NewReader(catalogPinnedSourceFiles))
	sourceBytes := map[string][]byte{}
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil || header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("invalid pinned source carrier")
		}
		raw, e := io.ReadAll(io.LimitReader(reader, 1048577))
		if e != nil || len(raw) > 1048576 {
			return nil, fmt.Errorf("invalid pinned source bytes")
		}
		sourceBytes[header.Name] = raw
	}
	for _, source := range p.sources {
		raw, ok := sourceBytes[source[0]]
		if !ok || sha256Sum(raw) != source[1] {
			return nil, fmt.Errorf("pinned source mismatch %s", source[0])
		}
		// A source tuple can name the exact bytes already used by a resource.
		// It adds a _global reachability root, not a conflicting second media
		// descriptor with the same raw-byte identity.
		if existing, ok := p.content[source[1]]; ok {
			if existing["kind"] != "bytes" {
				return nil, fmt.Errorf("source/content identity collision")
			}
			continue
		}
		p.put(map[string]any{"kind": "bytes", "sha256": source[1], "length": len(raw), "media_type": "application/octet-stream", "data_ref": p.root(raw)})
	}
	return p.assemble(ctx)
}
