package cmd

import (
	"testing"
)

func TestMaskToken(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"app_abcdefgh12345678", "app_abcd***"},
		{"short", "short***"},
		{"", nil},
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
