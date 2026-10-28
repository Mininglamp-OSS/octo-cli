package service

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const (
	loopProjectID  = "11111111-1111-4111-8111-111111111111"
	loopProjectID2 = "22222222-2222-4222-8222-222222222222"
	loopResourceID = "33333333-3333-4333-8333-333333333333"
)

func TestLoopProjectRequests(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, body string
		args                     []string
	}{
		{"list", "GET", "/projects", "", []string{"list"}},
		{"get", "GET", "/projects/" + loopProjectID, "", []string{"get", loopProjectID}},
		{"create", "POST", "/projects", `{"title":"Demo","status":"planned","priority":"high"}`, []string{"create", "--title", "Demo", "--status", "planned", "--priority", "high"}},
		{"update", "PATCH", "/projects/" + loopProjectID, `{"description":"New text"}`, []string{"update", loopProjectID, "--description", "New text"}},
		{"clear lead", "PATCH", "/projects/" + loopProjectID, `{"lead_type":null,"lead_id":null}`, []string{"update", loopProjectID, "--data", `{"lead_type":null,"lead_id":null}`}},
		{"member lead", "POST", "/projects", `{"title":"Members","lead_type":"member","lead_octo_uid":"user-uid"}`, []string{"create", "--title", "Members", "--lead-type", "member", "--lead-member-id", "user-uid"}},
		{"member lead compatibility alias", "POST", "/projects", `{"title":"Members","lead_type":"member","lead_octo_uid":"user-uid"}`, []string{"create", "--title", "Members", "--lead-type", "member", "--lead-octo-uid", "user-uid"}},
		{"update member lead", "PATCH", "/projects/" + loopProjectID, `{"lead_type":"member","lead_octo_uid":"Case-Sensitive-ID"}`, []string{"update", loopProjectID, "--lead-type", "member", "--lead-member-id", "Case-Sensitive-ID"}},
		{"atomic resources", "POST", "/projects", `{"title":"Resources","resources":[{"resource_type":"github_repo","resource_ref":{"url":"https://github.com/org/repo"}}]}`, []string{"create", "--data", `{"title":"Resources","resources":[{"resource_type":"github_repo","resource_ref":{"url":"https://github.com/org/repo"}}]}`}},
		{"delete", "DELETE", "/projects/" + loopProjectID, "", []string{"delete", loopProjectID}},
		{"resources", "GET", "/projects/" + loopProjectID + "/resources", "", []string{"resource", "list", loopProjectID}},
		{"resource create", "POST", "/projects/" + loopProjectID + "/resources", `{"resource_type":"github_repo","resource_ref":{"url":"https://github.com/org/repo"},"label":"Code"}`, []string{"resource", "create", loopProjectID, "--type", "github_repo", "--ref", `{"url":"https://github.com/org/repo"}`, "--label", "Code"}},
		{"resource add alias", "POST", "/projects/" + loopProjectID + "/resources", `{"resource_type":"local_directory","resource_ref":{"local_path":"/work/repo","daemon_id":"daemon-1"}}`, []string{"resource", "add", loopProjectID, "--data", `{"resource_type":"local_directory","resource_ref":{"local_path":"/work/repo","daemon_id":"daemon-1"}}`}},
		{"resource update", "PATCH", "/projects/" + loopProjectID + "/resources/" + loopResourceID, `{"position":2,"label":null}`, []string{"resource", "update", loopProjectID, loopResourceID, "--position", "2", "--data", `{"label":null}`}},
		{"resource delete", "DELETE", "/projects/" + loopProjectID + "/resources/" + loopResourceID, "", []string{"resource", "delete", loopProjectID, loopResourceID}},
		{"resource remove alias", "DELETE", "/projects/" + loopProjectID + "/resources/" + loopResourceID, "", []string{"resource", "remove", loopProjectID, loopResourceID}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tt.method || r.URL.Path != "/fleet/api/v1"+tt.path || r.Header.Get("X-Workspace-ID") != "workspace-1" {
					t.Errorf("unexpected request: %s %s workspace=%q", r.Method, r.URL, r.Header.Get("X-Workspace-ID"))
				}
				if r.Header.Get("Accept") != "application/vnd.octo+json" {
					t.Error("public API negotiation missing")
				}
				if tt.body != "" {
					var got, want map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if err := json.Unmarshal([]byte(tt.body), &want); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("body=%v want=%v", got, want)
					}
				}
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			args := append([]string{"loop", "project"}, tt.args...)
			root.SetArgs(append(args, "--workspace-id", "workspace-1"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d want 1", requests)
			}
		})
	}
}

