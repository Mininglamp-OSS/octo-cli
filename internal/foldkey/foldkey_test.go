package foldkey

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// TestKeyMatchesEqualFoldPerRune checks every rune against every member of its
// SimpleFold orbit and against its neighbours, so Key groups single runes
// exactly as strings.EqualFold does across the whole Unicode range.
func TestKeyMatchesEqualFoldPerRune(t *testing.T) {
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogates encode as U+FFFD, covered below
		}
		a := string(r)
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if Key(a) != Key(string(f)) {
				t.Fatalf("Key(%U) != Key(%U) although EqualFold is true", r, f)
			}
		}
		if r > 0 {
			b := string(r - 1)
			if (Key(a) == Key(b)) != strings.EqualFold(a, b) {
				t.Fatalf("Key/EqualFold disagree on %U and %U", r, r-1)
			}
		}
	}
}

func TestKeyMatchesEqualFoldOnStrings(t *testing.T) {
	cases := [][2]string{
		{"space_id", "ſpace_id"}, // long s
		{"kind", "Kind"},         // Kelvin sign
		{"headers", "HEADERS"},
		{"headers", "HEADERſ"},
		{"ß", "ss"},                      // no full folding: not equal
		{"a\xffb", "a�b"},                // invalid byte decodes to U+FFFD
		{"a\xffb", "a\xfeb"},             // two invalid bytes compare equal
		{"Σ", "ς"},                       // Greek sigma orbit of three
		{"Ǆ", "ǆ"},                       // DŽ title-case orbit
		{"abc", "abcd"},                  // different length
		{"İ", "i"},                       // dotted capital I is not "i"
		{"ẞ", "ß"},                       // capital sharp s
		{"extra_000001", "EXTRA_000001"}, // ASCII fast path
		{"xͅ", "xι"},                     // combining ypogegrammeni orbit
		{"", ""},                         // empty
		{"ⱥ", "Ⱥ"},                       // orbit whose members differ in UTF-8 length
		{"Mcpſervers", "mcpservers"},     // alias at a nested step
		{"Kſ", "ks"},                     // two special folds
		{"Kſ", "Kſ"},                     // mixed
		{"Kſ", "kS"},                     // mixed case
	}
	for _, c := range cases {
		if got, want := Key(c[0]) == Key(c[1]), strings.EqualFold(c[0], c[1]); got != want {
			t.Errorf("Key(%q)==Key(%q) is %v, EqualFold is %v", c[0], c[1], got, want)
		}
	}
	// Random strings drawn from runes that have nontrivial orbits.
	var pool []rune
	for r := rune(0); r < 0x2D00 && len(pool) < 4000; r++ {
		if unicode.SimpleFold(r) != r {
			pool = append(pool, r)
		}
	}
	pool = append(pool, utf8.RuneError, 'a', '_')
	rng := rand.New(rand.NewSource(1))
	gen := func() string {
		n := 1 + rng.Intn(4)
		var b strings.Builder
		for i := 0; i < n; i++ {
			b.WriteRune(pool[rng.Intn(len(pool))])
		}
		return b.String()
	}
	for i := 0; i < 200000; i++ {
		a := gen()
		// Half the time compare against a fold variant of a.
		var b string
		if i%2 == 0 {
			var sb strings.Builder
			for _, r := range a {
				if rng.Intn(2) == 0 {
					r = unicode.SimpleFold(r)
				}
				sb.WriteRune(r)
			}
			b = sb.String()
		} else {
			b = gen()
		}
		if (Key(a) == Key(b)) != strings.EqualFold(a, b) {
			t.Fatalf("Key/EqualFold disagree on %q and %q", a, b)
		}
	}
}
