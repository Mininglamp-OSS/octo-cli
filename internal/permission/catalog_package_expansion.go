package permission

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

func (v *VerifiedCatalog) verifyExpansion(ctx context.Context) error { //nolint:gocyclo // separate stored/pre/post bounds count every semantic payload occurrence
	multiplicity := map[string]int{}
	isJSON := map[string]bool{}
	for _, m := range v.content {
		switch m["kind"] {
		case "bytes":
			root := text(m["data_ref"])
			multiplicity[root]++
			if m["media_type"] == "application/json" {
				isJSON[root] = true
			}
		case "template":
			root := text(m["value_ref"])
			multiplicity[root]++
			isJSON[root] = true
		case "discovery_index":
			for _, item := range catalogArray(m["rows"]) {
				root := v.rows[text(item)].Root
				multiplicity[root]++
				isJSON[root] = true
			}
		}
	}
	for hash, r := range v.roots {
		weight := max(1, multiplicity[hash])
		if r.Length > 0 && weight > (16777216-v.ledger.PreBytes)/r.Length {
			return catalogInvalid("pre-slot bytes")
		}
		v.ledger.PreBytes += weight * r.Length
		v.ledger.Parts += len(r.Parts)
		v.ledger.MaxParts = max(v.ledger.MaxParts, len(r.Parts))
		if v.ledger.Parts > 65536 {
			return catalogInvalid("parts total")
		}
	}
	coverage := map[string][][2]int{}
	work := map[string]CatalogJSONWork{}
	for hash, r := range v.roots {
		if err := ctx.Err(); err != nil {
			return err
		}
		sum := 0
		if (r.Length == 0) != (len(r.Parts) == 0) {
			return catalogInvalid("empty root recipe")
		}
		for _, p := range r.Parts {
			blob, ok := v.files[p.Path]
			if !ok || !strings.HasPrefix(p.Path, "blobs/") || p.Offset < 0 || p.Length < 1 || p.Offset > len(blob) || p.Length > len(blob)-p.Offset || p.Length > r.Length-sum {
				return catalogInvalid("part type/range/overflow")
			}
			sum += p.Length
			coverage[p.Path] = append(coverage[p.Path], [2]int{p.Offset, p.Offset + p.Length})
		}
		if sum != r.Length {
			return catalogInvalid("expanded root length")
		}
		raw := make([]byte, 0, r.Length)
		for _, p := range r.Parts {
			raw = append(raw, v.files[p.Path][p.Offset:p.Offset+p.Length]...)
		}
		if catalogHash(raw) != hash {
			return catalogInvalid("root raw hash")
		}
		v.raw[hash] = raw
		if isJSON[hash] {
			value, w, err := catalogJSONValue(raw, false)
			if err != nil {
				return catalogInvalid("declared JSON root")
			}
			_ = value
			work[hash] = w
			v.ledger.MaxDepth = max(v.ledger.MaxDepth, w.Depth)
		}
	}
	for path, raw := range v.files {
		if !strings.HasPrefix(path, "blobs/") {
			continue
		}
		ranges := coverage[path]
		sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
		end := 0
		for _, r := range ranges {
			if r[0] > end {
				return catalogInvalid("hidden blob padding")
			}
			end = max(end, r[1])
		}
		if end != len(raw) {
			return catalogInvalid("unreferenced blob bytes")
		}
	}
	v.ledger.PreNodes = v.ledger.MetadataNodes
	for hash, r := range v.roots {
		weight := max(1, multiplicity[hash])
		nodes := work[hash].Nodes
		if nodes > 0 && weight > (1048576-v.ledger.PreNodes)/nodes {
			return catalogInvalid("pre-slot nodes")
		}
		v.ledger.PreNodes += weight * nodes
		if multiplicity[hash] == 0 {
			v.ledger.PostBytes += r.Length
			v.ledger.PostNodes += nodes
		}
	}
	v.ledger.PostNodes += v.ledger.MetadataNodes
	v.renderedMax = map[string]int{}
	charge := func(b, n int) error {
		if b > 16777216-v.ledger.PostBytes || n > 1048576-v.ledger.PostNodes {
			return catalogInvalid("post-slot bytes/nodes")
		}
		v.ledger.PostBytes += b
		v.ledger.PostNodes += n
		return nil
	}
	for hash, m := range v.content {
		switch m["kind"] {
		case "bytes":
			root := text(m["data_ref"])
			if err := charge(len(v.raw[root]), work[root].Nodes); err != nil {
				return err
			}
		case "discovery_index":
			for _, item := range catalogArray(m["rows"]) {
				root := v.rows[text(item)].Root
				if err := charge(len(v.raw[root]), work[root].Nodes); err != nil {
					return err
				}
			}
		case "template":
			root := text(m["value_ref"])
			var slots []CatalogSlot
			if catalogDecodeInto(m["slots"], &slots) != nil {
				return ErrInvalid
			}
			if err := catalogValidateMarkers(v.raw[root], slots); err != nil {
				return err
			}
			uses := v.uses[hash]
			if len(uses) == 0 {
				return catalogInvalid("template has no finite source assignments")
			}
			maximumBytes, maximumNodes := 0, 0
			assignments := map[string]bool{}
			for _, use := range uses {
				if err := ctx.Err(); err != nil {
					return err
				}
				values := map[string]any{}
				var err error
				if len(slots) > 0 {
					if v.slots == nil {
						return catalogInvalid("joint slot source unavailable")
					}
					values, err = v.slots(use.Operation, use.Config, append([]byte(nil), v.raw[root]...), append([]CatalogSlot(nil), slots...))
					if err != nil {
						return err
					}
				}
				assignment := string(catalogEncode(values))
				if assignments[assignment] {
					continue
				}
				assignments[assignment] = true
				rendered, err := catalogRenderTemplate(v.raw[root], slots, values)
				if err != nil {
					return err
				}
				w, err := catalogRawJSONWork(rendered, 16777216)
				if err != nil {
					return err
				}
				maximumBytes = max(maximumBytes, len(rendered))
				maximumNodes = max(maximumNodes, w.Nodes)
				v.ledger.MaxDepth = max(v.ledger.MaxDepth, w.Depth)
			}
			v.renderedMax[hash] = maximumBytes
			if err := charge(maximumBytes, maximumNodes); err != nil {
				return err
			}
		}
	}
	for _, row := range v.rows {
		var payload map[string]json.RawMessage
		if json.Unmarshal(v.raw[row.Root], &payload) != nil {
			return catalogInvalid("row JSON object")
		}
		var id, summary, path string
		if json.Unmarshal(payload["id"], &id) != nil || json.Unmarshal(payload["path"], &path) != nil {
			return catalogInvalid("row source identity")
		}
		if raw, ok := payload["summary"]; ok && json.Unmarshal(raw, &summary) != nil {
			return ErrInvalid
		}
		if id != row.Operation || row.Match != strings.ToLower(id+" "+summary+" "+path) { //nolint:gocritic // stored match text must equal exact lowercase bytes, not case-fold equivalence
			return catalogInvalid("row matching truth")
		}
	}
	return nil
}
