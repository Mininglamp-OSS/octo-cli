package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/cmdutil"
	"github.com/dmwork-org/octo-cli/internal/config"
	"github.com/dmwork-org/octo-cli/internal/credential"
	"github.com/dmwork-org/octo-cli/internal/registry"
)

// rootWithService builds a minimal cobra root wired to a real httptest server,
// a TestFactory, and the real embedded registry. The returned TestFactory lets
// the caller read emitted envelopes.
func rootWithService(t *testing.T, handler http.HandlerFunc) (*cobra.Command, *cmdutil.TestFactory, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIURL:      srv.URL,
		MattersURL:  srv.URL,
		DmworkIMURL: srv.URL,
		BotToken:    "app_test",
		Format:      "json",
	}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetCredential(cred)
	cli := client.New(cfg, cred, client.Options{ErrOut: io.Discard})
	tf.SetClient(cli)
	// Wire the registry explicitly to the one from the binary so the test
	// exercises the real specs.
	tf.RegistryFunc = func() *registry.Registry { return registry.MustNew() }

	root := &cobra.Command{Use: "octo", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)

	return root, tf, srv
}

// --- command-tree assertions ---

func TestRegisterServiceCommands_TreeShape(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})

	want := map[string][]string{
		"matter":  {"archive", "assignee", "channel", "close", "create", "delete", "extract", "get", "list", "reopen", "timeline", "transition", "update"},
		"message": {"edit", "read-receipt", "send", "sync"},
		"event":   {"ack", "list"},
	}
	for svc, wantCmds := range want {
		svcCmd := findCmd(root, svc)
		if svcCmd == nil {
			t.Fatalf("missing service command %q", svc)
		}
		got := childNames(svcCmd)
		for _, w := range wantCmds {
			if !contains(got, w) {
				t.Errorf("%s: missing subcommand %q; got %v", svc, w, got)
			}
		}
	}

	// Sub-resource nesting check.
	matter := findCmd(root, "matter")
	assignee := findCmd(matter, "assignee")
	if assignee == nil || !contains(childNames(assignee), "add") || !contains(childNames(assignee), "remove") {
		t.Errorf("matter assignee should nest add/remove; got %v", childNames(assignee))
	}
	timeline := findCmd(matter, "timeline")
	if timeline == nil || !contains(childNames(timeline), "add") || !contains(childNames(timeline), "list") || !contains(childNames(timeline), "delete") {
		t.Errorf("matter timeline should nest add/list/delete; got %v", childNames(timeline))
	}
}

// --- operation execution (matter.list) ---

func TestMatterList_QueryParamsFromFlags(t *testing.T) {
	var gotQuery string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"has_more":false,"next_cursor":""}}`))
	})
	root.SetArgs([]string{"matter", "list", "--status", "open", "--assignee-id", "me", "--limit", "5"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(gotQuery, "status=open") || !strings.Contains(gotQuery, "assignee_id=me") || !strings.Contains(gotQuery, "limit=5") {
		t.Errorf("query mismatch: %q", gotQuery)
	}
	if !bytes.Contains(tf.Out.Bytes(), []byte(`"ok": true`)) {
		t.Errorf("expected success envelope, got %s", tf.Out.String())
	}
}

// --- body field auto-promotion (matter.create --title) ---

func TestMatterCreate_BodyAutoPromotion(t *testing.T) {
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m1","title":"Deploy"}`))
	})
	root.SetArgs([]string{
		"matter", "create",
		"--title", "Deploy",
		"--description", "prod push",
		"--assignee-ids", "u1,u2",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["title"] != "Deploy" {
		t.Errorf("title = %v, want Deploy", gotBody["title"])
	}
	if gotBody["description"] != "prod push" {
		t.Errorf("description = %v", gotBody["description"])
	}
	arr, ok := gotBody["assignee_ids"].([]any)
	if !ok || len(arr) != 2 {
		t.Errorf("assignee_ids = %v", gotBody["assignee_ids"])
	}
}

// --- --data + individual flag precedence ---

func TestMatterCreate_DataThenFlagOverride(t *testing.T) {
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	})
	root.SetArgs([]string{
		"matter", "create",
		"--data", `{"title":"from-data","description":"base"}`,
		"--title", "override",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["title"] != "override" {
		t.Errorf("individual --title should override --data; got %v", gotBody["title"])
	}
	if gotBody["description"] != "base" {
		t.Errorf("--data-only field should persist; got %v", gotBody["description"])
	}
}

// --- high-risk-write gate on matter.delete ---

// --- matter.delete executes directly (no confirmation gate) ---

func TestMatterDelete_ExecutesDirectly(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		w.WriteHeader(204)
	})
	root.SetArgs([]string{"matter", "delete", "m1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Error("expected server call")
	}
}

