package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLegacyProjectCommandsRejectWithoutRequests(t *testing.T) {
	for _, command := range []string{
		"list", "search", "get", "create", "update", "delete",
		"resource list", "resource create", "resource update", "resource delete",
	} {
		t.Run(command, func(t *testing.T) {
			root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
				t.Error("retired command must not send any request")
			})
			if findCmd(findCmd(root, "loop"), "project") != nil {
				t.Error("retired Project subtree remains in help/completion")
			}
			root.SetArgs(append([]string{"loop", "project"}, strings.Fields(command)...))
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), `unknown subcommand "project"`) {
				t.Errorf("error = %v, want unknown project subcommand", err)
			}
		})
	}
}

func TestLegacyProjectFlagsRemoved(t *testing.T) {
	for _, args := range [][]string{
		{"task", "quick-create"},
		{"autopilot", "create"},
		{"autopilot", "update", "autopilot-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
				t.Error("retired flag must not send any request")
			})
			cmd := findCmd(findCmd(findCmd(root, "loop"), args[0]), args[1])
			if cmd == nil {
				t.Fatal("retained command is missing")
			}
			if cmd.Flags().Lookup("project-id") != nil {
				t.Error("retired --project-id flag still advertised")
			}
			argv := append([]string{"loop"}, args...)
			root.SetArgs(append(argv, "--workspace-id", "workspace-1", "--project-id", "legacy-project"))
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: --project-id") {
				t.Errorf("error = %v, want unknown project-id flag", err)
			}
		})
	}
}

func TestLegacyProjectRemovalPreservesTaskAndAutopilotWrites(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, body string
		args                     []string
	}{
		{"quick create", "POST", "/fleet/api/v1/tasks/_quick_create", `{"prompt":"fix the test","expert_id":"expert-1"}`, []string{"task", "quick-create"}},
		{"autopilot create", "POST", "/fleet/api/v1/autopilots", `{"title":"Daily check","assignee_id":"expert-1","dispatch_mode":"create_task"}`, []string{"autopilot", "create"}},
		{"autopilot update", "PATCH", "/fleet/api/v1/autopilots/autopilot-1", `{"title":"Updated check"}`, []string{"autopilot", "update", "autopilot-1"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tt.method || r.URL.Path != tt.path || r.Header.Get("X-Workspace-ID") != "workspace-1" {
					t.Errorf("unexpected request: %s %s, workspace=%q", r.Method, r.URL, r.Header.Get("X-Workspace-ID"))
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if _, exists := body["project_id"]; exists {
					t.Error("request must not inject a legacy project_id")
				}
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			argv := append([]string{"loop"}, tt.args...)
			root.SetArgs(append(argv, "--workspace-id", "workspace-1", "--data", tt.body))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Errorf("requests = %d, want 1", requests)
			}
		})
	}
}
