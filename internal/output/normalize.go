package output

import (
	"bytes"
	"encoding/json"
	"fmt"

	"strconv"
	"strings"
)

// MaxUint64Decimal is the largest value a backend uint64 id can hold. Inputs
// are range-checked against it so an out-of-range id fails locally instead of
// being silently truncated on the wire.
const MaxUint64Decimal = "18446744073709551615"

// NormalizeResponse reshapes a backend JSON body for CLI output.
//
// Two spec-declared, opt-in transforms run in order:
//
//  1. fieldAliases (x-octo-response-fields) renames/duplicates keys, e.g. the
//     drive share DTO's bare `id` becomes both `share_id` (for revoke) and
//     `share_token` (consumed internally by share access/download), so an
//     Agent never has to guess which meaning a generic `id` carries.
//  2. losslessFields (x-octo-lossless-id-fields) rewrites uint64 ids from JSON
//     numbers to decimal strings. The decode uses json.Decoder.UseNumber, so a
//     value above 2^53 survives the round-trip that a float64 would round.
//
// Aliases run first so lossless paths can name the post-alias key. Both are
// no-ops when their spec metadata is absent, leaving the body byte-identical
// for every operation that does not declare them.
func NormalizeResponse(raw []byte, fieldAliases map[string][]string, losslessFields []string) ([]byte, error) {
	if len(raw) == 0 || (len(fieldAliases) == 0 && len(losslessFields) == 0) {
		return raw, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		// Not JSON (binary describe envelopes never declare these extensions);
		// pass the bytes through untouched rather than failing the command.
		return raw, nil //nolint:nilerr // non-JSON bodies are legitimately untransformed
	}

	for source, targets := range fieldAliases {
		applyAlias(doc, source, targets)
	}
	for _, path := range losslessFields {
		applyLossless(doc, path)
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("normalize response: %w", err)
	}
	return out, nil
}

// applyAlias copies the value at source onto each target key in the same
// object and removes the source key. A missing source path is a no-op, so a
// response that omits an optional field is not given a null alias.
func applyAlias(doc any, source string, targets []string) {
	walkPath(doc, source, func(parent map[string]any, key string) {
		v, ok := parent[key]
		if !ok {
			return
		}
		for _, t := range targets {
			parent[t] = v
		}
		if !containsString(targets, key) {
			delete(parent, key)
		}
	})
}

// applyLossless rewrites the numeric value at path to its decimal string form.
// Non-numeric values (already a string, or null) are left alone.
func applyLossless(doc any, path string) {
	walkPath(doc, path, func(parent map[string]any, key string) {
		n, ok := parent[key].(json.Number)
		if !ok {
			return
		}
		parent[key] = n.String()
	})
}

// walkPath resolves a dotted field path against doc and invokes fn with the
// owning object and final key for every match. Two segment forms are
// supported: `a.b` descends into object `a`, and `a[].b` descends into every
// element of array `a`. A bare `a` addresses a top-level key. Segments that do
// not resolve are skipped silently — the transforms are declarative hints, not
// assertions about a response that may legitimately omit fields.
func walkPath(doc any, path string, fn func(parent map[string]any, key string)) {
	segments := strings.Split(path, ".")
	walkSegments(doc, segments, fn)
}

func walkSegments(node any, segments []string, fn func(map[string]any, string)) {
	if len(segments) == 0 || node == nil {
		return
	}
	seg := segments[0]
	name, isArray := strings.CutSuffix(seg, "[]")

	if isArray {
		obj, ok := node.(map[string]any)
		if !ok {
			return
		}
		items, ok := obj[name].([]any)
		if !ok {
			return
		}
		for _, item := range items {
			if len(segments) == 1 {
				// `a[]` with no trailing field is not a meaningful target.
				continue
			}
			walkSegments(item, segments[1:], fn)
		}
		return
	}

	obj, ok := node.(map[string]any)
	if !ok {
		return
	}
	if len(segments) == 1 {
		fn(obj, name)
		return
	}
	walkSegments(obj[name], segments[1:], fn)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// ParseUint64Decimal validates a caller-supplied uint64 id. It accepts only a
// plain decimal string in [0, 2^64-1] — no sign, no exponent, no hex, no
// underscores — so an id that a JavaScript-based Agent may have mangled is
// rejected locally rather than silently truncated by the backend.
func ParseUint64Decimal(flagOrArg, value string) (uint64, *ExitError) {
	if value == "" {
		return 0, ErrValidation(
			fmt.Sprintf("%s must be a decimal uint64 id", flagOrArg),
			"pass the id exactly as returned by a previous drive command")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, ErrValidation(
				fmt.Sprintf("%s must be a decimal uint64 id, got %q", flagOrArg, value),
				"pass digits only — the id from `-q '.data.id' -r`, with no sign, spaces, or exponent")
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, ErrValidation(
			fmt.Sprintf("%s is out of range for a uint64 id: %q", flagOrArg, value),
			"the maximum id is "+MaxUint64Decimal)
	}
	return n, nil
}

// Uint64JSONNumber renders a validated uint64 as a json.Number so it marshals
// as a bare JSON integer. This keeps the wire contract (integer) unchanged
// while the CLI surface accepts and emits decimal strings. Go's int is not
// usable here: on a 64-bit platform math.MaxInt64 is an order of magnitude
// below math.MaxUint64, so an int-typed flag cannot carry the upper half of the
// id space.
func Uint64JSONNumber(n uint64) json.Number {
	return json.Number(strconv.FormatUint(n, 10))
}
