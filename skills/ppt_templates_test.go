package skills

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestPptGalleryTemplatesDocumentedInEmbeddedSkill(t *testing.T) {
	op, ok := registry.MustNew().GetOperation("docs.create")
	if !ok || op.RequestBody == nil {
		t.Fatal("docs.create request schema missing")
	}
	template := op.RequestBody.Properties["templateId"]
	var want []string
	for _, value := range template.Enum {
		id, ok := value.(string)
		if !ok {
			t.Fatalf("template enum is not a string: %v", value)
		}
		want = append(want, id)
	}
	slices.Sort(want)
	if len(want) == 0 {
		t.Fatal("template enum is empty")
	}
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	// Parse only advertised table rows, not prose that names retired IDs.
	row := regexp.MustCompile("(?m)^\\| `([^`]+)` \\|")
	var got []string
	for _, match := range row.FindAllStringSubmatch(string(b), -1) {
		got = append(got, match[1])
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("PPT table IDs = %v, schema enum = %v", got, want)
	}
	for _, file := range []string{"octo-docs/SKILL.md", "octo-docs/ppt.md", "../README.md"} {
		var content []byte
		if file == "../README.md" {
			content, err = os.ReadFile(file)
		} else {
			content, err = FS.ReadFile(file)
		}
		if err != nil {
			t.Fatal(err)
		}
		matches := templateExamples(string(content))
		if len(matches) == 0 {
			t.Errorf("%s has no template example", file)
		}
		for _, match := range matches {
			if !slices.Contains(want, match) {
				t.Errorf("%s example advertises unsupported template %q", file, match)
			}
		}
	}
	for _, phrase := range []string{"CLI", "pitch/report/lesson", "backend", "policy"} {
		if !strings.Contains(template.Description, phrase) {
			t.Errorf("template help must mention %q", phrase)
		}
	}
}

func templateExamples(content string) []string {
	var values []string
	// One scanner preserves ordering across shell flags and JSON examples.
	pattern := `--templateId(?:=|\s+)(?:"([^"]*)"|'([^']*)'|([^\s` + "`" + `]+))|"templateId"\s*:\s*("(?:\\.|[^"\\])*")`
	for _, match := range regexp.MustCompile(pattern).FindAllStringSubmatch(content, -1) {
		value := match[1] + match[2] + match[3]
		if match[4] != "" {
			if err := json.Unmarshal([]byte(match[4]), &value); err != nil {
				value = match[4] // Invalid JSON must not disappear from the guard.
			}
		}
		values = append(values, value)
	}
	return values
}

func TestTemplateExampleSyntax(t *testing.T) {
	for _, input := range []string{
		`--templateId pitch`, `--templateId=pitch`, `--templateId "pitch"`,
		`--templateId='pitch'`, `--templateId 'pitch'`, `--templateId="pitch"`,
		`{"templateId": "pitch"}`, `{"templateId":"p\u0069tch"}`,
	} {
		t.Run(input, func(t *testing.T) {
			got := templateExamples("--templateId blank\n" + input)
			if !slices.Equal(got, []string{"blank", "pitch"}) {
				t.Fatalf("example scanner missed unsupported template: %v", got)
			}
		})
	}
	if got := templateExamples(`--templateId=signal --templateId 'terra' {"templateId":"orbital"}`); !slices.Equal(got, []string{"signal", "terra", "orbital"}) {
		t.Fatalf("valid example syntax lost: %v", got)
	}
}

func TestPptGalleryRolloutDocumented(t *testing.T) {
	changelog, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"**BREAKING (docs):", "docs/ppt-release-gate.md", "catalogue activation", "permissionEpoch", "only `blank`"} {
		if !strings.Contains(string(changelog), phrase) {
			t.Errorf("CHANGELOG missing PPT rollout note %q", phrase)
		}
	}
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"#174", "docs/ppt-release-gate.md", "catalogue activation", "permissionEpoch", "not proof of deployment"} {
		if !strings.Contains(string(b), phrase) {
			t.Errorf("PPT skill missing policy/rollout boundary %q", phrase)
		}
	}
	gate, err := os.ReadFile("../docs/ppt-release-gate.md")
	if err != nil {
		t.Fatal(err)
	}
	if !validPptRolloutTable(string(gate)) {
		t.Error("release gate has an incomplete or stale dependency table")
	}
	for _, phrase := range []string{"## Current rollout dependencies", "Terra", "Orbital", "Player", "Catalogue activation", "Capacity", "Frontend", "permissionEpoch", "merge and deploy"} {
		if !strings.Contains(string(gate), phrase) {
			t.Errorf("release gate missing dependency %q", phrase)
		}
	}
	for name, content := range map[string]string{"CHANGELOG": string(changelog), "PPT skill": string(b), "release gate": string(gate)} {
		if regexp.MustCompile(`merge_requests/\d+`).MatchString(content) {
			t.Errorf("%s advertises an internal rollout vehicle", name)
		}
	}
}

