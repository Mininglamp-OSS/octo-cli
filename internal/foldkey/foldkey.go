// Package foldkey maps a string to a canonical key under Unicode simple case
// folding, so names that strings.EqualFold treats as equal can be grouped with
// one map lookup instead of a comparison against every other name.
package foldkey

import (
	"strings"
	"unicode"
)

// Key returns a canonical form of s such that Key(a) == Key(b) exactly when
// strings.EqualFold(a, b). EqualFold compares rune by rune and treats two
// runes as equal when they share a unicode.SimpleFold orbit, so each rune,
// including the U+FFFD an invalid byte decodes to, is replaced by the smallest
// rune of its orbit. Special folds such as U+017F (long s) and U+212A (Kelvin
// sign) therefore share a key with their ASCII letters.
func Key(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		low := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < low {
				low = f
			}
		}
		b.WriteRune(low)
	}
	return b.String()
}
