package permission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Key is the exact configured origin, verified Bot and explicit Space.
type Key struct {
	Origin  string `json:"origin"`
	BotUID  string `json:"bot_uid"`
	SpaceID string `json:"space_id"`
}

// Rule is a strictly validated complete caller/source selector.
type Rule map[string]any

// Stage is independently U (legacy) or E (enforced).
type Stage struct {
	State string `json:"state"`
	Rules []Rule `json:"rules"`
}

// Bundle is the complete ten-field enforcement projection P.
type Bundle struct {
	SchemaVersion  int    `json:"schema_version"`
	Key            Key    `json:"key"`
	Incarnation    string `json:"scope_incarnation"`
	Revision       string `json:"revision"`
	ContextEpoch   string `json:"context_epoch"`
	CatalogVersion string `json:"catalog_version"`
	CatalogDigest  string `json:"catalog_digest"`
	Digest         string `json:"bundle_digest"`
	Invocation     Stage  `json:"invocation"`
	Capability     Stage  `json:"capability"`
}

func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if c >= '0' && c <= '9' {
			continue
		}
		if c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}

// U64 accepts only a canonical decimal uint64 string.
func U64(s string) (uint64, error) {
	if s == "" || len(s) > 20 || len(s) > 1 && s[0] == '0' {
		return 0, ErrInvalid
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrInvalid
		}
	}
	n, e := strconv.ParseUint(s, 10, 64)
	if e != nil {
		return 0, ErrInvalid
	}
	return n, nil
}

