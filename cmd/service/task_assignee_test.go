package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const assignmentExpertID = "22222222-2222-4222-8222-222222222222"
const assignmentTeamID = "33333333-3333-4333-8333-333333333333"

func assignmentFixture(path string) string {
	switch {
	case strings.HasSuffix(path, "/members"):
		return `[{"uid":"Human/CaseSensitive","name":"Alice","robot":0},{"uid":"bot-uid","name":"Bot","robot":1}]`
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
		{"assign member by name", "PATCH", []string{"assign", "TASK-1", "--to", "Alice"}, map[string]any{"assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"assign member by mention", "PATCH", []string{"assign", "TASK-1", "--to", "@Alice"}, map[string]any{"assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"assign expert by ID", "PATCH", []string{"assign", "TASK-1", "--to-id", assignmentExpertID}, map[string]any{"assignee_type": "expert", "assignee_id": assignmentExpertID}},
		{"assign team by partial name", "PATCH", []string{"assign", "TASK-1", "--to", "Resear"}, map[string]any{"assignee_type": "expert_team", "assignee_id": assignmentTeamID}},
		{"create by name", "POST", []string{"create", "--title", "Example", "--assignee", "Alice"}, map[string]any{"title": "Example", "assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"create by full-width mention", "POST", []string{"create", "--title", "Example", "--assignee", "＠Alice"}, map[string]any{"title": "Example", "assignee_type": "member", "assignee_octo_uid": "Human/CaseSensitive"}},
		{"update by name", "PATCH", []string{"update", "TASK-1", "--assignee", "Research"}, map[string]any{"assignee_type": "expert_team", "assignee_id": assignmentTeamID}},
		{"update by mention", "PATCH", []string{"update", "TASK-1", "--assignee", "@Research"}, map[string]any{"assignee_type": "expert_team", "assignee_id": assignmentTeamID}},
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

func TestTaskUnassignClearsProjectMemberOnReadback(t *testing.T) {
	member := map[string]any{
		"task_id":           "TASK-1",
		"assignee_type":     "member",
		"assignee_id":       "55555555-5555-4555-8555-555555555555",
		"assignee_octo_uid": "Human/CaseSensitive",
	}
	cleared := map[string]any{
		"task_id":           "TASK-1",
		"assignee_type":     nil,
		"assignee_id":       nil,
		"assignee_octo_uid": nil,
	}
	current := member
	root, tf := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fleet/api/v1/tasks/TASK-1" || r.Header.Get("X-Workspace-ID") != projectMemberTestID {
			t.Errorf("unexpected task request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.Method == http.MethodPatch {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			want := map[string]any{"assignee_type": nil, "assignee_id": nil}
			if !reflect.DeepEqual(body, want) {
				t.Errorf("unassign body = %#v, want %#v", body, want)
			}
			current = cleared
		} else if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"data": current}); err != nil {
			t.Error(err)
		}
	})
	run := func(args ...string) map[string]any {
		t.Helper()
		tf.Out.Reset()
		root.SetArgs(append([]string{"loop", "task"}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v; %s", args, err, tf.ErrOut.String())
		}
		var result struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(tf.Out.Bytes(), &result); err != nil {
			t.Fatalf("decode %v output: %v; %s", args, err, tf.Out.String())
		}
		return result.Data
	}
	if got := run("get", "TASK-1", "--workspace-id", projectMemberTestID); !reflect.DeepEqual(got, member) {
		t.Fatalf("initial Project member = %#v, want %#v", got, member)
	}
	if got := run("assign", "TASK-1", "--unassign", "--workspace-id", projectMemberTestID); !reflect.DeepEqual(got, cleared) {
		t.Fatalf("unassign response = %#v, want %#v", got, cleared)
	}
	if got := run("get", "TASK-1", "--workspace-id", projectMemberTestID); !reflect.DeepEqual(got, cleared) {
		t.Fatalf("readback = %#v, want %#v", got, cleared)
	}
}

func TestTaskAssignmentListFilters(t *testing.T) {
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
	root.SetArgs([]string{"loop", "task", "list", "--workspace-id", projectMemberTestID, "--assignee", "@Alice"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestTaskListAssigneeIDPassesThroughWithoutRosterReads(t *testing.T) {
	taskReads := 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fleet/api/v1/tasks" {
			t.Errorf("task ID filter must not read a roster: %s", r.URL)
			http.Error(w, "roster denied", http.StatusForbidden)
			return
		}
		taskReads++
		if r.Method != http.MethodGet || r.URL.Query().Get("assignee_id") != assignmentExpertID || r.URL.Query().Has("assignee_types") {
			t.Errorf("task filter changed: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"pagination":{"total":0}}`)
	})
	root.SetArgs([]string{"loop", "task", "list", "--workspace-id", projectMemberTestID, "--assignee-id", assignmentExpertID})
	if err := root.Execute(); err != nil || taskReads != 1 {
		t.Fatalf("list by historical assignee ID: err=%v task reads=%d", err, taskReads)
	}
}

func TestTaskMemberIDFlagsKeepBackendFields(t *testing.T) {
	for _, tc := range []struct {
		name, method, field, value string
		args                       []string
	}{
		{"update member", http.MethodPatch, "assignee_octo_uid", "Human/CaseSensitive", []string{"update", "TASK-1", "--assignee-type", "member", "--assignee-member-id", "Human/CaseSensitive"}},
		{"create member", http.MethodPost, "assignee_octo_uid", "Human/CaseSensitive", []string{"create", "--title", "Example", "--assignee-type", "member", "--assignee-member-id", "Human/CaseSensitive"}},
		{"list member", http.MethodGet, "assignee_octo_uid", "Human/CaseSensitive", []string{"list", "--assignee-member-id", "Human/CaseSensitive"}},
		{"list members", http.MethodGet, "assignee_octo_uids", "Human/CaseSensitive,Second", []string{"list", "--assignee-member-ids", "Human/CaseSensitive,Second"}},
		{"list creator", http.MethodGet, "creator_octo_uid", "Human/CaseSensitive", []string{"list", "--creator-member-id", "Human/CaseSensitive"}},
		{"list creators", http.MethodGet, "creator_octo_uids", "Human/CaseSensitive,Second", []string{"list", "--creator-member-ids", "Human/CaseSensitive,Second"}},
		{"old update flag", http.MethodPatch, "assignee_octo_uid", "Human/CaseSensitive", []string{"update", "TASK-1", "--assignee-type", "member", "--assignee-octo-uid", "Human/CaseSensitive"}},
		{"old list flag", http.MethodGet, "assignee_octo_uid", "Human/CaseSensitive", []string{"list", "--assignee-octo-uid", "Human/CaseSensitive"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tc.method {
					t.Errorf("method = %s, want %s", r.Method, tc.method)
				}
				if tc.method == http.MethodGet {
					if got := r.URL.Query().Get(tc.field); got != tc.value {
						t.Errorf("%s = %q, want %q", tc.field, got, tc.value)
					}
					_, _ = io.WriteString(w, `{"data":[],"pagination":{"total":0}}`)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if got := body[tc.field]; got != tc.value {
					t.Errorf("%s = %v, want %q", tc.field, got, tc.value)
				}
				_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			})
			root.SetArgs(append(append([]string{"loop", "task"}, tc.args...), "--workspace-id", projectMemberTestID))
			if err := root.Execute(); err != nil || requests != 1 {
				t.Fatalf("execute: %v; requests=%d", err, requests)
			}
		})
	}
}

func TestTaskMemberIDHelpHidesWireTerminology(t *testing.T) {
	for _, verb := range []string{"create", "update", "list", "group", "assign"} {
		t.Run(verb, func(t *testing.T) {
			root, _ := rootWithDriveDryRun(t, "bf_assignment")
			var helpOutput strings.Builder
			root.SetOut(&helpOutput)
			args := []string{"loop", "task", verb}
			if verb == "update" || verb == "assign" {
				args = append(args, "TASK-1")
			}
			root.SetArgs(append(args, "--help"))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			help := strings.ToLower(helpOutput.String())
			if strings.Contains(help, "octo uid") || strings.Contains(help, "octo-uid") || strings.Contains(help, "octo_uid") {
				t.Errorf("help exposes backend identity terminology: %s", helpOutput.String())
			}
			if verb != "assign" && !strings.Contains(help, "--assignee-member-id") {
				t.Errorf("help omits member ID flag: %s", helpOutput.String())
			}
		})
	}
}

func TestTaskAssignmentLookupSuppressesSpaceHeader(t *testing.T) {
	lookups := 0
	root, _, _ := rootWithServiceSpaced(t, "space-from-profile", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Workspace-ID") != projectMemberTestID || r.Header.Get("X-Space-Id") != "" {
			t.Errorf("Loop request used wrong scope headers: %s %#v", r.URL, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			lookups++
			_, _ = io.WriteString(w, assignmentFixture(r.URL.Path))
			return
		}
		_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Alice"})
	if err := root.Execute(); err != nil || lookups != 3 {
		t.Fatalf("assignment lookups: err=%v count=%d", err, lookups)
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
		{"assign", "TASK-1", "--to", "@"},
		{"update", "TASK-1", "--assignee", "＠"},
		{"list", "--assignee", "Alice", "--assignee-octo-uids", "other"},
		{"list", "--assignee", "Alice", "--assignee-types", "expert"},
	} {
		root, _ := rootWithDriveToken(t, "bf_assignment", func(http.ResponseWriter, *http.Request) { t.Error("invalid assignment sent HTTP") })
		root.SetArgs(append(append([]string{"loop", "task"}, args...), "--workspace-id", projectMemberTestID))
		if err := root.Execute(); err == nil {
			t.Errorf("invalid selection accepted: %v", args)
		}
	}
}

func TestTaskAssignmentLookupStopsAfterTenPages(t *testing.T) {
	reads := 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/members") {
			t.Errorf("bounded lookup must only read members: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		reads++
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page > 10 {
			t.Errorf("lookup exceeded ten pages: %s", r.URL)
			http.Error(w, "too many pages", http.StatusBadRequest)
			return
		}
		items := make([]map[string]any, 100)
		for i := range items {
			items[i] = map[string]any{
				"uid": fmt.Sprintf("member-%d-%d", page, i), "name": fmt.Sprintf("Member %d-%d", page, i), "robot": 0,
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(items); err != nil {
			t.Error(err)
		}
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "Missing"})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "lookup page limit") || reads != 10 {
		t.Fatalf("bounded lookup: err=%v reads=%d", err, reads)
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
			if r.URL.Query().Get("page_size") != "100" || r.URL.Query().Has("offset") {
				t.Errorf("member lookup used unsupported pagination: %s", r.URL)
			}
			switch r.URL.Query().Get("page") {
			case "1":
				items := make([]map[string]any, 100)
				for i := range items {
					items[i] = map[string]any{"uid": fmt.Sprintf("member-%d", i), "name": fmt.Sprintf("Member %d", i), "robot": 0}
				}
				_ = json.NewEncoder(w).Encode(items)
			case "2":
				_, _ = io.WriteString(w, `[{"uid":"last","name":"Last","robot":0}]`)
			default:
				t.Errorf("unexpected member page: %s", r.URL)
				http.Error(w, "unexpected page", http.StatusBadRequest)
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
		`[{"uid":"Human/CaseSensitive","name":"Alice"}]`,
		`{"items":[{"uid":"Human/CaseSensitive","name":"Alice","robot":0}]}`,
		`{"data":null,"pagination":{"total":0}}`,
		`null`,
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
		{kind: "expert", id: "deadbeef-2222-4222-8222-222222222222", name: "Unrelated Expert"},
		{kind: "expert_team", id: assignmentTeamID, name: "deadbeef"},
	}
	actor, err := chooseTaskAssignee(actors, "alice", false)
	if err != nil || actor.uid != "uid-1" {
		t.Fatalf("exact name must beat substring: %#v %v", actor, err)
	}
	actor, err = chooseTaskAssignee(actors, "deadbeef", false)
	if err != nil || actor.id != assignmentTeamID {
		t.Fatalf("name selector must select the exact name, not a UUID prefix: %#v %v", actor, err)
	}
	if _, err := chooseTaskAssignee(actors, assignmentExpertID[:8], false); err == nil {
		t.Fatal("name selector accepted a UUID prefix")
	}
	if _, err := chooseTaskAssignee(actors, assignmentExpertID, false); err == nil {
		t.Fatal("name selector accepted a full ID")
	}
	actor, err = chooseTaskAssignee(actors, assignmentExpertID, true)
	if err != nil || actor.id != assignmentExpertID {
		t.Fatalf("ID selector must accept the full ID: %#v %v", actor, err)
	}
	archived := taskAssigneeRecord{TeamID: assignmentTeamID, Name: "Archived", ArchivedAt: json.RawMessage(`"2026-10-23"`)}
	if _, include, err := archived.assignee("expert_team"); err != nil || include {
		t.Fatalf("archived team must be excluded: %v %v", include, err)
	}
}

func TestTaskAssignmentNameCollisionWritesExactName(t *testing.T) {
	writes := 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			writes++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if body["assignee_type"] != "expert_team" || body["assignee_id"] != assignmentTeamID {
				t.Errorf("name collision assigned the wrong actor: %#v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/members"):
			_, _ = io.WriteString(w, `[]`)
		case strings.HasSuffix(r.URL.Path, "/experts"):
			_, _ = io.WriteString(w, `{"data":[{"expert_id":"deadbeef-2222-4222-8222-222222222222","name":"Unrelated Expert"}],"pagination":{"total":1}}`)
		case strings.HasSuffix(r.URL.Path, "/expert_teams"):
			_, _ = io.WriteString(w, `{"data":[{"expert_team_id":"`+assignmentTeamID+`","name":"deadbeef"}],"pagination":{"total":1}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "deadbeef"})
	if err := root.Execute(); err != nil || writes != 1 {
		t.Fatalf("assign exact name: err=%v writes=%d", err, writes)
	}
}

func TestTaskAssignmentMentionPrefixBeatsLiteralAtName(t *testing.T) {
	writes := 0
	root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch {
			writes++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["assignee_type"] != "member" || body["assignee_octo_uid"] != "alice" {
				t.Errorf("@Alice must resolve to Alice, got %#v", body)
			}
			_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/members"):
			_, _ = io.WriteString(w, `[{"uid":"alice","name":"Alice","robot":0},{"uid":"at-alice","name":"@Alice","robot":0}]`)
		case strings.HasSuffix(r.URL.Path, "/experts"), strings.HasSuffix(r.URL.Path, "/expert_teams"):
			_, _ = io.WriteString(w, `{"data":[],"pagination":{"total":0}}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	})
	root.SetArgs([]string{"loop", "task", "assign", "TASK-1", "--workspace-id", projectMemberTestID, "--to", "@Alice"})
	if err := root.Execute(); err != nil || writes != 1 {
		t.Fatalf("assign @Alice: err=%v writes=%d", err, writes)
	}
}
