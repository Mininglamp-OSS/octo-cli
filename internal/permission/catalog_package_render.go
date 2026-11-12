package permission

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type catalogSpan struct {
	start, end    int
	marker        string
	reserved      bool
	invalidMarker bool
	ambiguous     bool
}

func catalogValueSpans(raw []byte) (map[string]catalogSpan, error) { //nolint:gocyclo // bounded JSON token spans preserve original bytes and pointer positions
	if _, _, err := catalogJSONValue(raw, false); err != nil {
		return nil, err
	}
	spans := map[string]catalogSpan{}
	at := 0
	skip := func() {
		for at < len(raw) && strings.ContainsRune(" \t\r\n", rune(raw[at])) {
			at++
		}
	}
	stringEnd := func(start int) int {
		p := start + 1
		for p < len(raw) {
			if raw[p] == '\\' {
				p += 2
				continue
			}
			if raw[p] == '"' {
				return p + 1
			}
			p++
		}
		return len(raw)
	}
	var walk func(string)
	var scanErr error
	walk = func(path string) {
		if scanErr != nil {
			return
		}
		skip()
		start := at
		switch raw[at] {
		case '{':
			at++
			skip()
			for raw[at] != '}' {
				end := stringEnd(at)
				var key string
				if err := json.Unmarshal(raw[at:end], &key); err != nil {
					scanErr = err
					return
				}
				at = end
				skip()
				at++
				walk(path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
				if scanErr != nil {
					return
				}
				skip()
				if raw[at] == ',' {
					at++
					skip()
				} else {
					break
				}
			}
			at++
		case '[':
			at++
			skip()
			index := 0
			for raw[at] != ']' {
				walk(path + "/" + strconv.Itoa(index))
				if scanErr != nil {
					return
				}
				index++
				skip()
				if raw[at] == ',' {
					at++
					skip()
				} else {
					break
				}
			}
			at++
		case '"':
			at = stringEnd(at)
		default:
			for at < len(raw) && !strings.ContainsRune(",]} \n\r\t", rune(raw[at])) {
				at++
			}
		}
		span := catalogSpan{start: start, end: at}
		if raw[start] == '{' {
			var marker map[string]json.RawMessage
			if json.Unmarshal(raw[start:at], &marker) == nil {
				if value, ok := marker["$octo_slot"]; ok {
					span.reserved = true
					if len(marker) != 1 || json.Unmarshal(value, &span.marker) != nil || span.marker == "" {
						span.invalidMarker = true
					}
				}
			}
		}
		if prior, ok := spans[path]; ok {
			span.ambiguous = true
			span.reserved = span.reserved || prior.reserved
			span.invalidMarker = span.invalidMarker || prior.invalidMarker || span.reserved
		}
		if span.reserved && spans[path+"/$octo_slot"].ambiguous {
			span.invalidMarker = true
		}
		spans[path] = span
	}
	walk("")
	if scanErr != nil {
		return nil, scanErr
	}
	return spans, nil
}

func catalogValidateMarkers(raw []byte, slots []CatalogSlot) error {
	spans, err := catalogValueSpans(raw)
	if err != nil {
		return err
	}
	ids, pointers := map[string]bool{}, map[string]bool{}
	markers := 0
	for _, span := range spans {
		if span.invalidMarker {
			return catalogInvalid("reserved template marker key")
		}
		if span.reserved {
			markers++
		}
	}
	for _, slot := range slots {
		span, ok := spans[slot.Pointer]
		if !ok || span.marker != slot.ID || ids[slot.ID] || pointers[slot.Pointer] || !catalogPointerUnique(spans, slot.Pointer) {
			return catalogInvalid("slot marker/pointer")
		}
		ids[slot.ID] = true
		pointers[slot.Pointer] = true
	}
	if markers != len(slots) {
		return catalogInvalid("undeclared marker")
	}
	return nil
}

func catalogPointerUnique(spans map[string]catalogSpan, path string) bool {
	for {
		if span, ok := spans[path]; !ok || span.ambiguous {
			return false
		}
		if path == "" {
			return true
		}
		path = path[:strings.LastIndex(path, "/")]
	}
}

func catalogRenderTemplate(raw []byte, slots []CatalogSlot, values map[string]any) ([]byte, error) {
	if len(values) != len(slots) {
		return nil, catalogInvalid("slot assignment count")
	}
	spans, err := catalogValueSpans(raw)
	if err != nil {
		return nil, err
	}
	type edit struct {
		span        catalogSpan
		replacement []byte
	}
	edits := []edit{}
	for _, slot := range slots {
		value, ok := values[slot.ID]
		span, found := spans[slot.Pointer]
		if !ok || !found || span.marker != slot.ID || !catalogPointerUnique(spans, slot.Pointer) || !catalogSlotInput(slot.Domain, value) {
			return nil, catalogInvalid("slot assignment/pointer")
		}
		replacement, err := json.Marshal(value)
		if err != nil || len(replacement) > slot.MaxLength || !catalogSlotDomain(slot.Domain, replacement) {
			return nil, catalogInvalid("slot value bound")
		}
		edits = append(edits, edit{span, replacement})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].span.start > edits[j].span.start })
	out := append([]byte(nil), raw...)
	for _, e := range edits {
		next := make([]byte, 0, len(out)-e.span.end+e.span.start+len(e.replacement))
		next = append(next, out[:e.span.start]...)
		next = append(next, e.replacement...)
		next = append(next, out[e.span.end:]...)
		out = next
	}
	return json.MarshalIndent(json.RawMessage(out), "", "  ")
}

