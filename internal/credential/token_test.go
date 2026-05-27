package credential

import (
	"strings"
	"testing"
)

func TestTokenKind(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"app_x":    "app_bot",
		"bf_x":     "user_bot",
		"weirdtok": "unknown",
		"app":      "unknown", // too short to be the app_ prefix
	}
	for in, want := range cases {
		if got := TokenKind(in); got != want {
			t.Errorf("TokenKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMaskToken(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"app_abcdefgh12345678", "app_ab***5678"},
		{"bf_something", "bf_so***hing"},
		{"app_tiny", "app_***"},         // body "tiny" too short
		{"bf_xx", "bf_***"},             // body too short
		{"short", "***"},                // unknown prefix
		{"unknown_format_token", "***"}, // unknown prefix: reveal nothing
	}
	for _, c := range cases {
		got := MaskToken(c.in)
		if got != c.want {
			t.Errorf("MaskToken(%q) = %q, want %q", c.in, got, c.want)
		}
		// The secret middle must never appear verbatim.
		if c.in != "" && got != "" && strings.Contains(got, "****") {
			t.Errorf("MaskToken(%q) = %q leaks length via variable asterisks", c.in, got)
		}
	}
}

// TestMaskTokenNeverRevealsFullSecret guards the invariant that the masked form
// is never the original token for any non-trivial input.
func TestMaskTokenNeverRevealsFullSecret(t *testing.T) {
	for _, tok := range []string{"app_abcdefgh12345678", "bf_something", "unknown_format_token"} {
		if MaskToken(tok) == tok {
			t.Errorf("MaskToken(%q) returned the full token", tok)
		}
	}
}