// ValidNativeID checks scalar/byte syntax, never native existence or authority.
func ValidNativeID(s string) bool { return validText(s, 40, 160) }
func validText(s string, scalars, octets int) bool {
	if s == "" || len(s) > octets || !utf8.ValidString(s) || utf8.RuneCountInString(s) > scalars || strings.TrimSpace(s) != s {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// ValidOrigin checks the exact HTTPS authority grammar without normalization.
func ValidOrigin(s string) bool {
	if s == "" || len(s) > 2048 || strings.Contains(s, "#") {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && !u.ForceQuery
}

// Valid checks structural domains without inferring native authority.
func (k Key) Valid() bool {
	return ValidOrigin(k.Origin) && ValidNativeID(k.BotUID) && ValidNativeID(k.SpaceID)
}
func asObject(v any) (map[string]any, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, ErrInvalid
	}
	x, e := decodeStrict(bytes.NewReader(b), MaxBundleBytes)
	if e != nil {
		return nil, e
	}
	m, ok := x.(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	return m, nil
}

// ParseBundle validates strict lexical/structural/canonical P and its digest.
func ParseBundle(r io.Reader) (Bundle, error) {
	v, e := decodeStrict(r, MaxBundleBytes)
	if e != nil {
		return Bundle{}, ErrUnavailable
	}
	m, ok := v.(map[string]any)
	if !ok {
		return Bundle{}, ErrUnavailable
	}
	if !validBundleShape(m) {
		return Bundle{}, ErrUnavailable
	}
	if normalizeBundle(m) != nil {
		return Bundle{}, ErrUnavailable
	}
	digest := m["bundle_digest"].(string)
	delete(m, "bundle_digest")
	if hash(canonical(m)) != digest {
		return Bundle{}, ErrUnavailable
	}
	m["bundle_digest"] = digest
	var p Bundle
	if json.Unmarshal(canonical(m), &p) != nil {
		return Bundle{}, ErrUnavailable
	}
	return p, nil
}

// Seal validates the complete resulting object, fills only group defaults and
// computes its digest. Callers still need current native/reference authority.
func Seal(p Bundle) (Bundle, []byte, error) { //nolint:gocritic,gocyclo // immutable input snapshot and guarded input-only default filling precedes closed projection validation without mutating caller maps
	p.Digest = strings.Repeat("0", 64)
	m, e := asObject(p)
	if e != nil {
		return Bundle{}, nil, e
	}
	// Defaults are input-only. ParseBundle still requires their explicit wire
	// representation. Work on the detached map, never the caller's rule maps.
	for _, name := range []string{"invocation", "capability"} {
		if stage, ok := m[name].(map[string]any); ok {
			if rules, ok := stage["rules"].([]any); ok {
				for _, r := range rules {
					if rule, ok := r.(map[string]any); ok {
						if source, ok := rule["source"].(map[string]any); ok {
							if refs, ok := source["refs"].([]any); ok {
								for _, ref := range refs {
									if ref, ok := ref.(map[string]any); ok && ref["kind"] == "group" {
										if _, present := ref["include_descendants"]; !present {
											ref["include_descendants"] = false
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	if !validBundleShape(m) || normalizeBundle(m) != nil {
		return Bundle{}, nil, ErrInvalid
	}
	delete(m, "bundle_digest")
	m["bundle_digest"] = hash(canonical(m))
	b := canonical(m)
	if len(b) > MaxBundleBytes {
		return Bundle{}, nil, ErrInvalid
	}
	// Unmarshal into a fresh value. Reusing p would reuse its rule maps and
	// silently mutate the caller's stored intent during normalization/cloning.
	var sealed Bundle
	if json.Unmarshal(b, &sealed) != nil {
		return Bundle{}, nil, ErrInvalid
	}
	return sealed, b, nil
}

func sortUnique(a []any) error {
	sort.Slice(a, func(i, j int) bool { return bytes.Compare(canonical(a[i]), canonical(a[j])) < 0 })
	for i := 1; i < len(a); i++ {
		if bytes.Equal(canonical(a[i-1]), canonical(a[i])) {
			return ErrInvalid
		}
	}
	return nil
}
func normalizeRules(a []any) error {
	seen := map[string]bool{}
	for _, v := range a {
		r := v.(map[string]any)
		id := r["rule_id"].(string)
		if seen[id] {
			return ErrInvalid
		}
		seen[id] = true
		caller := r["caller"].(map[string]any)
		if u, ok := caller["uids"]; ok {
			if sortStrings(u.([]any)) != nil {
				return ErrInvalid
			}
		}
		source := r["source"].(map[string]any)
		if refs, ok := source["refs"]; ok {
			for _, ref := range refs.([]any) {
				m := ref.(map[string]any)
				if m["kind"] == "group" {
					if _, ok := m["include_descendants"]; !ok {
						m["include_descendants"] = false
					}
				}
			}
			if sortUnique(refs.([]any)) != nil {
				return ErrInvalid
			}
		}
		if types, ok := source["conversation_types"]; ok {
			if sortStrings(types.([]any)) != nil {
				return ErrInvalid
			}
		}
		if caps, ok := r["capabilities"]; ok {
			if sortStrings(caps.([]any)) != nil {
				return ErrInvalid
			}
		}
	}
	sort.Slice(a, func(i, j int) bool {
		return a[i].(map[string]any)["rule_id"].(string) < a[j].(map[string]any)["rule_id"].(string)
	})
	return nil
}
func sortStrings(a []any) error {
	sort.Slice(a, func(i, j int) bool { return a[i].(string) < a[j].(string) })
	for i := 1; i < len(a); i++ {
		if a[i] == a[i-1] {
			return ErrInvalid
		}
	}
	return nil
}
func normalizeBundle(m map[string]any) error {
	selectors, caps := 0, 0
	for _, name := range []string{"invocation", "capability"} {
		rules := m[name].(map[string]any)["rules"].([]any)
		if normalizeRules(rules) != nil {
			return ErrInvalid
		}
		for _, v := range rules {
			r := v.(map[string]any)
			c := r["caller"].(map[string]any)
			s := r["source"].(map[string]any)
			if a, ok := c["uids"]; ok {
				selectors += len(a.([]any))
			}
			if a, ok := s["refs"]; ok {
				selectors += len(a.([]any))
			}
			if a, ok := s["conversation_types"]; ok {
				selectors += len(a.([]any))
			}
			if a, ok := r["capabilities"]; ok {
				caps += len(a.([]any))
			}
		}
	}
	if selectors > 4096 || caps > 1024 {
		return ErrInvalid
	}
	return nil
}
