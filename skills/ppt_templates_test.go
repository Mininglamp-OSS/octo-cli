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
	for _, phrase := range []string{"**BREAKING (docs):", "merge_requests/146", "merge_requests/105", "only `blank`"} {
		if !strings.Contains(string(changelog), phrase) {
			t.Errorf("CHANGELOG missing PPT rollout note %q", phrase)
		}
	}
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"#174", "merge_requests/146", "merge_requests/105", "not proof of deployment"} {
		if !strings.Contains(string(b), phrase) {
			t.Errorf("PPT skill missing policy/rollout boundary %q", phrase)
		}
	}
}
