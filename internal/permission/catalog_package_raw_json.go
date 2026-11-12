package permission

import (
	"bytes"
	"encoding/json"
	"io"
)

// catalogRawJSONWork uses the declared Go JSON syntax domain. Unlike metadata,
// raw payloads may retain repeated members, noncanonical numbers and the Go
// renderer's string byte domain. Count each occurrence without building a map.
func catalogRawJSONWork(raw []byte, limit int) (CatalogJSONWork, error) { //nolint:gocyclo // one token traversal validates syntax and counts every duplicate raw value without metadata coercion
	if len(raw) > limit {
		return CatalogJSONWork{}, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	work := CatalogJSONWork{}
	var consume func(int) error
	consume = func(depth int) error {
		if depth > 32 || work.Nodes >= 1048576 {
			return ErrInvalid
		}
		work.Nodes++
		work.Depth = max(work.Depth, depth)
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		if delim != '{' && delim != '[' {
			return ErrInvalid
		}
		for d.More() {
			if delim == '{' {
				key, err := d.Token()
				if _, ok := key.(string); err != nil || !ok {
					return ErrInvalid
				}
			}
			if err := consume(depth + 1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
			return ErrInvalid
		}
		return nil
	}
	if err := consume(1); err != nil {
		return work, err
	}
	if _, err := d.Token(); err != io.EOF {
		return work, ErrInvalid
	}
	return work, nil
}
