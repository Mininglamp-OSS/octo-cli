package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func TestLoopProjectSchemaDiscovery(t *testing.T) {
	for _, args := range [][]string{{"schema", "--list", "loop"}, {"schema", "project.create"}, {"schema", "project.resource.create"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newTestFactoryWithReg()
			f.SetConfig(&config.Config{Format: "json"})
			out, _, err := execRoot(t, f, args...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "project.") {
				t.Fatal("schema must expose Loop projects")
			}
		})
	}
}

func TestLoopProjectCompletion(t *testing.T) {
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
	for _, name := range []string{"project", "workspace"} {
		found := false
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.SplitN(line, "\t", 2)[0] == name {
				found = true
			}
		}
		if !found {
			t.Errorf("completion missing %s: %s", name, out.String())
		}
	}
}
