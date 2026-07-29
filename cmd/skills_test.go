package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/skills"
)

func TestParseSkillDescription(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"basic", "---\nname: x\ndescription: hello world\n---\nbody", "hello world"},
		{"trims surrounding space", "---\ndescription:   spaced   \n---", "spaced"},
		{"no frontmatter", "# title\ndescription: ignored", ""},
		{"description only in body is ignored", "---\nname: x\n---\ndescription: in body", ""},
		{"empty input", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseSkillDescription([]byte(c.in)); got != c.want {
				t.Errorf("parseSkillDescription(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestLoadSkills(t *testing.T) {
	entries, err := loadSkills()
	if err != nil {
		t.Fatalf("loadSkills: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one embedded skill")
	}
	for i, s := range entries {
		if s.Name == "" || s.Description == "" || len(s.content) == 0 {
			t.Errorf("entry %d incomplete: name=%q desc=%q contentLen=%d", i, s.Name, s.Description, len(s.content))
		}
		if i > 0 && entries[i-1].Name >= s.Name {
			t.Errorf("entries not sorted ascending: %q before %q", entries[i-1].Name, s.Name)
		}
	}
	// octo-shared is the foundational skill; it must always be embedded.
	if !hasSkill(entries, "octo-shared") {
		names := make([]string, len(entries))
		for i, s := range entries {
			names[i] = s.Name
		}
		t.Errorf("octo-shared not found among %v", names)
	}
}

func hasSkill(entries []skillEntry, name string) bool {
	for _, s := range entries {
		if s.Name == name {
			return true
		}
	}
	return false
}

// octo-matter sets `disabled: true` in its frontmatter — it must drop out of
// the listing while staying embedded (so re-enabling is just a flag flip).
func TestLoadSkillsExcludesDisabled(t *testing.T) {
	entries, err := loadSkills()
	if err != nil {
		t.Fatalf("loadSkills: %v", err)
	}
	if hasSkill(entries, "octo-matter") {
		t.Error("disabled skill `octo-matter` must not be listed")
	}
	if hasSkill(entries, "octo-summary") {
		t.Error("disabled skill `octo-summary` must not be listed (see CHANGELOG withhold note)")
	}
	if _, err := skills.FS.ReadFile("octo-matter/SKILL.md"); err != nil {
		t.Errorf("octo-matter/SKILL.md must stay embedded (only the listing filters it): %v", err)
	}
	if _, err := skills.FS.ReadFile("octo-summary/SKILL.md"); err != nil {
		t.Errorf("octo-summary/SKILL.md must stay embedded (only the listing filters it): %v", err)
	}
}

// A config without a token must still let `octo-cli skills` run — the command is
// offline and on the skipValidation list.
func TestCmd_SkillsList(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills")
	if err != nil {
		t.Fatalf("skills: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v", env["ok"])
	}
	data, _ := env["data"].(map[string]any)
	list, _ := data["skills"].([]any)
	if len(list) == 0 {
		t.Errorf("expected a non-empty skills list, got %v", data)
	}
}

func TestCmd_SkillsShow(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills", "octo-shared")
	if err != nil {
		t.Fatalf("skills octo-shared: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	if data["name"] != "octo-shared" {
		t.Errorf("name = %v", data["name"])
	}
	if content, _ := data["content"].(string); !strings.Contains(content, "octo-shared") {
		t.Errorf("content missing skill body: %.80q", content)
	}
}

// A split skill (octo-docs) must reprint its whole set through `skills <name>`:
// SKILL.md in `content` plus every progressive-disclosure reference under
// `references`. This guards the bug where only SKILL.md was returned, dropping
// most of the skill's command docs after the split.
func TestCmd_SkillsShowIncludesReferences(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills", "octo-docs")
	if err != nil {
		t.Fatalf("skills octo-docs: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	refs, ok := data["references"].(map[string]any)
	if !ok || len(refs) == 0 {
		t.Fatalf("expected non-empty references for split skill octo-docs, got %v", data["references"])
	}
	for _, want := range []string{"sheet.md", "doc.md", "board.md", "common.md"} {
		body, present := refs[want].(string)
		if !present || strings.TrimSpace(body) == "" {
			t.Errorf("reference %q missing or empty in skills octo-docs output", want)
		}
	}
}

func TestCmd_SkillsUnknown(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "no-such-skill")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("error envelope missing validation type: %s", errOut)
	}
	if !strings.Contains(errOut, "no-such-skill") {
		t.Errorf("error should name the unknown skill: %s", errOut)
	}
}

func TestCmd_SkillsInstall(t *testing.T) {
	dir := t.TempDir()
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	out, _, err := execRoot(t, f, "skills", "--install", dir)
	if err != nil {
		t.Fatalf("skills --install: %v", err)
	}
	var env map[string]any
	if jerr := json.Unmarshal([]byte(out), &env); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, out)
	}
	data, _ := env["data"].(map[string]any)
	installed, _ := data["installed"].([]any)
	if len(installed) == 0 {
		t.Fatalf("nothing installed: %v", data)
	}
	for _, p := range installed {
		ps, _ := p.(string)
		fi, statErr := os.Stat(ps)
		if statErr != nil || fi.Size() == 0 {
			t.Errorf("installed file missing/empty: %s (%v)", ps, statErr)
		}
	}
	// Verify the expected on-disk layout.
	if _, statErr := os.Stat(filepath.Join(dir, "octo-shared", "SKILL.md")); statErr != nil {
		t.Errorf("expected octo-shared/SKILL.md under install dir: %v", statErr)
	}
	// A split skill must lay down its reference files too, not just SKILL.md.
	for _, ref := range []string{"SKILL.md", "sheet.md", "doc.md", "board.md", "common.md"} {
		if _, statErr := os.Stat(filepath.Join(dir, "octo-docs", ref)); statErr != nil {
			t.Errorf("expected octo-docs/%s under install dir: %v", ref, statErr)
		}
	}
}

func TestCmd_SkillsInstallEmptyDir(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "--install", "")
	if err == nil {
		t.Fatal("expected error when --install is given an empty directory")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("expected a validation error, got: %s", errOut)
	}
}

func TestCmd_SkillsInstallRejectsName(t *testing.T) {
	dir := t.TempDir()
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json"})

	_, errOut, err := execRoot(t, f, "skills", "octo-shared", "--install", dir)
	if err == nil {
		t.Fatal("expected error when combining a skill name with --install")
	}
	if !strings.Contains(errOut, "validation") {
		t.Errorf("expected a validation error, got: %s", errOut)
	}
}
