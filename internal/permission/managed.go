package permission

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// ManagedIdentity is the opt-in minimal projection. Policy is returned without
// interpretation so the existing context-policy validator stays independent.
type ManagedIdentity struct {
	BotUID string
	Policy json.RawMessage
}

// ParseIdentityV2 rejects legacy responses and extra display/owner/first-Space
// fields. It never retries a v1 verification or changes credential selection.
// The caller must have authenticated the exact configured authority first.
func ParseIdentityV2(raw []byte, withPolicy bool) (ManagedIdentity, error) {
	v, err := decodeStrict(bytes.NewReader(raw), 16<<10)
	if err != nil {
		return ManagedIdentity{}, ErrUnavailable
	}
	m, ok := v.(map[string]any)
	count := 3
	if withPolicy {
		count++
	}
	if !ok || len(m) != count || m["identity_version"] != json.Number("2") || m["principal_kind"] != "user_bot" {
		return ManagedIdentity{}, ErrUnavailable
	}
	uid, ok := m["bot_uid"].(string)
	if !ok || !ValidNativeID(uid) {
		return ManagedIdentity{}, ErrUnavailable
	}
	var policy json.RawMessage
	if withPolicy {
		p, ok := m["mcp_policy"].(map[string]any)
		if !ok {
			return ManagedIdentity{}, ErrUnavailable
		}
		policy = canonical(p)
	} else if _, present := m["mcp_policy"]; present {
		return ManagedIdentity{}, ErrUnavailable
	}
	return ManagedIdentity{uid, policy}, nil
}

// DeclarationV2 remains a claim until byte-equal native evidence and private
// capsule registration are verified. It grants no access on its own.
type DeclarationV2 struct {
	Sender, Channel, SpaceID, MessageID string
	ChannelType                         int
}

// ParseDeclarationV2 checks claim syntax only; native/private equality is separate.
func ParseDeclarationV2(raw []byte) (DeclarationV2, error) { //nolint:gocyclo // closed fields and three native channel domains must reject every coercion before use
	v, err := decodeStrict(bytes.NewReader(raw), 4096)
	if err != nil {
		return DeclarationV2{}, ErrInvalid
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 2 || m["version"] != json.Number("2") {
		return DeclarationV2{}, ErrInvalid
	}
	im, ok := m["im"].(map[string]any)
	if !ok || len(im) != 5 {
		return DeclarationV2{}, ErrInvalid
	}
	var out DeclarationV2
	for key, dst := range map[string]*string{"sender": &out.Sender, "channel": &out.Channel, "space_id": &out.SpaceID, "message_id": &out.MessageID} {
		s, ok := im[key].(string)
		if !ok {
			return DeclarationV2{}, ErrInvalid
		}
		*dst = s
	}
	if !ValidNativeID(out.Sender) || !ValidNativeID(out.SpaceID) {
		return DeclarationV2{}, ErrInvalid
	}
	if out.MessageID == "" || out.MessageID[0] == '0' {
		return DeclarationV2{}, ErrInvalid
	}
	for _, c := range out.MessageID {
		if c < '0' || c > '9' {
			return DeclarationV2{}, ErrInvalid
		}
	}
	if n, err := strconv.ParseInt(out.MessageID, 10, 64); err != nil || n <= 0 {
		return DeclarationV2{}, ErrInvalid
	}
	switch im["channel_type"] {
	case json.Number("1"):
		out.ChannelType = 1
		if !validText(out.Channel, 100, 400) {
			return DeclarationV2{}, ErrInvalid
		}
	case json.Number("2"):
		out.ChannelType = 2
		if !ValidNativeID(out.Channel) {
			return DeclarationV2{}, ErrInvalid
		}
	case json.Number("5"):
		out.ChannelType = 5
		if !validText(out.Channel, 256, 256) {
			return DeclarationV2{}, ErrInvalid
		}
	default:
		return DeclarationV2{}, ErrInvalid
	}
	return out, nil
}
