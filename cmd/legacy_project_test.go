package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func TestLegacyProjectSchemaDiscovery(t *testing.T) {
	for _, args := range [][]string{{"schema", "--list"}, {"schema", "--list", "loop"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newTestFactoryWithReg()
			f.SetConfig(&config.Config{Format: "json"})
			out, _, err := execRoot(t, f, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, `"project.`) || strings.Contains(out, "/fleet/api/v1/projects") {
				t.Error("schema discovery still exposes legacy Project")
			}
			if !strings.Contains(out, `"workspace.list"`) {
				t.Error("schema discovery lost Workspace listing")
			}
		})
	}
	for _, id := range []string{"project.list", "project.resource.create"} {
		f := newTestFactoryWithReg()
		f.SetConfig(&config.Config{Format: "json"})
		out, errOut, err := execRoot(t, f, "schema", id)
		if err == nil || out != "" || !strings.Contains(errOut, "validation") {
			t.Errorf("schema %s: stdout=%q stderr=%q error=%v", id, out, errOut, err)
		}
	}
}

func TestLegacyProjectNotInCompletion(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: "json", BotToken: "octo_loop_test"})
	f.SetCredential(&credential.BotCredential{Token: "octo_loop_test", Source: "test"})
	root := NewRootCmd(f.Factory)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"__complete", "loop", ""})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	workspace := false
	for _, line := range strings.Split(out.String(), "\n") {
		name := strings.SplitN(line, "\t", 2)[0]
		if name == "project" {
			t.Error("completion still exposes legacy Project")
		}
		if name == "workspace" {
			workspace = true
		}
	}
	if !workspace {
		t.Fatalf("completion lost Workspace: %s", out.String())
	}
}
