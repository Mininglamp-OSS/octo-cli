package skills

import (
	"strings"
	"testing"
)

// TestOctoDocsSkillEmbedded confirms the octo-docs skill is embedded and
// discoverable: SKILL.md ships under the */SKILL.md glob, carries the expected
// frontmatter name, stays enabled (no `disabled: true`, so `octo-cli skills`
// lists it), and — now that the skill is progressive — routes to its reference
// files, which are themselves embedded and carry the per-surface commands.
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

	// Progressive disclosure: SKILL.md must route to each reference file so a
	// reader loads only the one matching its task. It must also keep the up-front
	// doc_type / whiteboard framing so the surface split is clear.
	for _, ref := range []string{"sheet.md", "doc.md", "board.md", "common.md"} {
		if !strings.Contains(content, ref) {
			t.Errorf("SKILL.md must route to reference file %q", ref)
		}
	}
	if !strings.Contains(content, "doc_type") {
		t.Error("SKILL.md must state up front that the editable surface is chosen by doc_type")
	}
	if !strings.Contains(strings.ToLower(content), "whiteboard") {
		t.Error("SKILL.md must mention the whiteboard/board surface")
	}

	// The reference files are embedded (ride the */*.md glob) and each leads with
	// its surface's read/edit commands.
	refChecks := map[string][]string{
		"octo-docs/doc.md":    {"docs content get", "docs content edit"},
		"octo-docs/sheet.md":  {"docs sheet get", "docs sheet edit"},
		"octo-docs/board.md":  {"docs scene get", "docs scene edit"},
		"octo-docs/common.md": {"docs comments add", "docs versions restore", "docs members set", "docs attachments presign"},
	}
	for path, needles := range refChecks {
		rb, err := FS.ReadFile(path)
		if err != nil {
			t.Errorf("reference file %q not embedded: %v", path, err)
			continue
		}
		rc := string(rb)
		for _, n := range needles {
			if !strings.Contains(rc, n) {
				t.Errorf("%s must document %q", path, n)
			}
		}
	}
}
