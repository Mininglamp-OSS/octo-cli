package cmd

import (
	"bytes"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/skills"
)

const (
	loopSkillTestParentID1 = "11111111-1111-4111-8111-111111111111"
	loopSkillTestParentID2 = "22222222-2222-4222-8222-222222222222"
)

func TestOctoLoopSkillReadExamplesExecute(t *testing.T) {
	b, err := skills.FS.ReadFile("octo-loop/SKILL.md")
	if err != nil {
		t.Fatalf("octo-loop/SKILL.md not embedded: %v", err)
	}

	covered := [3]bool{}
	for _, example := range loopSkillReadExamples(string(b)) {
		args := loopSkillExampleArgv(t, example)
		got := runLoopSkillExample(t, args)
		if got.workspace != loopQueryTestWorkspaceID {
			t.Errorf("X-Workspace-ID = %q, want %q", got.workspace, loopQueryTestWorkspaceID)
		}

		switch {
		case strings.HasPrefix(example, "octo-cli loop task search "):
			query, afterDelimiter := loopSkillSearchQuery(args)
			if got.path != "/fleet/api/v1/tasks/_search" {
				t.Errorf("search path = %q, want /fleet/api/v1/tasks/_search", got.path)
			}
			if got.query != query {
				t.Errorf("request q = %q, want shell query argument %q", got.query, query)
			}
			covered[0] = covered[0] || (!afterDelimiter && strings.Contains(query, " "))
			covered[1] = covered[1] || (afterDelimiter && strings.HasPrefix(query, "-"))
		case strings.HasPrefix(example, "octo-cli loop task children-by-parent"):
			if got.path != "/fleet/api/v1/tasks/_children_by_parent" {
				t.Errorf("children-by-parent path = %q, want /fleet/api/v1/tasks/_children_by_parent", got.path)
			}
			wantParents := loopSkillTestParentID1 + "," + loopSkillTestParentID2
			if got.parentIDs != wantParents {
				t.Errorf("parent_task_ids = %q, want %q", got.parentIDs, wantParents)
			}
			covered[2] = true
		}
	}
	if !covered[0] || !covered[1] || !covered[2] {
		t.Errorf("embedded examples did not execute all required scenarios: ordinary-space=%t leading-dash=%t parents=%t", covered[0], covered[1], covered[2])
	}
}

func loopSkillReadExamples(doc string) []string {
	var examples []string
	inBashBlock := false
	for _, line := range strings.Split(doc, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBashBlock {
			inBashBlock = strings.HasPrefix(trimmed, "```bash")
			continue
		}
		if strings.HasPrefix(trimmed, "```") {
			inBashBlock = false
			continue
		}
		if strings.HasPrefix(trimmed, "octo-cli loop task search ") ||
			strings.HasPrefix(trimmed, "octo-cli loop task children-by-parent ") {
			examples = append(examples, trimmed)
		}
	}
	return examples
}

func loopSkillExampleArgv(t *testing.T, example string) []string {
	t.Helper()
	example = strings.ReplaceAll(example, "<ws>", loopQueryTestWorkspaceID)
	example = strings.ReplaceAll(example, "<uuid-1>", loopSkillTestParentID1)
	example = strings.ReplaceAll(example, "<uuid-2>", loopSkillTestParentID2)

	script := "set -f\nocto-cli() { printf '%s\\0' \"$@\"; }\n" + example + "\n"
	output, err := exec.Command("bash", "-c", script).Output()
	if err != nil {
		t.Fatalf("parse documented Bash example %q: %v", example, err)
	}
	parts := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	args := make([]string, len(parts))
	for i := range parts {
		args[i] = string(parts[i])
	}
	return args
}

func loopSkillSearchQuery(args []string) (string, bool) {
	for i := 3; i < len(args); i++ {
		switch args[i] {
		case "--":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case "--workspace-id":
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false
			}
			return args[i], false
		}
	}
	return "", false
}

type loopSkillRequest struct {
	path, workspace, query, parentIDs string
}

func runLoopSkillExample(t *testing.T, args []string) loopSkillRequest {
	t.Helper()
	var got loopSkillRequest
	requests := 0
	factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		got.path = r.URL.Path
		got.workspace = r.Header.Get("X-Workspace-ID")
		got.query = r.URL.Query().Get("q")
		got.parentIDs = r.URL.Query().Get("parent_task_ids")
		writeLoopQueryTestCollection(w)
	})
	if _, _, err := execRoot(t, factory, args...); err != nil {
		t.Fatalf("documented read-only command failed: %v", err)
	}
	if requests != 1 {
		t.Fatalf("documented read-only command made %d requests, want 1", requests)
	}
	return got
}
