package mcp

import (
	"bytes"
	"fmt"
	"sort"
)

// The offline producer greedily reuses exact ranges already present in its raw
// pool. The loader sees only approved one-level Part records, no dictionary or
// compression codec. Window/stride choices are producer heuristics, not limits.
func packageSlicePools(raw map[string][]byte, window int) (files map[string][]byte, roots []map[string]any, err error) { //nolint:gocyclo // exact slice matching and pool-boundary splitting are independent checked steps
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if len(raw[a]) != len(raw[b]) {
			return len(raw[a]) > len(raw[b])
		}
		return a < b
	})
	pool := []byte{}
	dictionary := map[uint64][]int{}
	indexed := 0
	hash := func(b []byte) uint64 {
		var h uint64 = 1469598103934665603
		for _, c := range b {
			h = (h ^ uint64(c)) * 1099511628211
		}
		return h
	}
	index := func() {
		for indexed+window <= len(pool) {
			h := hash(pool[indexed : indexed+window])
			positions := dictionary[h]
			if len(positions) < 4 {
				dictionary[h] = append(positions, indexed)
			}
			indexed += 16
		}
	}
	type segment struct{ offset, length int }
	recipes := map[string][]segment{}
	for _, key := range keys {
		data := raw[key]
		parts := []segment{}
		appendPart := func(offset, length int) {
			if length == 0 {
				return
			}
			if len(parts) > 0 && parts[len(parts)-1].offset+parts[len(parts)-1].length == offset {
				parts[len(parts)-1].length += length
			} else {
				parts = append(parts, segment{offset, length})
			}
		}
		literal := 0
		at := 0
		for at+window <= len(data) {
			bestAt, bestLength := 0, 0
			for _, position := range dictionary[hash(data[at:at+window])] {
				if !bytes.Equal(data[at:at+window], pool[position:position+window]) {
					continue
				}
				length := window
				for at+length < len(data) && position+length < len(pool) && data[at+length] == pool[position+length] {
					length++
				}
				if length > bestLength {
					bestAt, bestLength = position, length
				}
			}
			if bestLength == 0 {
				at++
				continue
			}
			if at > literal {
				offset := len(pool)
				pool = append(pool, data[literal:at]...)
				appendPart(offset, at-literal)
				index()
			}
			appendPart(bestAt, bestLength)
			at += bestLength
			literal = at
		}
		if literal < len(data) {
			offset := len(pool)
			pool = append(pool, data[literal:]...)
			appendPart(offset, len(data)-literal)
			index()
		}
		recipes[key] = parts
	}
	if len(pool) > 8*1048576 {
		return nil, nil, fmt.Errorf("v2 slice pools exceed closed blob domain")
	}
	files = map[string][]byte{}
	for offset, n := 0, 0; offset < len(pool); n++ {
		end := min(len(pool), offset+1048576)
		files[fmt.Sprintf("blobs/%d.bin", n)] = append([]byte(nil), pool[offset:end]...)
		offset = end
	}
	sort.Strings(keys)
	roots = []map[string]any{}
	for _, key := range keys {
		parts := []map[string]any{}
		for _, segment := range recipes[key] {
			offset, length := segment.offset, segment.length
			for length > 0 {
				file := offset / 1048576
				inside := offset % 1048576
				n := min(length, 1048576-inside)
				parts = append(parts, map[string]any{"path": fmt.Sprintf("blobs/%d.bin", file), "offset": inside, "length": n})
				offset += n
				length -= n
			}
		}
		if len(parts) > 256 {
			return nil, nil, fmt.Errorf("v2 root parts exceeded: %d", len(parts))
		}
		roots = append(roots, map[string]any{"sha256": key, "length": len(raw[key]), "parts": parts})
	}
	return files, roots, nil
}
