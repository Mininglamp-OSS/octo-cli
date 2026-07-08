package skills

import (
	"strings"
	"testing"
)

// TestOctoDocsSkillEmbedded confirms the octo-docs SKILL.md is embedded and
// discoverable: it must ship under the */SKILL.md glob, carry the expected
// frontmatter name, stay enabled (no `disabled: true`, so `octo-cli skills`
// lists it), and lead with the live-content-editing limitation.
func TestOctoDocsSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/SKILL.md")
	if err != nil {
		t.Fatalf("octo-docs/SKILL.md not embedded: %v", err)
	}
	content := string(b)

	if !strings.Contains(content, "name: octo-docs") {
		t.Error("frontmatter missing `name: octo-docs`")
	}

	// Discoverability: the skills command withholds `disabled: true` skills, so
	// the frontmatter must NOT disable this one.
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "disabled: true" {
			t.Error("octo-docs must stay enabled to be discoverable via `octo-cli skills`")
		}
	}

	// The content-editing limitation must appear near the top (before the first
	// command section) so a reader hits it first.
	idx := strings.Index(content, "## 1.")
	head := content
	if idx > 0 {
		head = content[:idx]
	}
	if !strings.Contains(head, "NOT available") {
		t.Error("skill must lead with the 'live content editing is NOT available' limitation")
	}
}
