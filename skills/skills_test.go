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

func TestOctoSummarySkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-summary/SKILL.md")
	if err != nil {
		t.Fatalf("octo-summary/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{"name: octo-summary", "summary create", "summary list", "summary get", "summary result", "context_before", "bf_*"} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-summary skill missing %q", want)
		}
	}
}

func TestOctoLoopSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-loop/SKILL.md")
	if err != nil {
		t.Fatalf("octo-loop/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"name: octo-loop",
		"loop task list",
		"loop expert list",
		"loop expert-team list",
		"OCTO_API_BASE_URL/fleet/api/v1",
		"not infer",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("octo-loop skill missing %q", want)
		}
	}
}

func TestOctoMarketplaceReferencesEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-marketplace/SKILL.md")
	if err != nil {
		t.Fatalf("octo-marketplace/SKILL.md not embedded: %v", err)
	}
	content := string(b)
	for _, ref := range []string{"skills.md", "mcp.md"} {
		if !strings.Contains(content, ref) {
			t.Errorf("octo-marketplace/SKILL.md must route to %q", ref)
		}
		path := "octo-marketplace/" + ref
		rb, readErr := FS.ReadFile(path)
		if readErr != nil {
			t.Errorf("reference %q not embedded: %v", path, readErr)
			continue
		}
		if len(rb) == 0 {
			t.Errorf("reference %q is empty", path)
		}
	}
}

func TestOctoMarketplacePublishFlowChecksOwnedNameBeforeMutation(t *testing.T) {
	b, err := FS.ReadFile("octo-marketplace/skills.md")
	if err != nil {
		t.Fatalf("read marketplace skills reference: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		"or an accessible Skill",
		"mktemp -d",
		"Never modify the source directory",
		"skill mine list --q <name> --page-all",
		"package `id` equals that Skill's `skill_id`",
		"name conflict",
		"soft-deleted Skill may still reserve the name",
		"do not initialize or upload before it",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("publish workflow must contain %q", want)
		}
	}
	if strings.Index(content, "skill mine list --q <name> --page-all") > strings.Index(content, "skill-upload create --file-name") {
		t.Error("owned-name lookup must happen before upload initialization")
	}
	if strings.Index(content, "skill-category list") > strings.Index(content, "one final plan") {
		t.Error("category lookup must happen before the final confirmation plan")
	}
}

// TestOctoDriveSkillEmbedded confirms the drive skill ships in the binary,
// stays discoverable, and leads with the facts an Agent gets wrong without
// being told: the credential is shared with every other domain, file ids must be
// copied not computed, and a share is handed over as a URL.
func TestOctoDriveSkillEmbedded(t *testing.T) {
	b, err := FS.ReadFile("octo-drive/SKILL.md")
	if err != nil {
		t.Fatalf("octo-drive/SKILL.md not embedded: %v", err)
	}
	content := string(b)

	if !strings.Contains(content, "name: octo-drive") {
		t.Error("frontmatter missing `name: octo-drive`")
	}
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "disabled: true" {
			t.Error("octo-drive must stay enabled to be discoverable via `octo-cli skills`")
		}
	}

	// Zero-flag first example: the credential comes from the environment, exactly
	// as it does for every other domain.
	if !strings.Contains(content, "octo-cli drive space list") {
		t.Error("SKILL.md must show the zero-flag `drive space list` example")
	}
	// The credential story must name both variables and their order, and must not
	// suggest a drive-specific profile.
	for _, needle := range []string{"OCTO_TOKEN", "OCTO_BOT_TOKEN", "drive-only profile"} {
		if !strings.Contains(content, needle) {
			t.Errorf("SKILL.md must discuss %q", needle)
		}
	}
	// The three traps that silently corrupt data if unstated.
	for _, needle := range []string{"share_url", "doc_space_id", "TOKEN_KIND_NOT_ALLOWED"} {
		if !strings.Contains(content, needle) {
			t.Errorf("SKILL.md must document %q", needle)
		}
	}
	// Every command group must appear, so the skill is a complete map of the tree.
	for _, group := range []string{
		"drive browse", "drive space", "drive member", "drive folder", "drive file",
		"drive blob", "drive upload", "drive download", "drive doc", "drive share",
		"drive invite", "drive im-transfer",
	} {
		if !strings.Contains(content, group) {
			t.Errorf("SKILL.md must cover %q", group)
		}
	}
	// The removed org commands must not be advertised.
	if strings.Contains(content, "drive org") {
		t.Error("SKILL.md must not mention `drive org`; those commands do not exist")
	}
}