func TestLoopProjectLeadHelpHidesWireTerminology(t *testing.T) {
	for _, verb := range []string{"create", "update"} {
		t.Run(verb, func(t *testing.T) {
			root, _ := rootWithDriveDryRun(t, "bf_project")
			var helpOutput strings.Builder
			root.SetOut(&helpOutput)
			root.SetArgs([]string{"loop", "project", verb, "--help"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := strings.ToLower(helpOutput.String())
			if strings.Contains(help, "octo uid") || strings.Contains(help, "octo-uid") || strings.Contains(help, "octo_uid") {
				t.Errorf("help exposes backend identity terminology: %s", helpOutput.String())
			}
			if !strings.Contains(help, "--lead-member-id") {
				t.Errorf("help omits member ID flag: %s", helpOutput.String())
			}
		})
	}
}

func TestLoopProjectSearchQuery(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fleet/api/v1/projects/_search" || r.URL.Query().Get("q") != "api & 中文" || r.URL.Query().Get("include_closed") != "true" {
			t.Errorf("unexpected search: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0}}`))
	})
	root.SetArgs([]string{"loop", "project", "search", "api & 中文", "--workspace-id", "workspace-1", "--include-closed"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestLoopProjectAssociationRequests(t *testing.T) {
	for _, args := range [][]string{
		{"task", "create", "--title", "Demo", "--project-id", loopProjectID},
		{"task", "update", "TES-86", "--project", loopProjectID},
		{"task", "quick-create", "--prompt", "Demo", "--expert-id", loopResourceID, "--project-id", loopProjectID},
		{"autopilot", "create", "--title", "Daily", "--assignee-id", loopResourceID, "--dispatch-mode", "create_task", "--project", loopProjectID},
		{"autopilot", "update", "autopilot-1", "--project-id", loopProjectID},
		{"task", "update", "TES-86", "--data", `{"project_id":null}`},
		{"task", "update", "TES-86", "--project", ""},
		{"autopilot", "update", "autopilot-1", "--project", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				want := any(loopProjectID)
				if args[len(args)-1] == "" || strings.Contains(args[len(args)-1], "null") {
					want = nil
				}
				if got, ok := body["project_id"]; !ok || got != want {
					t.Errorf("project_id=%v (present=%v) want %v", got, ok, want)
				}
				if r.Header.Get("X-Workspace-ID") != "workspace-1" {
					t.Error("workspace missing")
				}
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			root.SetArgs(append(append([]string{"loop"}, args...), "--workspace-id", "workspace-1"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if requests != 1 {
				t.Fatalf("requests=%d want 1", requests)
			}
		})
	}
}

func TestLoopProjectTaskFilters(t *testing.T) {
	for _, command := range []string{"list", "group"} {
		for _, filters := range [][]string{
			{"--project-id", loopProjectID}, {"--project", loopProjectID},
			{"--project-ids", loopProjectID + "," + loopProjectID2, "--include-no-project"},
			{"--include-no-project"},
		} {
			t.Run(command+strings.Join(filters, " "), func(t *testing.T) {
				root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					if filters[0] == "--project" || filters[0] == "--project-id" {
						if q.Get("project_id") != loopProjectID {
							t.Errorf("query=%v", q)
						}
					} else if q.Get("include_no_project") != "true" {
						t.Errorf("query=%v", q)
					}
					if filters[0] == "--project-ids" && q.Get("project_ids") != loopProjectID+","+loopProjectID2 {
						t.Errorf("query=%v", q)
					}
					_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0}}`))
				})
				root.SetArgs(append([]string{"loop", "task", command, "--workspace-id", "workspace-1"}, filters...))
				if err := root.Execute(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestLoopProjectValidationBeforeRequest(t *testing.T) {
	for _, args := range [][]string{
		{"project", "create", "--title", "Demo", "--status", "active"},
		{"project", "list", "--status", "active"},
		{"project", "create", "--title", "Demo", "--priority", "critical"},
		{"project", "create", "--title", "Demo", "--lead-type", "expert_team"},
		{"project", "create", "--title", "Demo", "--lead-id", loopResourceID, "--lead-octo-uid", "uid"},
		{"project", "resource", "create", loopProjectID, "--type", "github_repo", "--ref", "not-json"},
		{"project", "resource", "create", loopProjectID, "--type", "github_repo", "--ref", `[]`},
		{"project", "resource", "create", loopProjectID, "--type", "github_repo", "--ref", `null`},
		{"project", "resource", "create", loopProjectID, "--type", "github_repo", "--ref", `{} {}`},
		{"project", "resource", "create", loopProjectID, "--type", "github_repo", "--ref", `{}`, "--data", `{"resource_ref":{"url":"https://github.com/org/repo"}}`},
		{"task", "list", "--project-id", loopProjectID, "--project-ids", loopProjectID2},
		{"task", "list", "--project", loopProjectID, "--project-id", loopProjectID2},
		{"task", "update", "TES-86", "--project", loopProjectID, "--project-id", loopProjectID2},
		{"task", "create", "--title", "Demo", "--project", ""},
		{"autopilot", "create", "--title", "Daily", "--assignee-id", loopResourceID, "--dispatch-mode", "create_task", "--project", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, _ *http.Request) { requests++; _, _ = w.Write([]byte(`{"data":{}}`)) })
			root.SetArgs(append(append([]string{"loop"}, args...), "--workspace-id", "workspace-1"))
			if err := root.Execute(); err == nil {
				t.Error("expected validation error")
			}
			if requests != 0 {
				t.Errorf("invalid input made %d requests", requests)
			}
		})
	}
}

func TestLoopProjectRequiresWorkspace(t *testing.T) {
	for _, args := range [][]string{
		{"project", "list"},
		{"project", "create", "--title", "Demo"},
		{"project", "get", loopProjectID},
		{"project", "resource", "list", loopProjectID},
		{"task", "list", "--project-id", loopProjectID},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			root.SetArgs(append([]string{"loop"}, args...))
			if err := root.Execute(); err == nil {
				t.Error("a project selector must not replace --workspace-id")
			}
			if requests != 0 {
				t.Errorf("missing workspace made %d requests", requests)
			}
		})
	}
}
