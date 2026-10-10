package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// The registry currently declares at most 12 flat operation inputs. Keep a
// generous top-level ceiling, matching the tool input schemas.
const maxMCPObjectKeys = 512

// validateObjectKeys rejects duplicate or unknown keys at a safety-bearing
// JSON object boundary before Go's last-key-wins unmarshal can select one.
func validateObjectKeys(raw json.RawMessage, allowed ...string) error {
	return validateObjectKeysPolicy(raw, false, allowed...)
}

func validateObjectKeysPolicy(raw json.RawMessage, allowNull bool, allowed ...string) error { //nolint:gocyclo // each check validates an independent object boundary
	if len(raw) == 0 {
		return nil
	}
	if allowNull && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	if !isJSONObject(raw) {
		return fmt.Errorf("arguments must be a JSON object")
	}
	allow := map[string]bool{}
	for _, name := range allowed {
		allow[name] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("expected an argument name")
		}
		if len(allowed) > 0 && !allow[key] {
			return fmt.Errorf("unknown argument %q", truncateRunes(key, 200))
		}
		if seen[key] {
			return fmt.Errorf("duplicate argument %q", truncateRunes(key, 200))
		}
		if len(seen) >= maxMCPObjectKeys {
			return fmt.Errorf("too many object keys (maximum %d)", maxMCPObjectKeys)
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if !allowNull && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("argument %q must not be null", key)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}
