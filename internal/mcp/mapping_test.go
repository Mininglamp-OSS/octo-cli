package mcp

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestBuildMapping_EmbeddedDataIsValid exercises all six drift checks against
// the real embedded frontmatter + manifest + specs. It is the CI guard that
// fails the build if a spec op is renamed/removed, a service loses Skill
// coverage, a reference file or anchor goes missing, a disabled service and its
// skill fall out of sync, or a summary/advice overflows.
func TestBuildMapping_EmbeddedDataIsValid(t *testing.T) {
	reg := registry.MustNew()
	m, err := BuildMapping(reg)
	if err != nil {
		t.Fatalf("embedded skill navigation mapping is invalid:\n%v", err)
	}
	// Every enabled service must map to an enabled skill (check 2, positively).
	for _, svc := range reg.EnabledServices() {
		if _, ok := m.serviceToSkill[svc]; !ok {
			t.Errorf("enabled service %q has no skill mapping", svc)
		}
	}
	if m.mappingEtag == "" {
		t.Errorf("mapping etag not computed")
	}
}

func containsSubstr(problems []string, substr string) bool {
	for _, p := range problems {
		if strings.Contains(p, substr) {
			return true
		}
	}
	return false
}

func TestValidate_UnknownServiceReference(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	metas := map[string]*skillMeta{"octo-x": {Name: "octo-x", Services: []string{"not-a-service"}}}
	problems := m.validate(reg, &manifestFile{}, metas)
	if !containsSubstr(problems, `"not-a-service" which is not a registry service`) {
		t.Errorf("expected unknown-service problem, got %v", problems)
	}
}

func TestValidate_ManifestOpMustExistAndBeEnabled(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	metas := map[string]*skillMeta{"octo-messaging": {Name: "octo-messaging"}}
	mf := &manifestFile{Skills: map[string]manifestSkill{
		"octo-messaging": {Operations: map[string]manifestOp{"message.nope": {}}},
	}}
	problems := m.validate(reg, mf, metas)
	if !containsSubstr(problems, `references operation "message.nope"`) {
		t.Errorf("expected unknown-op problem, got %v", problems)
	}
}

func TestValidate_EnabledServiceMustHaveCoverage(t *testing.T) {
	reg := registry.MustNew()
	// Empty serviceToSkill: every enabled service is uncovered.
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	problems := m.validate(reg, &manifestFile{}, map[string]*skillMeta{})
	if !containsSubstr(problems, "has no Skill navigation") {
		t.Errorf("expected uncovered-service problem, got %v", problems)
	}
}

func TestValidate_ReferenceFileMustExist(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	metas := map[string]*skillMeta{"octo-docs": {Name: "octo-docs"}}
	mf := &manifestFile{Skills: map[string]manifestSkill{
		"octo-docs": {References: map[string]manifestReference{"ghost.md": {Topics: []string{"x"}}}},
	}}
	problems := m.validate(reg, mf, metas)
	if !containsSubstr(problems, `reference file "ghost.md" which is not embedded`) {
		t.Errorf("expected missing-reference problem, got %v", problems)
	}
}

func TestValidate_SectionAnchorMustExist(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	metas := map[string]*skillMeta{"octo-mail": {Name: "octo-mail"}}
	mf := &manifestFile{Skills: map[string]manifestSkill{
		"octo-mail": {Operations: map[string]manifestOp{
			"mail.message.send_intent": {Section: "SKILL.md#no-such-heading"},
		}},
	}}
	problems := m.validate(reg, mf, metas)
	if !containsSubstr(problems, "anchor \"no-such-heading\" is not a heading") {
		t.Errorf("expected bad-anchor problem, got %v", problems)
	}
}

func TestValidate_DisabledServiceSkillSync(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{serviceToSkill: map[string]string{}, skills: map[string]*skillMeta{}, ops: map[string]*opMeta{}}
	// matter is a disabled service; a skill claiming it must itself be disabled.
	metas := map[string]*skillMeta{"octo-x": {Name: "octo-x", Services: []string{"matter"}, Disabled: false}}
	problems := m.validate(reg, &manifestFile{}, metas)
	if !containsSubstr(problems, `"matter" is disabled but skill "octo-x" claiming it is enabled`) {
		t.Errorf("expected disabled-sync problem, got %v", problems)
	}
}

func TestValidate_LengthAndLevelCaps(t *testing.T) {
	reg := registry.MustNew()
	m := &Mapping{
		serviceToSkill: map[string]string{},
		skills:         map[string]*skillMeta{},
		ops:            map[string]*opMeta{"x.y": {Level: "bogus", Advice: strings.Repeat("a", maxAdvice+1)}},
	}
	metas := map[string]*skillMeta{"octo-x": {Name: "octo-x", SummaryShort: strings.Repeat("s", maxSummaryShort+1)}}
	problems := m.validate(reg, &manifestFile{}, metas)
	if !containsSubstr(problems, "summary_short exceeds") {
		t.Errorf("expected summary length problem, got %v", problems)
	}
	if !containsSubstr(problems, "advice exceeds") {
		t.Errorf("expected advice length problem, got %v", problems)
	}
	if !containsSubstr(problems, `invalid level "bogus"`) {
		t.Errorf("expected invalid-level problem, got %v", problems)
	}
}

func TestParseFrontmatterList(t *testing.T) {
	md := []byte("---\nname: octo-messaging\nservices: [\"message\", \"group\", \"thread\", \"event\"]\ndescription: x\n---\nbody\n")
	got := parseFrontmatterList(md, "services:")
	want := []string{"message", "group", "thread", "event"}
	if len(got) != len(want) {
		t.Fatalf("services = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("services[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// A key that lives outside the frontmatter fence must not be read.
	if v := parseFrontmatterScalar([]byte("---\nname: a\n---\nversion: 9\n"), "version:"); v != "" {
		t.Errorf("value outside frontmatter must be ignored, got %q", v)
	}
}

func TestSlugifyAndAnchor(t *testing.T) {
	if got := slugify("Compose and send"); got != "compose-and-send" {
		t.Errorf("slugify = %q, want compose-and-send", got)
	}
	// A raw markdown heading with the leading "## " must not yield a leading dash.
	if got := slugify(strings.TrimLeft("## Compose and send", "#")); got != "compose-and-send" {
		t.Errorf("slugify of stripped heading = %q, want compose-and-send", got)
	}
	content := []byte("# Title\n\n## Compose and send\ntext\n")
	if !headingAnchorExists(content, "compose-and-send") {
		t.Errorf("anchor compose-and-send should exist")
	}
	if headingAnchorExists(content, "missing") {
		t.Errorf("anchor missing should not exist")
	}
}