func catalogSlotInput(domain string, value any) bool {
	if domain == "facade_hint" {
		s, ok := value.(string)
		return ok && utf8.ValidString(s)
	}
	if domain != "server_managed_argument_names" {
		return false
	}
	switch names := value.(type) {
	case []string:
		for _, name := range names {
			if !utf8.ValidString(name) {
				return false
			}
		}
	case []any:
		for _, value := range names {
			if s, ok := value.(string); !ok || !utf8.ValidString(s) {
				return false
			}
		}
	default:
		return false
	}
	return true
}

func catalogSlotDomain(domain string, raw []byte) bool {
	if !pairedEscapes(raw) {
		return false
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	if domain == "facade_hint" {
		_, ok := value.(string)
		return ok
	}
	if domain != "server_managed_argument_names" {
		return false
	}
	return catalogArgumentNames(value)
}

func catalogArgumentNames(value any) bool {
	names, ok := value.([]any)
	if !ok || len(names) == 0 {
		return false
	}
	previous := ""
	for _, item := range names {
		name, ok := item.(string)
		if !ok || name <= previous || len(name) > 128 {
			return false
		}
		for i, c := range name {
			if i == 0 && !catalogArgumentStart(c) || i > 0 && !catalogArgumentRest(c) {
				return false
			}
		}
		previous = name
	}
	return true
}

func catalogArgumentStart(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}
func catalogArgumentRest(c rune) bool {
	return catalogArgumentStart(c) || c >= '0' && c <= '9' || c == '.' || c == '-'
}

// Schema renders the exact selected, preverified descriptor using private source values.
func (v *VerifiedCatalog) Schema(operation, facade string, c CatalogConfig) ([]byte, error) {
	if !catalogConfigValid(c) {
		return nil, ErrInvalid
	}
	hash, ok := v.schemas[catalogCell(operation+"|"+facade, c)]
	if !ok {
		return nil, ErrUnavailable
	}
	m := v.content[hash]
	return v.render(text(m["template_hash"]), operation, c)
}

// Static selects every resource, inventory and module-map configuration explicitly.
func (v *VerifiedCatalog) Static(entry string, c CatalogConfig) ([]byte, error) {
	if !catalogConfigValid(c) {
		return nil, ErrInvalid
	}
	hash, ok := v.static[catalogCell(entry, c)]
	if !ok {
		return nil, ErrUnavailable
	}
	m := v.content[hash]
	if m["kind"] == "bytes" {
		return append([]byte(nil), v.raw[text(m["data_ref"])]...), nil
	}
	return v.render(hash, "", c)
}

func (v *VerifiedCatalog) render(hash, operation string, c CatalogConfig) ([]byte, error) {
	m := v.content[hash]
	raw := v.raw[text(m["value_ref"])]
	var slots []CatalogSlot
	if catalogDecodeInto(m["slots"], &slots) != nil {
		return nil, ErrInvalid
	}
	values := map[string]any{}
	var err error
	if len(slots) > 0 {
		frozen, ok := v.frozenSlots[catalogSlotCell(hash, operation, c)]
		if !ok || json.Unmarshal(frozen, &values) != nil {
			return nil, ErrUnavailable
		}
	}
	out, err := catalogRenderTemplate(raw, slots, values)
	if err != nil || len(out) > v.renderedMax[hash] {
		return nil, catalogInvalid("rendered template bound")
	}
	return out, nil
}
func catalogConfigValid(c CatalogConfig) bool {
	return c.ManagedMask >= 0 && c.ManagedMask < 8 && c.Facade >= 0 && c.Facade < 3
}

// StaticMedia returns the selected immutable descriptor's media contract.
func (v *VerifiedCatalog) StaticMedia(entry string, c CatalogConfig) (string, error) {
	if !catalogConfigValid(c) {
		return "", ErrInvalid
	}
	hash, ok := v.static[catalogCell(entry, c)]
	if !ok {
		return "", ErrUnavailable
	}
	return text(v.content[hash]["media_type"]), nil
}

// CatalogDiscoveryContract retains every source matcher/output parameter.
type CatalogDiscoveryContract struct {
	PageLimit, QueryBytes, QueryRunes, QueryTerms int
	QueryNote, MoreNote                           string
}

// DiscoveryContract returns detached scalar metadata for source conformance.
func (v *VerifiedCatalog) DiscoveryContract(service, facade string, c CatalogConfig) (CatalogDiscoveryContract, error) {
	if !catalogConfigValid(c) {
		return CatalogDiscoveryContract{}, ErrInvalid
	}
	c.ManagedMask = 0
	hash, ok := v.discovery[catalogCell(service+"|"+facade, c)]
	if !ok {
		return CatalogDiscoveryContract{}, ErrUnavailable
	}
	m := v.content[hash]
	limits, notes := object(m["query_limits"]), object(m["notes"])
	return CatalogDiscoveryContract{PageLimit: catalogNumber(m["page_limit"]), QueryBytes: catalogNumber(limits["bytes"]), QueryRunes: catalogNumber(limits["runes"]), QueryTerms: catalogNumber(limits["terms"]), QueryNote: text(notes["query_bounded_format"]), MoreNote: text(notes["more_matches_format"])}, nil
}

// Discovery returns ordered source rows for the unchanged bounded matcher.
func (v *VerifiedCatalog) Discovery(service, facade string, c CatalogConfig) (rows [][]byte, matches []string, err error) {
	if !catalogConfigValid(c) {
		return nil, nil, ErrInvalid
	}
	c.ManagedMask = 0
	hash, ok := v.discovery[catalogCell(service+"|"+facade, c)]
	if !ok {
		return nil, nil, fmt.Errorf("catalog discovery selection: %w", ErrUnavailable)
	}
	rows, matches = [][]byte{}, []string{}
	for _, item := range catalogArray(v.content[hash]["rows"]) {
		row := v.rows[text(item)]
		rows = append(rows, append([]byte(nil), v.raw[row.Root]...))
		matches = append(matches, row.Match)
	}
	return rows, matches, nil
}
