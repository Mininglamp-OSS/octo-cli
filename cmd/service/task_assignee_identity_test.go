package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTaskAssignmentIDCaseContracts(t *testing.T) {
	const nativeID = "abcdefab-1234-4234-8234-abcdefabcdef"
	const expertID = "bcdefabc-1234-4234-8234-bcdefabcdefa"
	const teamID = "cdefabcd-1234-4234-8234-cdefabcdefab"
	for _, selector := range []struct {
		name string
		args []string
	}{
		{"assign", []string{"assign", "TASK-1", "--to-id"}},
		{"update", []string{"update", "TASK-1", "--assignee-id"}},
		{"create", []string{"create", "--title", "Example", "--assignee-id"}},
	} {
		for _, identity := range []struct {
			name, input, kind, field, wantID string
		}{
			{"Project exact", "Human/CaseSensitive", "member", "assignee_octo_uid", "Human/CaseSensitive"},
			{"Project case variant", "human/casesensitive", "", "", ""},
			{"native UUID case variant", strings.ToUpper(nativeID), "member", "assignee_id", nativeID},
			{"expert UUID case variant", strings.ToUpper(expertID), "expert", "assignee_id", expertID},
			{"team UUID case variant", strings.ToUpper(teamID), "expert_team", "assignee_id", teamID},
		} {
			t.Run(selector.name+"/"+identity.name, func(t *testing.T) {
				reads, writes := 0, 0
				root, _ := rootWithDriveToken(t, "bf_assignment", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodGet {
						reads++
						var fixture string
						switch {
						case strings.HasSuffix(r.URL.Path, "/members"):
							fixture = `[{"uid":"Human/CaseSensitive","name":"Alice","robot":0},{"id":"membership-id","user_id":"` + nativeID + `","name":"Native","principal_type":"member"}]`
						case strings.HasSuffix(r.URL.Path, "/experts"):
							fixture = `{"data":[{"expert_id":"` + expertID + `","name":"Engineer"}],"pagination":{"total":1}}`
						case strings.HasSuffix(r.URL.Path, "/expert_teams"):
							fixture = `{"data":[{"expert_team_id":"` + teamID + `","name":"Research"}],"pagination":{"total":1}}`
						default:
							t.Errorf("unexpected lookup: %s", r.URL.Path)
						}
						_, _ = io.WriteString(w, fixture)
						return
					}
					writes++
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if identity.kind == "" || body["assignee_type"] != identity.kind || body[identity.field] != identity.wantID {
						t.Errorf("unexpected assignment body: %#v", body)
					}
					otherField := "assignee_octo_uid"
					if identity.field == otherField {
						otherField = "assignee_id"
					}
					if _, present := body[otherField]; present {
						t.Errorf("unexpected identity field: %s", otherField)
					}
					_, _ = io.WriteString(w, `{"data":{"task_id":"TASK-1"}}`)
				})
				args := append([]string{"loop", "task"}, selector.args...)
				args = append(args, identity.input, "--workspace-id", projectMemberTestID)
				root.SetArgs(args)
				err := root.Execute()
				wantWrites := 1
				if identity.kind == "" {
					wantWrites = 0
					if err == nil || !strings.Contains(err.Error(), "no member, expert or expert team found") {
						t.Fatalf("case variant must be refused: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if reads != 3 || writes != wantWrites {
					t.Fatalf("roster reads=%d writes=%d; want 3 and %d", reads, writes, wantWrites)
				}
				t.Logf("roster_reads=%d writes=%d canonical_ID=%q", reads, writes, identity.wantID)
			})
		}
	}
}

func TestTaskAssignmentIDCollisionRefused(t *testing.T) {
	const id = "abcdefab-1234-4234-8234-abcdefabcdef"
	actors := []taskAssignee{
		{kind: "member", id: id, uid: id, name: "Project Member"},
		{kind: "expert", id: id, name: "Expert"},
	}
	if _, err := chooseTaskAssignee(actors, id, true); err == nil || !strings.Contains(err.Error(), "ambiguous assignee") {
		t.Fatalf("matching IDs across kinds must remain ambiguous: %v", err)
	}
	actor, err := chooseTaskAssignee(actors, strings.ToUpper(id), true)
	if err != nil || actor.kind != "expert" {
		t.Fatalf("case-varied UUID must not match a Project UID: %#v %v", actor, err)
	}
}
