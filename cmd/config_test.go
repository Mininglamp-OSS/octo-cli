package cmd

import (
	"testing"
)

func TestMaskToken(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"app_abcdefgh12345678", "app_ab***5678"},
		{"bf_something", "bf_so***hing"},
		{"app_tiny", "app_***"}, // body too short to reveal
		{"octo_loop_abcdefghijk", "octo_loop_ab***hijk"},
		{"short", "***"}, // unknown prefix
		{"", nil},
		{"unknown_format_token", "***"}, // unknown prefix: reveal nothing
	}
	for _, c := range cases {
		if got := maskToken(c.in); got != c.want {
			t.Errorf("maskToken(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBotKind(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"app_xxx", "app_bot"},
		{"bf_xxx", "user_bot"},
		{"other", "unknown"},
		{"", nil},
	}
	for _, c := range cases {
		if got := botKind(c.in); got != c.want {
			t.Errorf("botKind(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
