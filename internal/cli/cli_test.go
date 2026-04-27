package cli

import (
	"strings"
	"testing"
)

// --- Command tree structure ---

func TestTodoCmd_HasAllSubcommands(t *testing.T) {
	root := NewRootCmd()
	todoCmd, _, err := root.Find([]string{"todo"})
	if err != nil {
		t.Fatalf("todo command not found: %v", err)
	}

	expected := []string{
		"list", "get", "create", "update", "close", "reopen",
		"assign", "unassign", "comment", "comments", "comment-delete",
		"delete", "goal", "attachment",
	}

	subs := make(map[string]bool)
	for _, c := range todoCmd.Commands() {
		subs[c.Name()] = true
	}

	for _, name := range expected {
		if !subs[name] {
			t.Errorf("todo missing subcommand %q", name)
		}
	}
}

func TestGoalCmd_HasAllSubcommands(t *testing.T) {
	root := NewRootCmd()
	goalCmd, _, err := root.Find([]string{"todo", "goal"})
	if err != nil {
		t.Fatalf("todo goal command not found: %v", err)
	}

	expected := []string{"list", "get", "create", "archive", "update", "assign", "unassign"}

	subs := make(map[string]bool)
	for _, c := range goalCmd.Commands() {
		subs[c.Name()] = true
	}

	for _, name := range expected {
		if !subs[name] {
			t.Errorf("goal missing subcommand %q", name)
		}
	}
}

func TestAttachmentCmd_HasAllSubcommands(t *testing.T) {
	root := NewRootCmd()
	attCmd, _, err := root.Find([]string{"todo", "attachment"})
	if err != nil {
		t.Fatalf("todo attachment command not found: %v", err)
	}

	expected := []string{"list", "add", "delete"}

	subs := make(map[string]bool)
	for _, c := range attCmd.Commands() {
		subs[c.Name()] = true
	}

	for _, name := range expected {
		if !subs[name] {
			t.Errorf("attachment missing subcommand %q", name)
		}
	}
}

// --- Flag validation ---

func TestTodoCreate_RequiresTitle(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"todo", "create"})

	// Need to set env to avoid validation error on bot token
	t.Setenv("OCTO_BOT_TOKEN", "test/key")

	err := root.Execute()
	if err == nil {
		t.Error("expected error for missing --title flag")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error should mention title, got: %v", err)
	}
}

func TestGoalCreate_RequiresTitle(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"todo", "goal", "create"})

	t.Setenv("OCTO_BOT_TOKEN", "test/key")

	err := root.Execute()
	if err == nil {
		t.Error("expected error for missing --title flag")
	}
	if !strings.Contains(err.Error(), "title") {
		t.Errorf("error should mention title, got: %v", err)
	}
}

func TestTodoGet_RequiresArg(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"todo", "get"})

	t.Setenv("OCTO_BOT_TOKEN", "test/key")

	err := root.Execute()
	if err == nil {
		t.Error("expected error for missing todo-id argument")
	}
}

func TestTodoAssign_RequiresTwoArgs(t *testing.T) {
	root := NewRootCmd()
	root.SetArgs([]string{"todo", "assign", "only-one-arg"})

	t.Setenv("OCTO_BOT_TOKEN", "test/key")

	err := root.Execute()
	if err == nil {
		t.Error("expected error for missing second argument")
	}
}

// --- Version command ---

func TestVersion_OutputsVersion(t *testing.T) {
	// version prints to os.Stdout directly, so we just verify it runs without error.
	root := NewRootCmd()
	root.SetArgs([]string{"version"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("version command failed: %v", err)
	}
}

func TestVersion_RunsWithoutAuth(t *testing.T) {
	t.Setenv("OCTO_BOT_TOKEN", "")

	root := NewRootCmd()
	root.SetArgs([]string{"version"})

	err := root.Execute()
	if err != nil {
		t.Errorf("version should work without auth, got error: %v", err)
	}
}
