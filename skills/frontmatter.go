package skills

import "strings"

// DisabledFromFrontmatter is shared by the CLI listing and MCP resources.
// A leading unclosed frontmatter block is withheld. A late fenced block that
// explicitly says disabled: true is also withheld, while ordinary Markdown
// rules and enabled late blocks are not mistaken for disabled frontmatter.
func DisabledFromFrontmatter(content []byte) bool {
	rows := strings.Split(strings.TrimPrefix(string(content), "\ufeff"), "\n")
	first := 0
	for first < len(rows) && strings.TrimSpace(rows[first]) == "" {
		first++
	}
	leading := first < len(rows) && strings.TrimSpace(rows[first]) == "---"
	for i := first; i < len(rows); i++ {
		if strings.TrimSpace(rows[i]) != "---" {
			continue
		}
		disabled := false
		closed := false
		for j := i + 1; j < len(rows); j++ {
			trimmed := strings.TrimSpace(rows[j])
			if trimmed == "---" {
				closed, i = true, j
				break
			}
			if strings.HasPrefix(trimmed, "disabled:") {
				value := strings.TrimSpace(strings.SplitN(strings.TrimSpace(strings.TrimPrefix(trimmed, "disabled:")), "#", 2)[0])
				if strings.EqualFold(strings.Trim(value, `"'`), "true") {
					disabled = true
				}
			}
		}
		if disabled || (leading && !closed) {
			return true
		}
		if leading {
			return false
		}
		if !closed {
			return false // thematic break or setext underline, no disabled declaration
		}
	}
	return false
}
