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
			name: "explicit page pagination",
			args: []string{projectMemberTestID, "--page", "3", "--page-size", "2"},
			page: url.Values{"page": {"3"}, "page_size": {"2"}},
		},
		{
			name: "old offset pagination maps to a page",
			args: []string{projectMemberTestID, "--limit", "2", "--offset", "4"},
			page: url.Values{"page": {"3"}, "page_size": {"2"}},
		},
		{
			name: "offset uses default page size",
			args: []string{projectMemberTestID, "--offset", "50"},
			page: url.Values{"page": {"2"}, "page_size": {"50"}},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests := 0
			root, tf := rootWithDriveToken(t, "bf_project_test", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/fleet/api/v1/workspaces/"+projectMemberTestID+"/members" {
					t.Errorf("unexpected member read: %s %s", r.Method, r.URL)
				}
				wantQuery := url.Values{}
				for name, values := range scenario.page {
					wantQuery[name] = values
				}
				if !reflect.DeepEqual(r.URL.Query(), wantQuery) {
					t.Errorf("query = %v, want %v", r.URL.Query(), wantQuery)
				}
				if r.Header.Get("Authorization") != "Bearer bf_project_test" {
					t.Error("member read lost the selected Bot credential")
				}
				if r.Header.Get("X-Workspace-ID") != projectMemberTestID {
					t.Error("Fleet binding header must match the single Project selector")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `[{"uid":"Human/CaseSensitive","name":"Alice","role":0,"robot":0},{"uid":"22222222-2222-4222-8222-222222222222","name":"Bot","role":1,"robot":1}]`)
			})
			args := append([]string{"loop", "workspace", "member", "list"}, scenario.args...)
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute Project member read: %v", err)
			}
			if requests != 1 {
				t.Fatalf("requests = %d, want one independent Project member read", requests)
			}
			var response struct {
				OK   bool `json:"ok"`
				Data []struct {
					UID   string `json:"uid"`
					Robot int    `json:"robot"`
				} `json:"data"`
			}
			if err := json.Unmarshal(tf.Out.Bytes(), &response); err != nil {
				t.Fatalf("decode member envelope: %v", err)
			}
			if !response.OK || len(response.Data) != 2 || response.Data[0].UID != "Human/CaseSensitive" ||
				response.Data[1].UID != "22222222-2222-4222-8222-222222222222" || response.Data[1].Robot != 1 {
				t.Errorf("member identities must be returned unchanged, including Bot seats: %s", tf.Out.String())
			}
		})
	}
}

func TestWorkspaceMemberListRejectsObsoleteOBOArguments(t *testing.T) {
	for _, flag := range []string{"--obo", "--space-id"} {
		root, _ := rootWithDriveToken(t, "bf_project_test", func(http.ResponseWriter, *http.Request) { t.Error("invalid flag must fail before HTTP") })
		root.SetArgs([]string{"loop", "workspace", "member", "list", projectMemberTestID, flag, "1"})
		if err := root.Execute(); err == nil {
			t.Fatalf("obsolete flag accepted: %s", flag)
		}
	}
}

func TestWorkspaceMemberListRejectsInexactOffsetBeforeHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"--limit", "50", "--offset", "1"},
		{"--limit", "0", "--offset", "0"},
		{"--page", "2", "--offset", "50"},
		{"--page-size", "50", "--limit", "50"},
	} {
		root, _ := rootWithDriveToken(t, "bf_project_test", func(http.ResponseWriter, *http.Request) { t.Error("invalid pagination sent HTTP") })
		root.SetArgs(append([]string{"loop", "workspace", "member", "list", projectMemberTestID}, args...))
		if err := root.Execute(); err == nil {
			t.Errorf("accepted invalid pagination: %v", args)
		}
	}
}

func TestWorkspaceMemberListDoesNotFallbackAfterProjectDenial(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			requests := 0
			root, _ := rootWithDriveToken(t, "bf_project_test", func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/fleet/api/v1/workspaces/"+projectMemberTestID+"/members" {
					t.Errorf("unexpected fallback route: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"code":"PROJECT_READ_DENIED","message":"Project is not readable"}}`)
			})
			root.SetArgs([]string{"loop", "workspace", "member", "list", projectMemberTestID})
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
