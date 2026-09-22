package skills

import (
	"strings"
	"testing"
)

func TestSheetProductivitySafetyContract(test *testing.T) {
	for path, phrases := range map[string][]string{
		"octo-docs/SKILL.md": {"conditional formatting", "safe split/deduplication"},
		"octo-docs/sheet.md": {
			"default to **server-side preview**", "explicitly sends `preview:true`", "--preview=false",
			"Never add that flag automatically after a conflict", `"conditionalFormats":{"default!duplicates":null}`,
			"never silently drop rules", "requires an admin identity", "409 sheet_conditional_format_unsupported",
			"Deploy backend !147, then !166", "never write `filters` to imitate a personal view",
		},
	} {
		content, err := FS.ReadFile(path)
		if err != nil {
			test.Fatal(err)
		}
		normalized := strings.Join(strings.Fields(string(content)), " ")
		for _, phrase := range phrases {
			if !strings.Contains(normalized, phrase) {
				test.Errorf("%s must preserve safety contract %q", path, phrase)
			}
		}
	}
}
