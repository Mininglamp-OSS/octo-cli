package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DeclaredSource is a structurally validated client declaration, not evidence
// of message existence, membership, or authority. Never apply it to arguments.
type DeclaredSource struct {
	Version     int    `json:"version"`
	Sender      string `json:"sender"`
	Channel     string `json:"channel"`
	ChannelType int    `json:"channel_type"`
	SpaceID     string `json:"space_id"`
	MessageID   string `json:"message_id"`
}

type contextError struct{ path, reason string }

func (e *contextError) Error() string          { return "CONTEXT_INVALID" }
func contextFailure(path, reason string) error { return &contextError{path, reason} }

// strictObject checks decoded keys before a map can collapse escape aliases.
func strictObject(raw []byte, fields ...string) (map[string]json.RawMessage, error) {
	if !validJSONText(raw) {
		return nil, errors.New("type")
	}
	m := make(map[string]json.RawMessage)
	err := visitObjectMembers(raw, func(k string, v json.RawMessage) error {
		if _, ok := m[k]; ok {
			return errors.New("duplicate")
		}
		if len(fields) > 0 {
			found := false
			for _, f := range fields {
				if k == f {
					found = true
				}
			}
			if !found {
				return errors.New("unknown")
			}
		}
		m[k] = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, k := range fields {
		if _, ok := m[k]; !ok {
			return nil, errors.New("missing")
		}
	}
	return m, nil
}

// encoding/json replaces invalid UTF-8 and unpaired surrogates. Security
// metadata must reject those spellings rather than accept a lossy identity.
func validJSONText(raw []byte) bool { //nolint:gocyclo // explicit JSON escape and surrogate-pair state machine
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		end := jsonStringEnd(raw, i)
		for j := i + 1; j < end-1; j++ {
			if raw[j] != '\\' {
				continue
			}
			j++
			if raw[j] != 'u' {
				continue
			}
			n, err := strconv.ParseUint(string(raw[j+1:j+5]), 16, 16)
			if err != nil {
				return false
			}
			j += 4
			if n >= 0xdc00 && n <= 0xdfff {
				return false
			}
			if n >= 0xd800 && n <= 0xdbff {
				if j+6 >= end || raw[j+1] != '\\' || raw[j+2] != 'u' {
					return false
				}
				lo, err := strconv.ParseUint(string(raw[j+3:j+7]), 16, 16)
				if err != nil || lo < 0xdc00 || lo > 0xdfff {
					return false
				}
				j += 6
			}
		}
		i = end - 1
	}
	return true
}

func validID(s string, maxBytes int) bool {
	if len(s) < 1 || len(s) > maxBytes || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func positiveDecimal(s string, bits int) bool {
	if s == "" || s[0] < '1' || s[0] > '9' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	if bits == 63 {
		_, e := strconv.ParseInt(s, 10, 64)
		return e == nil
	}
	_, e := strconv.ParseUint(s, 10, bits)
	return e == nil
}

func parseDeclaredSource(meta json.RawMessage) (*DeclaredSource, error) { //nolint:gocyclo // closed context schema has independent lexical and byte constraints
	const base = "params._meta"
	if len(meta) == 0 {
		return nil, nil
	}
	m, err := strictObject(meta)
	if err != nil {
		return nil, contextFailure(base, schemaReason(err))
	}
	for key := range m {
		if strings.EqualFold(key, "octo") && key != "octo" {
			return nil, contextFailure(base+".octo", "unknown")
		}
	}
	raw, ok := m["octo"]
	if !ok {
		return nil, nil
	}
	if len(raw) > 4096 {
		return nil, contextFailure(base+".octo", "length")
	}
	o, err := contextObject(raw, base+".octo", "version", "im")
	if err != nil {
		return nil, err
	}
	if string(o["version"]) != "1" {
		return nil, contextFailure(base+".octo.version", "enum")
	}
	im, err := contextObject(o["im"], base+".octo.im", "sender", "channel", "channel_type", "space_id", "message_id")
	if err != nil {
		return nil, err
	}
	d := &DeclaredSource{Version: 1}
	for _, f := range []struct {
		k   string
		max int
		dst *string
	}{{"sender", 128, &d.Sender}, {"channel", 256, &d.Channel}, {"space_id", 40, &d.SpaceID}, {"message_id", 19, &d.MessageID}} {
		if len(im[f.k]) == 0 || im[f.k][0] != '"' || json.Unmarshal(im[f.k], f.dst) != nil {
			return nil, contextFailure(base+".octo.im."+f.k, "type")
		}
		if strings.TrimSpace(*f.dst) != *f.dst {
			return nil, contextFailure(base+".octo.im."+f.k, "whitespace")
		}
		for _, r := range *f.dst {
			if unicode.IsControl(r) {
				return nil, contextFailure(base+".octo.im."+f.k, "control")
			}
		}
		if !validID(*f.dst, f.max) {
			return nil, contextFailure(base+".octo.im."+f.k, "length")
		}
	}
	if !positiveDecimal(d.MessageID, 63) {
		return nil, contextFailure(base+".octo.im.message_id", "range")
	}
	switch string(im["channel_type"]) {
	case "1":
		d.ChannelType = 1
	case "2":
		d.ChannelType = 2
	case "5":
		d.ChannelType = 5
	default:
		return nil, contextFailure(base+".octo.im.channel_type", "enum")
	}
	return d, nil
}

func schemaReason(err error) string {
	switch err.Error() {
	case "duplicate", "unknown", "missing":
		return err.Error()
	default:
		return "type"
	}
}

func contextObject(raw []byte, path string, fields ...string) (map[string]json.RawMessage, error) {
	m, err := strictObject(raw)
	if err != nil {
		return nil, contextFailure(path, schemaReason(err))
	}
	for k := range m {
		known := false
		for _, f := range fields {
			if k == f {
				known = true
				break
			}
		}
		if !known {
			return nil, contextFailure(path, "unknown")
		}
	}
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			return nil, contextFailure(path+"."+f, "missing")
		}
	}
	return m, nil
}

func paramsMeta(params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 {
		return nil, nil
	}
	var meta json.RawMessage
	seen := false
	err := visitObjectMembers(params, func(k string, v json.RawMessage) error {
		if k == "_meta" {
			if seen {
				return contextFailure("params._meta", "duplicate")
			}
			seen = true
			meta = v
		}
		return nil
	})
	return meta, err
}

func validRPCID(id json.RawMessage) bool {
	if len(id) == 0 || bytes.Equal(id, []byte("null")) {
		return true
	}
	if !validJSONText(id) {
		return false
	}
	if id[0] == '"' {
		var s string
		return json.Unmarshal(id, &s) == nil && len(s) <= 256
	}
	return len(id) <= 128 && (id[0] == '-' || id[0] >= '0' && id[0] <= '9')
}
