package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// Bound materialized JSON values, including array members, before unmarshalling
// untrusted tool parameters. The separate 512-property limit applies only to
// the top-level arguments object, as advertised by the tool input schemas.
const maxMCPJSONDepth = 64
const maxMCPJSONValues = 65536

func validateJSONValueBudget(raw []byte, maxValues int) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	values := 0
	if err := walkJSONValue(dec, 0, &values, maxValues); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("trailing JSON content")
	}
	return nil
}

func walkJSONValue(dec *json.Decoder, depth int, values *int, maxValues int) error {
	if depth > maxMCPJSONDepth {
		return fmt.Errorf("JSON nesting exceeds maximum depth %d", maxMCPJSONDepth)
	}
	*values++
	if *values > maxValues {
		return fmt.Errorf("too many JSON values (maximum %d, including array members)", maxValues)
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		for dec.More() {
			if _, err := dec.Token(); err != nil {
				return err
			}
			if err := walkJSONValue(dec, depth+1, values, maxValues); err != nil {
				return err
			}
		}
		_, err = dec.Token()
	case json.Delim('['):
		for dec.More() {
			if err := walkJSONValue(dec, depth+1, values, maxValues); err != nil {
				return err
			}
		}
		_, err = dec.Token()
	}
	return err
}
