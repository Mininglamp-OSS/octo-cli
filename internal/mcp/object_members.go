package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
)

// visitObjectMembers validates the complete JSON with encoding/json, then
// visits decoded top-level keys and borrowed value spans. Nested values need
// no second grammar scan or copy just to check envelope member collisions.
// The span walker is only entered for a syntactically valid JSON object.
func visitObjectMembers(raw []byte, visit func(string, json.RawMessage) error) error {
	if !json.Valid(raw) || !isJSONObject(raw) {
		return errors.New("expected a complete JSON object")
	}
	i := skipJSONSpace(raw, 0) + 1
	for {
		i = skipJSONSpace(raw, i)
		if raw[i] == '}' {
			return nil
		}
		end := jsonStringEnd(raw, i)
		var key string
		if err := json.Unmarshal(raw[i:end], &key); err != nil {
			return err
		}
		i = skipJSONSpace(raw, end) + 1 // colon, guaranteed by json.Valid
		i = skipJSONSpace(raw, i)
		end = jsonValueEnd(raw, i)
		if err := visit(key, raw[i:end]); err != nil {
			return err
		}
		i = skipJSONSpace(raw, end)
		if raw[i] == ',' {
			i++
		}
	}
}

func skipJSONSpace(raw []byte, i int) int {
	for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\r' || raw[i] == '\n') {
		i++
	}
	return i
}

// jsonStringEnd returns the byte after the closing quote in valid JSON.
func jsonStringEnd(raw []byte, i int) int {
	for {
		i += 1 + bytes.IndexByte(raw[i+1:], '"')
		backslashes := 0
		for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return i + 1
		}
	}
}

// jsonValueEnd finds a value boundary without interpreting its contents. Syntax,
// nesting limits and full-input validation remain encoding/json's job.
func jsonValueEnd(raw []byte, i int) int {
	if raw[i] == '"' {
		return jsonStringEnd(raw, i)
	}
	if raw[i] != '{' && raw[i] != '[' {
		if n := bytes.IndexAny(raw[i:], ",}] \t\r\n"); n >= 0 {
			return i + n
		}
		return len(raw)
	}
	depth := 1
	for depth > 0 {
		i += 1 + bytes.IndexAny(raw[i+1:], "{}[]\"")
		switch raw[i] {
		case '"':
			i = jsonStringEnd(raw, i) - 1
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
	}
	return i + 1
}
