package permission

import (
	"bytes"
	"encoding/json"
)

// ControlFrame is the closed private host request envelope, not an MCP tool.
type ControlFrame struct {
	Version                      int
	RequestID, SessionID, Method string
	Payload                      map[string]any
}

// ParseControlFrame is called only after mutual channel authentication.
func ParseControlFrame(raw []byte) (ControlFrame, error) {
	v, err := decodeStrict(bytes.NewReader(raw), 65536)
	if err != nil {
		return ControlFrame{}, ErrInvalid
	}
	if !bytes.Equal(raw, canonical(v)) {
		return ControlFrame{}, ErrInvalid
	}
	m := object(v)
	if !fields(m, "control_version", "request_id", "session_id", "method", "payload") || m["control_version"] != json.Number("1") || !validHex(text(m["request_id"]), 32) || !validHex(text(m["session_id"]), 32) || object(m["payload"]) == nil {
		return ControlFrame{}, ErrInvalid
	}
	return ControlFrame{1, text(m["request_id"]), text(m["session_id"]), text(m["method"]), object(m["payload"])}, nil
}

// PrivateJSON encodes bounded canonical private records without lexical repair.
func PrivateJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalid
	}
	value, err := decodeStrict(bytes.NewReader(b), 65536)
	if err != nil {
		return nil, err
	}
	return canonical(value), nil
}

// CanonicalManifest validates the separate approved 1 MiB metadata domain.
// Private control frames themselves retain their 64 KiB limit.
func CanonicalManifest(raw []byte) ([]byte, error) {
	v, err := decodeStrict(bytes.NewReader(raw), 1<<20)
	if err != nil || object(v) == nil {
		return nil, ErrInvalid
	}
	return canonical(v), nil
}

// DecodeManifest retains the canonical native Header integer domains.
func DecodeManifest(raw []byte) (map[string]any, error) {
	v, err := decodeStrict(bytes.NewReader(raw), 1<<20)
	if err != nil || object(v) == nil {
		return nil, ErrInvalid
	}
	return object(v), nil
}

// NewID creates an unpredictable private control/capsule/action identifier.
func NewID() string { return randomID() }

// Fields validates a closed private payload before any method side effects.
func Fields(m map[string]any, names ...string) bool { return fields(m, names...) }

// ReadPolicy obtains a complete private P for native admission preparation.
func (c *Coordinator) ReadPolicy(raw []byte) (Bundle, error) {
	return ParseBundle(bytes.NewReader(raw))
}