func validPptRolloutTable(text string) bool {
	_, section, ok := strings.Cut(text, "## Current rollout dependencies\n")
	if !ok {
		return false
	}
	section, _, _ = strings.Cut(section, "\n## ")
	expected := map[string]string{
		"Terra":                "Merge and deploy before activation",
		"Orbital":              "Merge and deploy before activation",
		"Player":               "Merge and deploy before activation",
		"Catalogue activation": "Merge and deploy after prerequisites; all five choices must work",
		"Capacity":             "Deploy migration, request/proxy and MySQL packet settings before large-deck acceptance",
		"Frontend":             "Confirm the picker and rendered template effects in the actual deployed UI",
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| Component |") || strings.HasPrefix(line, "| --- |") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) != 4 || strings.TrimSpace(cells[3]) != "" {
			return false
		}
		name := strings.TrimSpace(cells[1])
		want, found := expected[name]
		if !found || seen[name] || strings.TrimSpace(cells[2]) != want {
			return false
		}
		seen[name] = true
	}
	return len(seen) == len(expected)
}

func publicPptRolloutTable() string {
	return `## Current rollout dependencies

| Component | Release condition |
| --- | --- |
| Terra | Merge and deploy before activation |
| Orbital | Merge and deploy before activation |
| Player | Merge and deploy before activation |
| Catalogue activation | Merge and deploy after prerequisites; all five choices must work |
| Capacity | Deploy migration, request/proxy and MySQL packet settings before large-deck acceptance |
| Frontend | Confirm the picker and rendered template effects in the actual deployed UI |
`
}

func TestPptRolloutGuardAcceptsPublicContract(t *testing.T) {
	if !validPptRolloutTable(publicPptRolloutTable()) {
		t.Fatal("public component/condition table must not require private implementation links")
	}
}

func TestPptReleaseGuidanceContainsOnlyPublicReferences(t *testing.T) {
	// This release guide needs no external links or implementation SHAs.
	// Keep deployment provenance in access-controlled operational evidence.
	for _, file := range []string{"../docs/ppt-release-gate.md", "../cmd/service/enum_vocab_test.go", "ppt_templates_test.go"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, pattern := range []string{`https?://[^\s)]+/-/(tree|blob|merge_requests)/`, `\b[a-f0-9]{40}\b`} {
			if regexp.MustCompile(pattern).Match(b) {
				t.Errorf("%s exposes a private implementation reference", file)
			}
		}
	}
}

func TestPptRolloutGuardRejectsInvalidRows(t *testing.T) {
	b, err := os.ReadFile("../docs/ppt-release-gate.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	if !validPptRolloutTable(text) {
		t.Fatal("valid current dependency table rejected")
	}
	for name, changed := range map[string]string{
		"missing rows with prose retained": regexp.MustCompile(`(?m)^\| (Terra|Orbital|Player|Catalogue activation|Capacity|Frontend) \|.*\n`).ReplaceAllString(text, ""),
		"duplicate component":              strings.Replace(text, "| Orbital |", "| Terra |", 1),
		"unknown component":                strings.Replace(text, "| Terra |", "| Unknown |", 1),
		"extra source column":              strings.Replace(text, "| Terra |", "| Terra | https://example.invalid/source |", 1),
		"malformed row":                    strings.Replace(text, "| Terra |", "| Terra", 1),
		"premature activation":             strings.Replace(text, "Merge and deploy after prerequisites; all five choices must work", "Ready to deploy now", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if validPptRolloutTable(changed) {
				t.Fatal("rollout guard accepted a missing or stale dependency row")
			}
		})
	}
}
