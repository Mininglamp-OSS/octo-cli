package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

const projectMemberTestID = "11111111-1111-4111-8111-111111111111"

func TestWorkspaceMemberListHelpKeepsLoopNamespace(t *testing.T) {
	op, ok := registry.MustNew().GetOperation("workspace.member.list")
	if !ok {
		t.Fatal("workspace.member.list is missing")
	}
	command := buildOperationCmd(cmdutil.NewTestFactory().Factory, op, "list")
	if !strings.Contains(command.Long, "octo-cli loop workspace member list --workspace-id") {
		t.Fatalf("member-list help omitted the loop namespace: %s", command.Long)
	}
}

func TestWorkspaceMemberListReadsOctoProjectMembers(t *testing.T) {
	for _, scenario := range []struct {
		name string
		args []string
		page url.Values
	}{
		{name: "positional project with backend pagination defaults", args: []string{projectMemberTestID}},
		{name: "workspace flag selects project", args: []string{"--workspace-id", projectMemberTestID}},
		{
			name: "explicit offset pagination",
			args: []string{projectMemberTestID, "--limit", "2", "--offset", "4"},
			page: url.Values{"limit": {"2"}, "offset": {"4"}},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests := 0
			root, tf := rootWithDriveToken(t, "bf_project_test", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/v1/bot/projects/"+projectMemberTestID+"/members" {
					t.Errorf("unexpected member read: %s %s", r.Method, r.URL)
				}
				wantQuery := url.Values{"space_id": {"space-1"}, "obo": {"true"}}
				for name, values := range scenario.page {
					wantQuery[name] = values
				}
				if !reflect.DeepEqual(r.URL.Query(), wantQuery) {
					t.Errorf("query = %v, want %v", r.URL.Query(), wantQuery)
				}
				if r.Header.Get("Authorization") != "Bearer bf_project_test" {
					t.Error("member read lost the selected Bot credential")
				}
				if r.Header.Get("X-Workspace-ID") != "" {
					t.Error("Project membership must not depend on a Fleet Workspace header")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"items":[{"uid":"Human/CaseSensitive","name":"Alice","role":0,"robot":0},{"uid":"22222222-2222-4222-8222-222222222222","name":"Bot","role":1,"robot":1}],"total":6,"limit":2,"offset":4,"decision_id":"decision-1"}`)
			})
			args := append([]string{"loop", "workspace", "member", "list"}, scenario.args...)
			root.SetArgs(append(args, "--space-id", "space-1", "--obo"))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute Project member read: %v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want one independent Project member read", requests)
			}
			var response struct {
				OK   bool `json:"ok"`
				Data struct {
					Items []struct {
						UID   string `json:"uid"`
						Robot int    `json:"robot"`
					} `json:"items"`
					Total      int    `json:"total"`
					Limit      int    `json:"limit"`
					Offset     int    `json:"offset"`
					DecisionID string `json:"decision_id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(tf.Out.Bytes(), &response); err != nil {
				t.Fatalf("decode member envelope: %v", err)
			}
			if !response.OK || len(response.Data.Items) != 2 || response.Data.Items[0].UID != "Human/CaseSensitive" ||
				response.Data.Items[1].UID != "22222222-2222-4222-8222-222222222222" || response.Data.Items[1].Robot != 1 {
				t.Errorf("member identities must be returned unchanged, including Bot seats: %s", tf.Out.String())
			}
			if response.Data.Total != 6 || response.Data.Limit != 2 || response.Data.Offset != 4 || response.Data.DecisionID != "decision-1" {
				t.Errorf("Project page metadata was lost: %s", tf.Out.String())
			}
		})
	}
}

func TestWorkspaceMemberListRequiresExplicitDelegationAndSpace(t *testing.T) {
	for _, scenario := range []struct {
		name string
		args []string
		want string
	}{
		{name: "missing delegation", args: []string{"--space-id", "space-1"}, want: "obo"},
		{name: "missing space", args: []string{"--obo"}, want: "space-id"},
		{name: "blank space", args: []string{"--obo", "--space-id", ""}, want: "space-id"},
		{name: "false delegation", args: []string{"--obo=false", "--space-id", "space-1"}, want: enumNotAllowed},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root, tf := rootWithDriveToken(t, "bf_project_test", func(http.ResponseWriter, *http.Request) {
				t.Error("invalid Project member input must fail before HTTP")
			})
			args := []string{"loop", "workspace", "member", "list", projectMemberTestID}
			root.SetArgs(append(args, scenario.args...))
			err := root.Execute()
			if err == nil {
				t.Fatal("invalid Project member input was accepted")
			}
			if got := err.Error() + tf.ErrOut.String(); !strings.Contains(got, scenario.want) {
				t.Errorf("error = %s, want %s", got, scenario.want)
			}
		})
	}
}

func TestWorkspaceMemberListRejectsUnsupportedCredentials(t *testing.T) {
	for _, token := range []string{"app_project_test", "uk_project_test", "octo_loop_project_test"} {
		t.Run(strings.SplitN(token, "_", 2)[0], func(t *testing.T) {
			root, _ := rootWithDriveToken(t, token, func(http.ResponseWriter, *http.Request) {
				t.Error("unsupported credential must fail before Project HTTP")
			})
			root.SetArgs([]string{"loop", "workspace", "member", "list", projectMemberTestID, "--space-id", "space-1", "--obo"})
			err := root.Execute()
			if exit := output.AsExitError(err); exit == nil || exit.Code != tokenKindNotAllowed {
				t.Fatalf("error = %v, want %s", err, tokenKindNotAllowed)
			}
		})
	}
}

func TestWorkspaceMemberListDoesNotFallbackAfterProjectDenial(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			root, _ := rootWithDriveToken(t, "bf_project_test", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/bot/projects/"+projectMemberTestID+"/members" {
					t.Errorf("unexpected fallback route: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"PROJECT_READ_DENIED","message":"Project is not readable"}}`)
			})
			root.SetArgs([]string{"loop", "workspace", "member", "list", projectMemberTestID, "--space-id", "space-1", "--obo"})
			err := root.Execute()
			if exit := output.AsExitError(err); exit == nil || exit.Code != "PROJECT_READ_DENIED" {
				t.Fatalf("error = %v, want the Project denial", err)
			}
			if requests != 1 {
				t.Errorf("denied Project read sent %d requests, want one with no fallback", requests)
			}
		})
	}
}
