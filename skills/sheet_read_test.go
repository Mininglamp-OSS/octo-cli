package skills

import (
	"strings"
	"testing"
)

func TestEmbeddedWorksheetDiscoveryInstructions(test *testing.T) {
	content, err := FS.ReadFile("octo-docs/sheet.md")
	if err != nil {
		test.Fatal(err)
	}
	for _, phrase := range []string{
		"docs sheet list <docId>", "--sheet-id", "--sheet-name", "ambiguous_sheet_name",
		"ask the user", "SHEET_READ_SCOPE_UNAVAILABLE", "Do not decode, construct or modify cursors",
	} {
		if !strings.Contains(string(content), phrase) {
			test.Errorf("embedded worksheet instructions missing %q", phrase)
		}
	}
}
