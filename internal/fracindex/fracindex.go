// Package fracindex validates fractional-index order keys — the z-order keys
// the whiteboard backend expects on every Excalidraw element.
//
// It is a faithful port of the reference `fractional-indexing` library's
// validateOrderKey (the same rules the frontend and backend enforce). The CLI
// uses it to reject malformed keys locally, before they reach the backend, so
// callers can no longer send index-less or garbage-index elements (e.g. the
// "r00000003" repair artifact from XIN-792) that corrupt a board.
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
	// The fractional part (everything after the integer part) must be composed
	// entirely of base-62 digits.
	for i := intLen; i < len(key); i++ {
		if !isBase62(key[i]) {
			return fmt.Errorf("invalid order key: %q (bad fractional digit %q)", key, string(key[i]))
		}
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
