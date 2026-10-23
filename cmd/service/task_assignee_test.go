package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const assignmentExpertID = "22222222-2222-4222-8222-222222222222"
const assignmentTeamID = "33333333-3333-4333-8333-333333333333"

func assignmentFixture(path string) string {
	switch {
	case strings.HasSuffix(path, "/members"):
		return `{"items":[{"uid":"Human/CaseSensitive","name":"Alice","robot":0},{"uid":"bot-uid","name":"Bot","robot":1}],"total":2,"limit":100,"offset":0}`
	case strings.HasSuffix(path, "/experts"):
		return `{"data":[{"expert_id":"` + assignmentExpertID + `","name":"Engineer"}],"pagination":{"total":1,"page":1,"page_size":100}}`
	case strings.HasSuffix(path, "/expert_teams"):
		return `{"data":[{"expert_team_id":"` + assignmentTeamID + `","name":"Research"}],"pagination":{"total":1,"page":1,"page_size":100}}`
	default:
		return ""
	}
}

func TestTaskAssignmentMulticaStyle(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		args         []string
		body         map[string]any
	}{
		{"assign member by name", "PATCH", []string{"assign", "TASK-1", "--to", "@alice"}, map[string]any{"assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"assign expert by ID", "PATCH", []string{"assign", "TASK-1", "--to-id", assignmentExpertID}, map[string]any{"assignee_type": "expert", "assignee_id": assignmentExpertID}},
		{"assign team by partial name", "PATCH", []string{"assign", "TASK-1", "--to", "Resear"}, map[string]any{"assignee_type": "expert_team", "assignee_id": assignmentTeamID}},
		{"create by name", "POST", []string{"create", "--title", "Example", "--assignee", "Alice"}, map[string]any{"title": "Example", "assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"update by name", "PATCH", []string{"update", "TASK-1", "--assignee", "Research"}, map[string]any{"assignee_type": "expert_team", "assignee_id": assignmentTeamID}},
		{"update untyped ID", "PATCH", []string{"update", "TASK-1", "--assignee-id", "Human/CaseSensitive"}, map[string]any{"assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"create untyped ID", "POST", []string{"create", "--title", "Example", "--assignee-id", assignmentExpertID}, map[string]any{"title": "Example", "assignee_type": "expert", "assignee_id": assignmentExpertID}},
		{"unassign", "PATCH", []string{"assign", "TASK-1", "--unassign"}, map[string]any{"assignee_type": nil, "assignee_id": nil}},
		{"clear with raw data", "PATCH", []string{"update", "TASK-1", "--data", `{"assignee_type":null,"assignee_id":null}`}, map[string]any{"assignee_type": nil, "assignee_id": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writes, reads := 0, 0
			root, tf := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Workspace-ID") != projectMemberTestID || r.Header.Get("Authorization") != "Bearer bf_assignment" {
					t.Errorf("request lost workspace or credential: %s", r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					reads++
					fixture := assignmentFixture(r.URL.Path)
					if fixture == "" {
						t.Errorf("unexpected lookup: %s", r.URL)
					}
					_, _ = io.WriteString(w, fixture)
					return
				}
				writes++
				if r.Method != tc.method || !strings.HasPrefix(r.URL.Path, "/fleet/api/v1/tasks") {
					t.Errorf("write = %s %s", r.Method, r.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(body, tc.body) {
					t.Errorf("body = %#v, want %#v", body, tc.body)
				}
				_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			})
			args := append([]string{"loop", "task"}, tc.args...)
			root.SetArgs(append(args, "--workspace-id", projectMemberTestID))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v; %s", err, tf.ErrOut.String())
			}
			if writes != 1 || ((tc.name == "unassign" || tc.name == "clear with raw data") && reads != 0) {
				t.Errorf("reads=%d writes=%d", reads, writes)
			}
		})
	}
}

func TestTaskAssignmentListFilters(t *testing.T) {
	for _, flags := range [][]string{{"--assignee", "Alice"}, {"--assignee-id", "Human/CaseSensitive"}} {
		root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/fleet/api/v1/tasks" {
				if r.URL.Query().Get("assignee_octo_uid") != "Human/CaseSensitive" || r.URL.Query().Has("assignee_id") {
					t.Errorf("incorrect member filter: %s", r.URL)
				}
				_, _ = io.WriteString(w, `{"data":[],"pagination":{"total":0}}`)
				return
			}
			_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
		})
		args := []string{"loop", "task", "list", "--workspace-id", projectMemberTestID}
		root.SetArgs(append(args, flags...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTaskAssignmentRejectsConflictsBeforeHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"assign", "TASK-1"},
		{"assign", "TASK-1", "--to", "Alice", "--to-id", assignmentExpertID},
		{"assign", "TASK-1", "--to", "Alice", "--unassign"},
		{"assign", "TASK-1", "--unassign=false"},
		{"update", "TASK-1", "--assignee", "Alice", "--assignee-id", assignmentExpertID},
		{"update", "TASK-1", "--assignee", "Alice", "--data", `{"assignee_type":"member","assignee_octo_uid":"somebody"}`},
		{"update", "TASK-1", "--assignee", ""},
		{"list", "--assignee", "Alice", "--assignee-octo-uids", "other"},
	} {
		root, _ := rootWithDriveToken(t, "bf_assignment", func(http.ResponseWriter, *http.Request) { t.Error("invalid assignment sent HTTP") })
		root.SetArgs(append(append([]string{"loop", "task"}, args...), "--workspace-id", projectMemberTestID))
		if err := root.Execute(); err == nil {
			t.Errorf("invalid selection accepted: %v", args)
		}
	}
}

func TestTaskAssignmentRefusesAmbiguityAndIncompleteLookups(t *testing.T) {
	for _, scenario := range []string{"duplicate name", "expert lookup denied", "bot member", "case-sensitive UID"} {
		t.Run(scenario, func(t *testing.T) {
			root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("unsafe lookup must not write")
				}
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/experts") && scenario == "expert lookup denied" {
					w.WriteHeader(http.StatusForbidden)
					_, _ = io.WriteString(w, `{"error":{"code":"PROJECT_READ_DENIED","message":"denied"}}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/expert_teams") && scenario == "duplicate name" {
					_, _ = io.WriteString(w, `{"data":[{"expert_team_id":"`+assignmentTeamID+`","name":"Alice"}],"pagination":{"total":1}}`)
					return
				}
				_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
			})
			flags := []string{"--to", "Alice"}
			switch scenario {
			case "bot member":
				flags = []string{"--to", "Bot"}
			case "case-sensitive UID":
				flags = []string{"--to-id", "human/casesensitive"}
			}
			root.SetArgs(append([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID}, flags...))
			if err := root.Execute(); err == nil {
				t.Fatal("unsafe assignment was accepted")
			} else if scenario == "expert lookup denied" && output.AsExitError(err).Code != "PROJECT_READ_DENIED" {
				t.Fatalf("lost upstream denial: %v", err)
			}
		})
	}
}

func TestTaskAssignmentReadsEveryMemberPage(t *testing.T) {
	memberReads, writes := 0, 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PATCH" {
			writes++
			_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/members") {
			memberReads++
			if r.URL.Query().Get("offset") == "0" {
				_, _ = io.WriteString(w, `{"items":[{"uid":"first","name":"First","robot":0}],"total":2,"limit":1,"offset":0}`)
			} else {
				_, _ = io.WriteString(w, `{"items":[{"uid":"last","name":"Last","robot":0}],"total":2,"limit":1,"offset":1}`)
			}
			return
		}
		_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Last"})
	if err := root.Execute(); err != nil || memberReads != 2 || writes != 1 {
		t.Fatalf("err=%v member reads=%d writes=%d", err, memberReads, writes)
	}
}

func TestTaskAssignmentKeepsTypedFlagsAndDryRunOffline(t *testing.T) {
	root, tf := rootWithDriveDryRun(t, "bf_assignment")
	tf.Globals.DryRun = true
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--unassign"})
	if err := root.Execute(); err != nil || !strings.Contains(tf.Out.String(), `"assignee_type": null`) {
		t.Fatalf("clear dry run: %v %s", err, tf.Out.String())
	}
	for _, flags := range [][]string{
		{"--assignee-type", "expert", "--assignee-id", assignmentExpertID},
		{"--assignee-type", "member", "--assignee-octo-uid", "Human/CaseSensitive"},
	} {
		root, _ := rootWithDriveDryRun(t, "bf_assignment")
		root.SetArgs(append([]string{"loop", "task", "update", "TASK-1", "--workspace-id", projectMemberTestID}, flags...))
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	root, tf = rootWithDriveDryRun(t, "bf_assignment")
	tf.Globals.DryRun = true
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Alice"})
	if err := root.Execute(); err == nil || !strings.Contains(fmt.Sprint(err), "dry-run") {
		t.Fatalf("name dry run must explain offline resolution: %v", err)
	}
}

func TestTaskAssignmentLegacyMemberUsesUserID(t *testing.T) {
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PATCH" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["assignee_id"] != assignmentExpertID || body["assignee_type"] != "member" || body["assignee_octo_uid"] != nil {
				t.Errorf("native member must use user UUID, not membership UUID: %#v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/members") {
			_, _ = io.WriteString(w, `[{"id":"membership-id","user_id":"`+assignmentExpertID+`","name":"Native Member","principal_type":"member","octo_uid":"identity-bridge"}]`)
			return
		}
		_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Native Member"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskAssignmentExpertPaginationFindsLaterNames(t *testing.T) {
	pages, writes := 0, 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "PATCH" {
			writes++
			_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/experts") {
			pages++
			if r.URL.Query().Get("page") == "1" {
				_, _ = io.WriteString(w, `{"data":[{"expert_id":"first-expert","name":"First"}],"pagination":{"total":2}}`)
			} else {
				_, _ = io.WriteString(w, `{"data":[{"expert_id":"last-expert","name":"Last Expert"}],"pagination":{"total":2}}`)
			}
			return
		}
		_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Last Expert"})
	if err := root.Execute(); err != nil || pages != 2 || writes != 1 {
		t.Fatalf("err=%v expert pages=%d writes=%d", err, pages, writes)
	}
}

func TestTaskAssignmentRefusesBrokenPaginationAndIdentity(t *testing.T) {
	for _, fixture := range []string{
		`{"items":[{"uid":"Human/CaseSensitive","name":"Alice","robot":0}],"total":2}`,
		`{"items":[],"total":2}`,
		`{"items":[{"uid":"Human/CaseSensitive","name":"Alice"}],"total":1}`,
		`{"items":[{"uid":"Human/CaseSensitive","name":"Alice","robot":0}]}`,
		`{"data":null,"pagination":{"total":0}}`,
		`{"items":`,
	} {
		root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Error("malformed identity or incomplete list must not assign")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, fixture)
		})
		root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Alice"})
		if err := root.Execute(); err == nil {
			t.Errorf("accepted malformed list: %s", fixture)
		}
	}
}

func TestTaskAssignmentNamePriority(t *testing.T) {
	actors := []taskAssignee{
		{kind: "member", id: "uid-1", uid: "uid-1", name: "Alice"},
		{kind: "expert", id: assignmentExpertID, name: "Alice Extended"},
	}
	actor, err := chooseTaskAssignee(actors, "alice", false)
	if err != nil || actor.uid != "uid-1" {
		t.Fatalf("exact name must beat substring: %#v %v", actor, err)
	}
	actor, err = chooseTaskAssignee(actors, assignmentExpertID[:8], false)
	if err != nil || actor.id != assignmentExpertID {
		t.Fatalf("name selector supports the original short UUID: %#v %v", actor, err)
	}
	archived := taskAssigneeRecord{TeamID: assignmentTeamID, Name: "Archived", ArchivedAt: json.RawMessage(`"2026-10-23"`)}
	if _, include, err := archived.assignee("expert_team"); err != nil || include {
		t.Fatalf("archived team must be excluded: %v %v", include, err)
	}
}
