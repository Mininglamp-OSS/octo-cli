package mcp

import (
	"encoding/json"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

type sourceJSONWork struct{ Bytes, Nodes, Depth int }

// This producer ledger uses actual source outputs, independently of the
// consumer's slot renderer, graph counters and occurrence implementation.
func sourceJSONStats(raw []byte) sourceJSONWork {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		panic("source JSON is invalid")
	}
	var visit func(any, int) (int, int)
	visit = func(value any, depth int) (int, int) {
		nodes, maximum := 1, depth
		switch v := value.(type) {
		case map[string]any:
			for _, child := range v {
				n, d := visit(child, depth+1)
				nodes += n
				maximum = max(maximum, d)
			}
		case []any:
			for _, child := range v {
				n, d := visit(child, depth+1)
				nodes += n
				maximum = max(maximum, d)
			}
		}
		return nodes, maximum
	}
	nodes, depth := visit(value, 1)
	return sourceJSONWork{len(raw), nodes, depth}
}

func (p *packageProducer) measure(files map[string][]byte, roots []map[string]any) permission.CatalogLedger { //nolint:gocyclo // independent occurrence equations retain each content-kind charge
	out := permission.CatalogLedger{Files: len(files), Roots: len(p.raw), Rows: len(p.rows), Content: len(p.content), SchemaCells: p.checks["schema_cells"], DiscoveryCells: p.checks["discovery_cells"], StaticCells: p.checks["static_cells"]}
	for path, raw := range files {
		out.StoredBytes += len(raw)
		if strings.HasSuffix(path, ".json") {
			work := sourceJSONStats(raw)
			out.MetadataNodes += work.Nodes
			out.MaxDepth = max(out.MaxDepth, work.Depth)
		}
	}
	for _, root := range roots {
		parts := len(root["parts"].([]map[string]any))
		out.Parts += parts
		out.MaxParts = max(out.MaxParts, parts)
	}
	occurrences := map[string]int{}
	jsonRoots := map[string]bool{}
	for _, record := range p.content {
		switch record["kind"] {
		case "bytes":
			root := record["data_ref"].(string)
			occurrences[root]++
			if record["media_type"] == "application/json" {
				jsonRoots[root] = true
			}
		case "template":
			root := record["value_ref"].(string)
			occurrences[root]++
			jsonRoots[root] = true
		case "discovery_index":
			for _, ref := range record["rows"].([]string) {
				root := p.rows[ref]["row_ref"].(string)
				occurrences[root]++
				jsonRoots[root] = true
			}
		}
	}
	stats := map[string]sourceJSONWork{}
	out.PreNodes = out.MetadataNodes
	out.PostNodes = out.MetadataNodes
	for hash, raw := range p.raw {
		work := sourceJSONWork{Bytes: len(raw)}
		if jsonRoots[hash] {
			work = sourceJSONStats(raw)
			out.MaxDepth = max(out.MaxDepth, work.Depth)
		}
		stats[hash] = work
		weight := max(1, occurrences[hash])
		out.PreBytes += weight * len(raw)
		out.PreNodes += weight * work.Nodes
		if occurrences[hash] == 0 {
			out.PostBytes += len(raw)
			out.PostNodes += work.Nodes
		}
	}
	for hash, record := range p.content {
		switch record["kind"] {
		case "bytes":
			root := record["data_ref"].(string)
			out.PostBytes += stats[root].Bytes
			out.PostNodes += stats[root].Nodes
		case "template":
			out.PostBytes += p.post[hash].Bytes
			out.PostNodes += p.post[hash].Nodes
			out.MaxDepth = max(out.MaxDepth, p.post[hash].Depth)
		case "discovery_index":
			for _, ref := range record["rows"].([]string) {
				root := p.rows[ref]["row_ref"].(string)
				out.PostBytes += stats[root].Bytes
				out.PostNodes += stats[root].Nodes
			}
		}
	}
	return out
}
