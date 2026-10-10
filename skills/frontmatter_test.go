package skills

import "testing"

func TestDisabledFromFrontmatterFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		want          bool
	}{
		{"ordinary", "---\ndisabled: false\n---\n# Ready", false},
		{"disabled", "---\ndisabled: true\n---\n# Held", true},
		{"leading blank", "\n---\ndisabled: true\n---", true},
		{"indented", "---\nmetadata:\n  disabled: true\n---", true},
		{"unclosed", "---\ndisabled: false\n# no closing fence", true},
		{"quoted", "---\ndisabled: \"true\"\n---", true},
		{"uppercase", "---\ndisabled: TRUE\n---", true},
		{"commented", "---\ndisabled: true # withheld\n---", true},
		{"title before disabled fence", "# Title\n\n---\ndisabled: true\n---", true},
		{"comment before disabled fence", "<!-- generated -->\n---\ndisabled: true\n---", true},
		{"comment before enabled fence", "<!-- generated -->\n---\nname: x\n---\nbody", false},
		{"thematic break", "# Title\n\ntext\n\n---\n\nmore", false},
		{"setext heading", "Title\n---\n\nbody", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisabledFromFrontmatter([]byte(tt.content)); got != tt.want {
				t.Fatalf("disabled = %t, want %t", got, tt.want)
			}
		})
	}
}