// --- status aliases ---

func TestMatterStatusAliases(t *testing.T) {
	cases := []struct {
		cmd    string
		status string
	}{
		{"close", "done"},
		{"reopen", "open"},
		{"archive", "archived"},
	}
	for _, c := range cases {
		t.Run(c.cmd, func(t *testing.T) {
			var gotMethod, gotPath string
			var gotBody map[string]any
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				_ = json.NewDecoder(r.Body).Decode(&gotBody)
				w.WriteHeader(200)
				_, _ = w.Write([]byte(`{}`))
			})
			root.SetArgs([]string{"matter", c.cmd, "m1"})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotMethod != "PUT" {
				t.Errorf("method = %s, want PUT", gotMethod)
			}
			if gotPath != "/api/v1/matters/m1/status" {
				t.Errorf("path = %s", gotPath)
			}
			if gotBody["status"] != c.status {
				t.Errorf("status = %v, want %s", gotBody["status"], c.status)
			}
		})
	}
}

// --- dry-run output ---

func TestDryRun_NoRequest(t *testing.T) {
	called := false
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIURL: "http://dry.local", MattersURL: "http://dry.local", BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetCredential(cred)
	cli := client.New(cfg, cred, client.Options{DryRun: true, ErrOut: io.Discard})
	tf.SetClient(cli)
	tf.RegistryFunc = func() *registry.Registry { return registry.MustNew() }

	// A server we don't expect to hit — if we do, `called` flips.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	t.Cleanup(srv.Close)

	root := &cobra.Command{Use: "octo", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)

	root.SetArgs([]string{"matter", "list", "--status", "open"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called {
		t.Error("dry-run must not hit the network")
	}
	out := tf.Out.String()
	if !strings.Contains(out, `"dry_run": true`) || !strings.Contains(out, "status=open") {
		t.Errorf("dry-run output missing fields: %s", out)
	}
}

// --- pagination ---

func TestPagination_PageAllMergesAllPages(t *testing.T) {
	pages := []string{
		`{"data":[{"id":"1"},{"id":"2"}],"pagination":{"has_more":true,"next_cursor":"c2"}}`,
		`{"data":[{"id":"3"}],"pagination":{"has_more":true,"next_cursor":"c3"}}`,
		`{"data":[{"id":"4"}],"pagination":{"has_more":false,"next_cursor":""}}`,
	}
	var cursors []string
	idx := 0
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[idx]))
		idx++
	})
	root.SetArgs([]string{"matter", "list", "--page-all"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got, want := len(cursors), 3; got != want {
		t.Fatalf("wanted 3 requests, got %d (cursors=%v)", got, cursors)
	}
	if cursors[0] != "" || cursors[1] != "c2" || cursors[2] != "c3" {
		t.Errorf("cursor progression = %v", cursors)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK || len(env.Data) != 4 {
		t.Errorf("expected 4 merged items, got %+v", env)
	}
}

func TestPagination_PageLimitStops(t *testing.T) {
	page := `{"data":[{"id":"x"}],"pagination":{"has_more":true,"next_cursor":"c"}}`
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(page))
	})
	root.SetArgs([]string{"matter", "list", "--page-all", "--page-limit", "2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls bounded by --page-limit, got %d", calls)
	}
}

// --- helpers ---

func findCmd(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func childNames(parent *cobra.Command) []string {
	var out []string
	for _, c := range parent.Commands() {
		out = append(out, c.Name())
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
