// Package fracindex validates fractional-index order keys — the z-order keys
// the whiteboard backend expects on every Excalidraw element.
//
// A key is accepted only if it satisfies every rule the frontend and backend
// enforce, so the CLI rejects locally exactly what they reject server-side:
//   - a well-formed integer part (correct length for its head character), from
//     the reference `fractional-indexing` library's validateOrderKey;
//   - a full-key base-62 charset, from the frontend/backend INDEX_RE
//     (/^[A-Za-z0-9]+$/) — the integer part is charset-checked, not just the
//     fraction;
//   - no trailing '0' in the fractional part, from the reference
//     validateOrderKey (a non-canonical key).
//
// The CLI uses it to reject malformed keys locally, before they reach the
// backend, so callers can no longer send index-less or garbage-index elements
// (e.g. the "r00000003" repair artifact from XIN-792) that corrupt a board.
package fracindex

import (
	"errors"
	"fmt"
)

// base62Digits is the ordered digit alphabet used for the fractional part of a
// key. The integer part's magnitude is encoded in the head character.
const base62Digits = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// smallestInteger is the reserved lower bound the library never emits as a real
// key; treating it as valid would let a caller pin an element below every other.
const smallestInteger = "A00000000000000000000000000"

// integerLength returns the total length the integer part must have given its
// head character. Mirrors the reference getIntegerLength: 'a'..'z' encode
// increasing positive magnitudes, 'A'..'Z' increasing negative magnitudes.
func integerLength(head byte) (int, error) {
	switch {
	case head >= 'a' && head <= 'z':
		return int(head-'a') + 2, nil
	case head >= 'A' && head <= 'Z':
		return int('Z'-head) + 2, nil
	default:
		return 0, fmt.Errorf("invalid order key head: %q", string(head))
	}
}

// ValidateOrderKey reports whether key is a well-formed fractional index. It
// returns nil for a valid key and a descriptive error otherwise. An empty key
// is always invalid.
func ValidateOrderKey(key string) error {
	if key == "" {
		return errors.New("order key is empty")
	}
	if key == smallestInteger {
		return fmt.Errorf("invalid order key: %q (reserved lower bound)", key)
	}
	intLen, err := integerLength(key[0])
	if err != nil {
		return err
	}
	if intLen > len(key) {
		return fmt.Errorf("invalid order key: %q (integer part truncated)", key)
	}
	// Every byte of the key — integer part and fractional part alike — must be a
	// base-62 digit. The frontend (INDEX_RE = /^[A-Za-z0-9]+$/) and backend
	// (identical regex) apply this gate to the whole key, so a non-base62
	// integer-part byte such as the '!' in "a!" must be rejected here too.
	for i := 0; i < len(key); i++ {
		if !isBase62(key[i]) {
			return fmt.Errorf("invalid order key: %q (bad digit %q)", key, string(key[i]))
		}
	}
	// The fractional part (everything after the integer part) must not end in the
	// first digit '0': a trailing zero is non-canonical and equivalent to the
	// shorter key, so the reference validateOrderKey and the frontend/backend all
	// reject it (e.g. "a00", "a10").
	if len(key) > intLen && key[len(key)-1] == base62Digits[0] {
		return fmt.Errorf("invalid order key: %q (fractional part ends in zero)", key)
	}
	return nil
}

func isBase62(c byte) bool {
	for i := 0; i < len(base62Digits); i++ {
		if base62Digits[i] == c {
			return true
		}
	}
	return false
}
