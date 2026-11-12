package mcp

import (
	"fmt"
	"sort"
)

// Whole-resource sharing is measured first. Only a package that does not fit
// proceeds to offline slice factoring; no pool encoding is a runtime codec.
func packageRawPools(raw map[string][]byte) (files map[string][]byte, roots []map[string]any, err error) {
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	files = map[string][]byte{}
	roots = []map[string]any{}
	pool := []byte{}
	number := 0
	for _, hash := range keys {
		data := raw[hash]
		length := len(data)
		parts := []map[string]any{}
		for len(data) > 0 {
			if len(pool) == 1048576 {
				files[fmt.Sprintf("blobs/%d.bin", number)] = pool
				pool = []byte{}
				number++
			}
			if number > 7 {
				return nil, nil, fmt.Errorf("whole resources exceed blob inventory")
			}
			n := min(len(data), 1048576-len(pool))
			parts = append(parts, map[string]any{"path": fmt.Sprintf("blobs/%d.bin", number), "offset": len(pool), "length": n})
			pool = append(pool, data[:n]...)
			data = data[n:]
		}
		roots = append(roots, map[string]any{"sha256": hash, "length": length, "parts": parts})
	}
	if len(pool) > 0 {
		files[fmt.Sprintf("blobs/%d.bin", number)] = pool
	}
	return files, roots, nil
}
