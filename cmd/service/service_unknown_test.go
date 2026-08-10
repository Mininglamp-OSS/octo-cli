package service

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Round-10 P2-8. childCommandNames filters on IsAvailableCommand, so a parent whose
// children are all hidden (or which has none) rendered "available: " with nothing
// after it — a message that reads like a truncated bug rather than a diagnostic.
func TestRejectUnknownSubcommand_OmitsAnEmptyAvailableList(t *testing.T) {
	for _, tc := range []struct {
		name     string
		children []*cobra.Command
		wantList bool
	}{
		{"no children at all", nil, false},
		{"every child hidden", []*cobra.Command{{Use: "secret", Hidden: true, Run: func(*cobra.Command, []string) {}}}, false},
		// Runnable, or cobra does not count it as available and the fixture would
		// be asserting the empty-list branch by accident.
		{"a visible child", []*cobra.Command{{Use: "list", Run: func(*cobra.Command, []string) {}}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := &cobra.Command{Use: "drive"}
			for _, c := range tc.children {
				parent.AddCommand(c)
			}
			err := rejectUnknownSubcommand(parent, []string{"bogus"})
			if err == nil {
				t.Fatal("an unknown subcommand must be an error")
			}
			hasList := strings.Contains(err.Error(), "available:")
			if hasList != tc.wantList {
				t.Errorf("message = %q; available list present = %v, want %v", err.Error(), hasList, tc.wantList)
			}
			if strings.HasSuffix(err.Error(), "available: ") {
				t.Errorf("message ends with an empty list: %q", err.Error())
			}
			if strings.Contains(err.Error(), "bogus") {
				t.Errorf("the unrecognised token must not be echoed: %q", err.Error())
			}
		})
	}
}
