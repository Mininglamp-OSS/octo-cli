package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestMain isolates the whole package from the developer's own OCTO_
// environment. These tests assert on the exact request the engine builds, and
// ambient variables rewrite it: OCTO_MARKETPLACE_API_PREFIX replaces the
// leading /market segment (run.go marketplacePath), OCTO_API_BASE_URL and
// OCTO_SPACE_ID feed the resolved config, OCTO_FORMAT reshapes the envelope.
//
// The sweep is by prefix rather than by name on purpose. Naming variables is
// what kept breaking: the clear list has been out of date three times
// (OCTO_TOKEN, then OCTO_FORMAT, then OCTO_BOT_ID), each time because a new
// variable was added without updating the tests. OCTO_MARKETPLACE_API_PREFIX is
// read straight from the environment with no config constant behind it, so it is
// exactly the kind a by-name list misses.
//
// OCTO_CONFIG_DIR is re-set *after* the sweep: it must point at an empty temp
// dir rather than be absent, or authstore falls back to the real user config
// dir and a developer's stored profiles leak back in. Tests that exercise a
// variable's effect still set it themselves with t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "octo-service-test")
	if err != nil {
		panic(err)
	}
	sweepOctoEnv()
	os.Setenv("OCTO_CONFIG_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// sweepOctoEnv unsets every OCTO_-prefixed variable in the process environment.
// os.Environ returns a snapshot, so unsetting while ranging over it is safe.
func sweepOctoEnv() {
	for _, kv := range os.Environ() {
		if name, _, found := strings.Cut(kv, "="); found && strings.HasPrefix(name, "OCTO_") {
			os.Unsetenv(name)
		}
	}
}

// rootWithService builds a minimal cobra root wired to a real httptest server,
// a TestFactory, and the real embedded registry. The returned TestFactory lets
// the caller read emitted envelopes.
func rootWithService(t *testing.T, handler http.HandlerFunc) (*cobra.Command, *cmdutil.TestFactory, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIBaseURL: srv.URL,
		BotToken:   "app_test",
		Format:     "json",
	}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetCredential(cred)
	cli := client.New(cfg, cred, client.Options{ErrOut: io.Discard})
	tf.SetClient(cli)
	// Wire the registry explicitly to the one from the binary so the test
	// exercises the real specs.
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)

	return root, tf, srv
}

// rootWithServiceSpaced is rootWithService with a credential that carries a
// SpaceID, so tests can assert whether X-Space-Id reaches the wire. It is the
// realistic shape for a platform-scoped bot (space supplied via --space /
// OCTO_SPACE_ID), which is exactly the case the per-operation space-header
// gating governs.
func rootWithServiceSpaced(t *testing.T, spaceID string, handler http.HandlerFunc) (*cobra.Command, *cmdutil.TestFactory, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIBaseURL: srv.URL,
		BotToken:   "app_test",
		Format:     "json",
	}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "app_test", SpaceID: spaceID, Source: "test"}
	tf.SetCredential(cred)
	cli := client.New(cfg, cred, client.Options{ErrOut: io.Discard})
	tf.SetClient(cli)
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
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

	loop := findCmd(root, "loop")
	for _, resource := range []string{
		"attachment", "autopilot", "comment", "execution", "expert",
		"expert-template", "expert-team", "label", "project", "runtime",
		"skill", "skill-file", "task", "workspace",
	} {
		if findCmd(loop, resource) == nil {
			t.Errorf("loop: missing resource %q; got %v", resource, childNames(loop))
		}
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

func TestLoopCommandUsesUnifiedGatewayAndModulePath(t *testing.T) {
	var gotPath, gotAuth, gotSpace string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotSpace = r.Header.Get("X-Space-Id")
		_, _ = w.Write([]byte(`{"data":{"task_id":"task-1"}}`))
	}))
	defer gateway.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIBaseURL: gateway.URL,
		BotToken:   "octo_loop_test",
		Format:     "json",
	}
	cred := &credential.BotCredential{Token: "octo_loop_test", SpaceID: "space-from-legacy-profile"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"loop", "task", "get", "task-1", "--workspace-id", "workspace-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("loop task get: %v", err)
	}
	if gotPath != "/fleet/api/v1/tasks/task-1" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer octo_loop_test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotSpace != "" {
		t.Fatalf("Loop public API must not receive X-Space-Id, got %q", gotSpace)
	}
}

func TestLoopTaskWorkspaceHeaderFlag(t *testing.T) {
	var gotPath, gotWorkspace string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotWorkspace = r.Header.Get("X-Workspace-ID")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
	}))
	defer gateway.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{
		APIBaseURL: gateway.URL,
		BotToken:   "octo_pat_test",
		Format:     "json",
	}
	cred := &credential.BotCredential{Token: "octo_pat_test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"loop", "task", "list", "--workspace-id", "workspace-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("loop task list: %v", err)
	}
	if gotPath != "/fleet/api/v1/tasks" || gotWorkspace != "workspace-1" {
		t.Fatalf("path=%q X-Workspace-ID=%q", gotPath, gotWorkspace)
	}
}

func TestLoopWorkspacePathHeaderFlag(t *testing.T) {
	var gotPath, gotWorkspace string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotWorkspace = r.Header.Get("X-Workspace-ID")
		_, _ = w.Write([]byte(`{"id":"workspace-1"}`))
	}))
	defer gateway.Close()

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: gateway.URL, BotToken: "octo_pat_test", Format: "json"}
	cred := &credential.BotCredential{Token: "octo_pat_test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"loop", "workspace", "get", "workspace-1", "--workspace-id", "workspace-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("loop workspace get: %v", err)
	}
	if gotPath != "/fleet/api/v1/workspaces/workspace-1" || gotWorkspace != "workspace-1" {
		t.Fatalf("path=%q X-Workspace-ID=%q", gotPath, gotWorkspace)
	}
}

func TestLoopWorkspaceIDFlagIsRequired(t *testing.T) {
	requestCount := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	root.SetArgs([]string{"loop", "task", "list"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "required flag(s) \"workspace-id\"") {
		t.Fatalf("loop task list error = %v, want required workspace-id", err)
	}
	if requestCount != 0 {
		t.Fatalf("request count = %d, want 0", requestCount)
	}
}

func TestLoopWorkspaceCreateIsNotRegistered(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("hidden workspace create command must not send a request")
	})
	workspace := findCmd(findCmd(root, "loop"), "workspace")
	if workspace == nil {
		t.Fatal("loop workspace command not registered")
	}
	if findCmd(workspace, "create") != nil {
		t.Fatal("loop workspace create must not be registered")
	}
}

func TestRegisterServiceCommands_PreservesForeignOperationDomain(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})
	marketplace := findCmd(root, "marketplace")
	if marketplace == nil {
		t.Fatal("missing marketplace service command")
	}
	for _, domain := range []string{"skill", "mcp"} {
		if findCmd(marketplace, domain) == nil {
			t.Errorf("marketplace must preserve %q from the operationId", domain)
		}
	}
}

// --- operation execution (matter.list) ---

func TestHTMLDocIDUseAndOutboundWireCompatibility(t *testing.T) {
	var gotPath, gotQuery string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.EscapedPath(), r.URL.RawQuery
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	})

	get := findCmd(findCmd(root, "html"), "get")
	if get == nil {
		t.Fatal("missing html get")
	}
	if get.Use != "get <doc-ref>" {
		t.Fatalf("html get Use = %q, want %q", get.Use, "get <doc-ref>")
	}
	root.SetArgs([]string{"html", "get", "doc/canonical"})
	if err := root.Execute(); err != nil {
		t.Fatalf("html get: %v", err)
	}
	if gotPath != "/docs-html/v1/docs/doc%2Fcanonical" {
		t.Errorf("get path = %q", gotPath)
	}

	gotBody = nil
	root.SetArgs([]string{"html", "publish", "--html", "<h1>new</h1>", "--idempotency-key", "create-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("canonical html create: %v", err)
	}
	if gotBody["html"] != "<h1>new</h1>" || gotBody["idempotency_key"] != "create-1" {
		t.Errorf("canonical create body = %#v", gotBody)
	}
	if _, hasSlug := gotBody["slug"]; hasSlug {
		t.Errorf("canonical create body must omit slug: %#v", gotBody)
	}

	root.SetArgs([]string{"html", "comment", "list", "--slug", "doc-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("comment list: %v", err)
	}
	if gotQuery != "slug=doc-1" {
		t.Errorf("comment query = %q, wire key must remain slug", gotQuery)
	}

	root.SetArgs([]string{"html", "element", "get", "--slug", "doc-1", "--aid", "a1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("element get: %v", err)
	}
	if gotBody["slug"] != "doc-1" {
		t.Errorf("element body = %#v, wire key must remain slug", gotBody)
	}
}

func TestHTMLPublishConditionalValidationAndResponseUnwrap(t *testing.T) {
	var hits int
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"doc-1","doc_id":"doc-1","status":"published"}}`))
	})

	invalid := [][]string{
		{"html", "publish", "--html", "new", "--slug", "legacy-unknown", "--idempotency-key", "bad"},
		{"html", "publish", "--data", `{"html":"new","slug":null,"idempotency_key":"bad"}`},
		{"html", "publish", "--data", `{"html":"new","slug":"doc-1","idempotency_key":null}`},
	}
	for _, args := range invalid {
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Fatalf("%v unexpectedly succeeded", args)
		}
	}
	if hits != 0 {
		t.Fatalf("invalid publishes reached backend %d time(s)", hits)
	}

	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"doc-1","doc_id":"doc-1","status":"published"}}`))
	})
	root.SetArgs([]string{"html", "publish", "--html", "new", "--slug", "doc-1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("republish existing: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	data, _ := env["data"].(map[string]any)
	if data["slug"] != "doc-1" || data["doc_id"] != "doc-1" || data["data"] != nil {
		t.Errorf("CLI response was not unwrapped: %s", tf.Out.String())
	}
}

func TestHTMLMutationResponseUnwrapContract(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "delete", args: []string{"html", "rm", "doc-1"}},
		{name: "draft save", args: []string{"html", "draft", "save", "doc-1", "--html", "updated"}},
		{name: "unshare", args: []string{"html", "unshare", "doc-1"}},
		{name: "grant add", args: []string{"html", "grant", "add", "doc-1", "--uid", "u1"}},
		{name: "grant remove", args: []string{"html", "grant", "rm", "doc-1", "u1"}},
		{name: "asset remove", args: []string{"html", "asset", "rm", "doc-1", "sum"}},
		{name: "comment add", args: []string{"html", "comment", "add", "--slug", "doc-1", "--text", "hello"}},
		{name: "reply", args: []string{"html", "reply", "--slug", "doc-1", "--parent-id", "c1", "--text", "hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"marker":"unwrapped"}}`))
			})
			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var env map[string]any
			if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
				t.Fatalf("response: %v: %s", err, tf.Out.String())
			}
			data, _ := env["data"].(map[string]any)
			if data["marker"] != "unwrapped" || data["data"] != nil {
				t.Errorf("response was not unwrapped: %s", tf.Out.String())
			}
		})
	}
}

func TestHTMLListOperationsPreserveOffsetPaginationEnvelope(t *testing.T) {
	tests := []struct {
		name        string
		commandPath []string
		args        []string
		wantQuery   string
	}{
		{name: "documents", commandPath: []string{"html", "list"}, args: []string{"html", "list"}},
		{name: "comments", commandPath: []string{"html", "comment", "list"}, args: []string{"html", "comment", "list", "--slug", "doc-1"}, wantQuery: "slug=doc-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.RawQuery != tt.wantQuery {
					t.Errorf("query = %q, want %q", r.URL.RawQuery, tt.wantQuery)
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"one"}],"pagination":{"total":11,"page":1,"page_size":20}}`))
			})

			cmd, _, err := root.Find(tt.commandPath)
			if err != nil {
				t.Fatalf("find command: %v", err)
			}
			for _, flag := range []string{"cursor", "limit", "page-all", "page-limit"} {
				if cmd.Flags().Lookup(flag) != nil {
					t.Errorf("%s unexpectedly exposes --%s", strings.Join(tt.commandPath, " "), flag)
				}
			}

			root.SetArgs(tt.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			var env map[string]any
			if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
				t.Fatalf("response: %v: %s", err, tf.Out.String())
			}
			if _, ok := env["data"].([]any); !ok {
				t.Errorf("data = %T, want array: %s", env["data"], tf.Out.String())
			}
			pagination, ok := env["_pagination"].(map[string]any)
			if !ok || pagination["total"] != float64(11) || pagination["page"] != float64(1) || pagination["page_size"] != float64(20) {
				t.Errorf("_pagination = %#v, want offset pagination: %s", env["_pagination"], tf.Out.String())
			}
		})
	}
}

func TestHTMLCreateGeneratesRunScopedIdempotencyKey(t *testing.T) {
	var bodies []map[string]any
	hits := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":"TEMP","message":"retry"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"slug":"doc-1","doc_id":"doc-1"}}`))
	})
	root.SetArgs([]string{"html", "publish", "--html", "new"})
	if err := root.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("request count = %d, want retry", len(bodies))
	}
	first, _ := bodies[0]["idempotency_key"].(string)
	second, _ := bodies[1]["idempotency_key"].(string)
	if first == "" || first != second {
		t.Errorf("retry keys = %q, %q; want one generated key", first, second)
	}
}

func TestHTMLCreateKeysDifferAcrossInvocationsAndExplicitKeyWins(t *testing.T) {
	var keys []string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		keys = append(keys, body["idempotency_key"].(string))
		_, _ = w.Write([]byte(`{"data":{"slug":"doc-1","doc_id":"doc-1"}}`))
	})
	for _, args := range [][]string{
		{"html", "publish", "--html", "one"},
		{"html", "publish", "--html", "two"},
		{"html", "publish", "--html", "three", "--idempotency-key", "caller-key"},
	} {
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if keys[0] == "" || keys[1] == "" || keys[0] == keys[1] {
		t.Errorf("independent invocation keys = %q, %q", keys[0], keys[1])
	}
	if keys[2] != "caller-key" {
		t.Errorf("explicit key = %q", keys[2])
	}
}

func TestHTMLCanonicalDraftCreate(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = w.Write([]byte(`{"data":{"slug":"doc-draft","doc_id":"doc-draft"}}`))
	})
	root.SetArgs([]string{"html", "draft", "create", "--html", "<h1>wip</h1>"})
	if err := root.Execute(); err != nil {
		t.Fatalf("draft create: %v", err)
	}
	if key, _ := gotBody["idempotency_key"].(string); gotPath != "/docs-html/v1/docs/draft" || key == "" {
		t.Errorf("draft create request: path=%q body=%#v", gotPath, gotBody)
	}
}

func TestHTMLDraftCreateRejectsSlugWithoutHTTPRequest(t *testing.T) {
	hits := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	root.SetArgs([]string{"html", "draft", "create", "--data", `{"html":"wip","slug":"legacy"}`})
	if err := root.Execute(); err == nil {
		t.Fatal("draft create with slug unexpectedly succeeded")
	}
	if hits != 0 {
		t.Fatalf("draft create with slug reached backend %d time(s)", hits)
	}
}

func TestHTMLAutoIdempotencyReplacesEmptyValues(t *testing.T) {
	operations := []struct {
		name string
		args []string
	}{
		{name: "publish", args: []string{"html", "publish"}},
		{name: "draft create", args: []string{"html", "draft", "create"}},
	}
	inputs := []struct {
		name  string
		value string
	}{
		{name: "absent"},
		{name: "null", value: `,"idempotency_key":null`},
		{name: "empty", value: `,"idempotency_key":""`},
		{name: "whitespace", value: `,"idempotency_key":"   "`},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			var keys []string
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				key, _ := body["idempotency_key"].(string)
				keys = append(keys, key)
				_, _ = w.Write([]byte(`{"data":{"slug":"doc-1","doc_id":"doc-1"}}`))
			})
			for _, input := range inputs {
				args := append(append([]string{}, operation.args...), "--data", `{"html":"wip"`+input.value+`}`)
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("%s: %v", input.name, err)
				}
			}
			seen := map[string]bool{}
			for i, key := range keys {
				if strings.TrimSpace(key) == "" {
					t.Errorf("%s key = %q, want generated key", inputs[i].name, key)
				}
				if seen[key] {
					t.Errorf("generated key %q was reused across invocations", key)
				}
				seen[key] = true
			}
		})
	}
}

func TestHTMLAutoIdempotencyPreservesExplicitNonEmptyValue(t *testing.T) {
	for _, args := range [][]string{
		{"html", "publish", "--data", `{"html":"wip","idempotency_key":"caller-key"}`},
		{"html", "draft", "create", "--data", `{"html":"wip","idempotency_key":"caller-key"}`},
	} {
		var got string
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			got, _ = body["idempotency_key"].(string)
			_, _ = w.Write([]byte(`{"data":{}}`))
		})
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if got != "caller-key" {
			t.Errorf("%v sent key %q", args[:3], got)
		}
	}
}

func TestHTMLVariantsRejectEmptyRequiredStringsWithoutHTTPRequest(t *testing.T) {
	for _, args := range [][]string{
		{"html", "publish", "--html", ""},
		{"html", "draft", "create", "--html", ""},
	} {
		hits := 0
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { hits++ })
		root.SetArgs(args)
		if err := root.Execute(); err == nil {
			t.Errorf("%v unexpectedly succeeded", args)
		}
		if hits != 0 {
			t.Errorf("%v reached backend %d time(s)", args, hits)
		}
	}
}

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
	cfg := &config.Config{APIBaseURL: "http://dry.local", BotToken: "app_test", Format: "json"}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetCredential(cred)
	cli := client.New(cfg, cred, client.Options{DryRun: true, ErrOut: io.Discard})
	tf.SetClient(cli)
	tf.RegistryFunc = registry.MustNew

	// A server we don't expect to hit — if we do, `called` flips.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	t.Cleanup(srv.Close)

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
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

// TestPagination_BodyCursorEndpoint pins S1: for POST-body search endpoints the
// spec declares `cursor` as a body field (not a query param), so paging must
// inject the next cursor into the request BODY, never the URL query. A body
// clone per page must not mutate the previous page's body.
func TestPagination_BodyCursorEndpoint(t *testing.T) {
	pages := []string{
		`{"data":[{"id":"1"}],"pagination":{"has_more":true,"next_cursor":"c2"}}`,
		`{"data":[{"id":"2"}],"pagination":{"has_more":true,"next_cursor":"c3"}}`,
		`{"data":[{"id":"3"}],"pagination":{"has_more":false,"next_cursor":""}}`,
	}
	var bodyCursors []string
	var queryCursors []string
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCursors = append(queryCursors, r.URL.Query().Get("cursor"))
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		c, _ := body["cursor"].(string)
		bodyCursors = append(bodyCursors, c)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[idx]))
		idx++
	}))
	t.Cleanup(srv.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: "bf_test", Format: "json"}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: "bf_test", Source: "test"}
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	// message.search is a POST-body endpoint; --chat-id keeps it in-scope.
	root.SetArgs([]string{"message", "search", "--chat-id", "c1", "--keyword", "hi", "--page-all"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(bodyCursors) != 3 {
		t.Fatalf("wanted 3 requests, got %d (body cursors=%v)", len(bodyCursors), bodyCursors)
	}
	// Cursor progresses in the BODY, first page has none.
	if bodyCursors[0] != "" || bodyCursors[1] != "c2" || bodyCursors[2] != "c3" {
		t.Errorf("body cursor progression = %v, want ['' c2 c3]", bodyCursors)
	}
	// Cursor never leaks into the query string.
	for i, qc := range queryCursors {
		if qc != "" {
			t.Errorf("page %d: cursor leaked into query (%q); it must stay in the body", i, qc)
		}
	}
}

func TestPagination_PageLimitStops(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		page := fmt.Sprintf(`{"data":[{"id":"x"}],"pagination":{"has_more":true,"next_cursor":"c%d"}}`, calls)
		_, _ = w.Write([]byte(page))
	})
	root.SetArgs([]string{"matter", "list", "--page-all", "--page-limit", "5"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 5 {
		t.Errorf("expected 5 calls bounded by --page-limit, got %d", calls)
	}
}

func TestPagination_LegacyStableCursorStillUsesPageLimit(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"data":[{"id":"x"}],"pagination":{"has_more":true,"next_cursor":"stable"}}`))
	})
	root.SetArgs([]string{"matter", "list", "--page-all", "--page-limit", "5"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 5 {
		t.Errorf("stable cursor must remain valid for legacy operations: got %d calls, want 5", calls)
	}
}

func TestParsePage_CustomFieldPaths(t *testing.T) {
	items, cursor, hasMore, err := parsePage([]byte(`{
		"result":{"hits":[{"z":1,"a":"<b>hi</b>"}]},
		"paging":{"after":"c2","more":true}
	}`), &registry.PaginationInfo{
		ItemsField:   "result.hits",
		CursorField:  "paging.after",
		HasMoreField: "paging.more",
	})
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if len(items) != 1 || string(items[0]) != `{"z":1,"a":"<b>hi</b>"}` || cursor != "c2" || !hasMore {
		t.Errorf("items=%s cursor=%q hasMore=%v", items, cursor, hasMore)
	}
}

func TestParsePage_InferHasMoreRequiresOptIn(t *testing.T) {
	body := []byte(`{"items":[],"nextCursor":"c2"}`)
	base := registry.PaginationInfo{ItemsField: "items", CursorField: "nextCursor"}
	_, _, hasMore, err := parsePage(body, &base)
	if err != nil {
		t.Fatalf("parsePage without inference: %v", err)
	}
	if hasMore {
		t.Fatal("cursor alone must not imply has-more without explicit opt-in")
	}
	base.InferHasMore = true
	_, _, hasMore, err = parsePage(body, &base)
	if err != nil {
		t.Fatalf("parsePage with inference: %v", err)
	}
	if !hasMore {
		t.Fatal("non-empty cursor must imply has-more with explicit opt-in")
	}
}

func TestParsePage_LegacyNullItemsAreEmpty(t *testing.T) {
	items, cursor, hasMore, err := parsePage([]byte(`{"data":null,"pagination":{"has_more":false,"next_cursor":null}}`), nil)
	if err != nil {
		t.Fatalf("parsePage: %v", err)
	}
	if items != nil || cursor != "" || hasMore {
		t.Errorf("items=%s cursor=%q hasMore=%v, want empty page", items, cursor, hasMore)
	}
}

func TestParsePage_RejectsInvalidFieldTypes(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "items object", body: `{"data":{},"pagination":{"has_more":false,"next_cursor":null}}`},
		{name: "cursor object", body: `{"data":[],"pagination":{"has_more":true,"next_cursor":{}}}`},
		{name: "has-more string", body: `{"data":[],"pagination":{"has_more":"yes","next_cursor":"c2"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parsePage([]byte(tt.body), nil)
			if err == nil || err.Code != "PAGINATION_PARSE" {
				t.Fatalf("error = %#v, want PAGINATION_PARSE", err)
			}
		})
	}
}

// TestRunPaginated_RejectsOutputPathConflict pins the item-2 guard: pagination
// and binary output-to-disk are mutually exclusive (the page loop reuses the
// single OutputPath, so it would write every page over the same file). No spec
// op declares both today, so this cannot be reached through the command tree —
// it is a fail-loud guard called directly here to prove a future spec wiring
// the two together is rejected up front rather than silently corrupting output.
func TestRunPaginated_RejectsOutputPathConflict(t *testing.T) {
	tf := cmdutil.NewTestFactory()
	tf.SetConfig(&config.Config{APIBaseURL: "http://example.invalid", Format: "json"})

	dst := filepath.Join(t.TempDir(), "must-not-write.bin")
	err := runPaginated(context.Background(), tf.Factory, &operationRuntime{}, &client.Request{
		Method: "GET", Path: "/x", OutputPath: dst,
	})
	if err == nil {
		t.Fatal("expected an error for a paginated op carrying OutputPath, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should explain the conflict, got %q", err.Error())
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Error("guard must reject before any write; output file must not exist")
	}
}

// TestDocsSceneExport_DescribeOnlyEnvelope covers the no-`-o` path: without a
// destination, docs.scene.export must not write anything and the envelope
// reports the binary body's status / content_type / size (describe-only). This
// path was only covered indirectly before.
func TestDocsSceneExport_DescribeOnlyEnvelope(t *testing.T) {
	payload := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle r="5"/></svg>`)
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write(payload)
	})

	root.SetArgs([]string{"docs", "scene", "export", "abc", "--image-format", "svg"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Status      int    `json:"status"`
			ContentType string `json:"content_type"`
			Size        int    `json:"size"`
			Output      string `json:"output"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK {
		t.Fatalf("expected ok envelope, got %s", tf.Out.String())
	}
	if env.Data.Status != 200 {
		t.Errorf("status = %d, want 200", env.Data.Status)
	}
	if env.Data.ContentType != "image/svg+xml" {
		t.Errorf("content_type = %q, want image/svg+xml", env.Data.ContentType)
	}
	if env.Data.Size != len(payload) {
		t.Errorf("size = %d, want %d", env.Data.Size, len(payload))
	}
	if env.Data.Output != "" {
		t.Errorf("describe-only envelope must not carry an output path, got %q", env.Data.Output)
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

// --- multipart upload (file.upload) ---

// TestFileUpload_FlagRegistration confirms a multipart operation gets a --file
// flag and that it is marked required by cobra.
func TestFileUpload_FlagRegistration(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})

	upload := findCmd(findCmd(root, "file"), "upload")
	if upload == nil {
		t.Fatal("file upload command not registered")
	}
	f := upload.Flags().Lookup("file")
	if f == nil {
		t.Fatal("multipart operation missing --file flag")
	}
	required := upload.Flags().Lookup("file").Annotations[cobra.BashCompOneRequiredFlag]
	if len(required) == 0 || required[0] != "true" {
		t.Errorf("--file should be required; annotations=%v", required)
	}
}

// TestFileUpload_MissingFileReturnsError confirms running the upload command
// without --file fails before touching the network.
func TestFileUpload_MissingFileReturnsError(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	root.SetArgs([]string{"file", "upload"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for missing --file")
	}
	if called {
		t.Error("server should not be called when --file is missing")
	}
}

// TestFileUpload_ContentTypeIsMultipart confirms the client sends multipart
// form-data and that the uploaded bytes land in the "file" form field.
func TestFileUpload_ContentTypeIsMultipart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	payload := []byte("hello multipart")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	var gotCT string
	var gotFileName string
	var gotFileBody []byte
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		mediaType, params, err := mime.ParseMediaType(gotCT)
		if err != nil {
			t.Fatalf("parse media type: %v", err)
		}
		if mediaType != "multipart/form-data" {
			t.Errorf("media type = %q", mediaType)
		}
		if params["boundary"] == "" {
			t.Error("missing boundary parameter")
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		fhs := r.MultipartForm.File["file"]
		if len(fhs) != 1 {
			t.Fatalf("expected 1 file part, got %d", len(fhs))
		}
		gotFileName = fhs[0].Filename
		f, err := fhs[0].Open()
		if err != nil {
			t.Fatalf("open part: %v", err)
		}
		defer f.Close()
		gotFileBody, _ = io.ReadAll(f)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"url":"cos://x","name":"hello.txt","size":15}`))
	})
	root.SetArgs([]string{"file", "upload", "--file", path})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(gotCT, "multipart/form-data") {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotFileName != "hello.txt" {
		t.Errorf("filename = %q", gotFileName)
	}
	if !bytes.Equal(gotFileBody, payload) {
		t.Errorf("body = %q want %q", gotFileBody, payload)
	}
}

// TestBuildMultipartBody_FormTextFields directly exercises buildMultipartBody
// to confirm non-binary body flags are emitted as form text fields alongside
// the binary part. The current registry has no multipart op with additional
// body properties, so this synthesises the runtime.
func TestBuildMultipartBody_FormTextFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(path, []byte("binary"), 0o644); err != nil {
		t.Fatalf("write tmp file: %v", err)
	}

	filePath := path
	label := "report"
	count := 7
	flag := true
	tags := []string{"a", "b"}
	rt := &operationRuntime{
		filePath: &filePath,
		bodyFlags: map[string]*bodyFlag{
			"label": {apiName: "label", kind: kindString, strVal: &label},
			"count": {apiName: "count", kind: kindInt, intVal: &count},
			"flag":  {apiName: "flag", kind: kindBool, boolVal: &flag},
			"tags":  {apiName: "tags", kind: kindStringSlice, strSlc: &tags},
		},
	}

	cmd := &cobra.Command{Use: "synth"}
	cmd.Flags().StringVar(&filePath, "file", filePath, "")
	cmd.Flags().StringVar(&label, "label", label, "")
	cmd.Flags().IntVar(&count, "count", count, "")
	cmd.Flags().BoolVar(&flag, "flag", flag, "")
	cmd.Flags().StringSliceVar(&tags, "tags", tags, "")
	if err := cmd.ParseFlags([]string{"--label", "report", "--count", "7", "--flag", "--tags", "a,b"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	raw, ct, err := buildMultipartBody(cmd, rt)
	if err != nil {
		t.Fatalf("buildMultipartBody: %v", err)
	}
	if !strings.HasPrefix(ct, "multipart/form-data") {
		t.Fatalf("content type = %q", ct)
	}

	_, params, _ := mime.ParseMediaType(ct)
	req := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
	req.Header.Set("Content-Type", ct)
	_ = params
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatalf("parse multipart: %v", err)
	}

	if got := req.MultipartForm.Value["label"]; len(got) != 1 || got[0] != "report" {
		t.Errorf("label form field = %v", got)
	}
	if got := req.MultipartForm.Value["count"]; len(got) != 1 || got[0] != "7" {
		t.Errorf("count form field = %v", got)
	}
	if got := req.MultipartForm.Value["flag"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("flag form field = %v", got)
	}
	if got := req.MultipartForm.Value["tags"]; len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("tags form field = %v", got)
	}
	if len(req.MultipartForm.File["file"]) != 1 {
		t.Error("binary file part missing")
	}
}

func TestValidateRequiredBodyFields_SkipsMultipartBinaryProperty(t *testing.T) {
	rt := &operationRuntime{detail: &registry.OperationDetail{
		Multipart: true,
		RequestBody: &registry.SchemaInfo{
			Type:       "object",
			Required:   []string{"file", "label"},
			Properties: map[string]registry.SchemaInfo{"file": {Type: "string", Format: "binary"}, "label": {Type: "string"}},
		},
	}}
	if err := validateRequiredBodyFields(rt, map[string]any{"label": "report"}); err != nil {
		t.Fatalf("multipart validation must be handled by buildMultipartBody: %v", err)
	}
}

func TestValidateRequiredBodyFields_EnforcesComposedSchemas(t *testing.T) {
	r := registry.MustNew()
	quickCreate, ok := r.GetOperation("task.quick_create")
	if !ok || quickCreate.RequestBody == nil {
		t.Fatal("task.quick_create request schema not found")
	}
	rt := &operationRuntime{detail: quickCreate}
	for _, body := range []map[string]any{
		{"prompt": "work"},
		{"prompt": "work", "expert_id": "e1", "expert_team_id": "et1"},
	} {
		if err := validateRequiredBodyFields(rt, body); err == nil {
			t.Fatalf("body should fail oneOf validation: %#v", body)
		}
	}
	if err := validateRequiredBodyFields(rt, map[string]any{"prompt": "work", "expert_id": "e1"}); err != nil {
		t.Fatalf("valid oneOf body rejected: %v", err)
	}

	secret, ok := r.GetOperation("autopilot.signing_secret.set")
	if !ok || secret.RequestBody == nil {
		t.Fatal("signing secret request schema not found")
	}
	rt = &operationRuntime{detail: secret}
	if err := validateRequiredBodyFields(rt, map[string]any{"signing_secret": "short"}); err == nil {
		t.Fatal("short non-empty signing secret should fail anyOf validation")
	}
	if err := validateRequiredBodyFields(rt, map[string]any{"signing_secret": ""}); err != nil {
		t.Fatalf("empty signing secret should be accepted for clearing: %v", err)
	}

	numericConst := &registry.OperationDetail{
		OperationInfo:       registry.OperationInfo{Service: "loop"},
		RequestBodyRequired: true,
		RequestBody: &registry.SchemaInfo{
			Type: "object",
			Properties: map[string]registry.SchemaInfo{
				"value": {Const: float64(1)},
			},
		},
	}
	if err := validateRequiredBodyFields(
		&operationRuntime{detail: numericConst},
		map[string]any{"value": json.Number("1")},
	); err != nil {
		t.Fatalf("numeric constant should match an equivalent JSON number: %v", err)
	}
}

func TestValidateRequiredBodyFields_PreservesLegacyConstraintHandling(t *testing.T) {
	r := registry.MustNew()
	threadCreate, ok := r.GetOperation("thread.create")
	if !ok || threadCreate.RequestBody == nil {
		t.Fatal("thread.create request schema not found")
	}
	if err := validateRequiredBodyFields(
		&operationRuntime{detail: threadCreate},
		map[string]any{"name": strings.Repeat("x", 101)},
	); err != nil {
		t.Fatalf("legacy service string bounds must remain backend-enforced: %v", err)
	}

	additionalProperties := false
	legacy := &registry.OperationDetail{
		OperationInfo:       registry.OperationInfo{Service: "thread"},
		RequestBodyRequired: true,
		RequestBody: &registry.SchemaInfo{
			Type:                 "object",
			MinProperties:        2,
			AdditionalProperties: &additionalProperties,
			Properties: map[string]registry.SchemaInfo{
				"title": {Type: "string"},
			},
		},
	}
	if err := validateRequiredBodyFields(
		&operationRuntime{detail: legacy},
		map[string]any{"title": nil, "unknown": true},
	); err != nil {
		t.Fatalf("legacy object constraints must remain backend-enforced: %v", err)
	}
}

func TestValidateRequiredBodyFields_AllowsOptionalLoopNulls(t *testing.T) {
	r := registry.MustNew()
	update, ok := r.GetOperation("autopilot.update")
	if !ok || update.RequestBody == nil {
		t.Fatal("autopilot.update request schema not found")
	}
	body := map[string]any{
		"description":         nil,
		"project_id":          nil,
		"task_title_template": nil,
	}
	if err := validateRequiredBodyFields(&operationRuntime{detail: update}, body); err != nil {
		t.Fatalf("optional Loop fields must be forwarded for clearing: %v", err)
	}

	taskUpdate, ok := r.GetOperation("task.update")
	if !ok || taskUpdate.RequestBody == nil {
		t.Fatal("task.update request schema not found")
	}
	if err := validateRequiredBodyFields(
		&operationRuntime{detail: taskUpdate},
		map[string]any{"description": nil, "parent_task_id": nil},
	); err != nil {
		t.Fatalf("optional typed Loop fields must preserve null passthrough: %v", err)
	}

	metadataSet, ok := r.GetOperation("task.metadata.set")
	if !ok || metadataSet.RequestBody == nil {
		t.Fatal("task.metadata.set request schema not found")
	}
	if err := validateRequiredBodyFields(
		&operationRuntime{detail: metadataSet},
		map[string]any{"value": nil},
	); err == nil {
		t.Fatal("a required Loop field set to null must remain missing")
	}
}

func TestValidateRequiredBodyFields_EnforcesLoopObjectConstraints(t *testing.T) {
	r := registry.MustNew()
	update, ok := r.GetOperation("autopilot.update")
	if !ok || update.RequestBody == nil {
		t.Fatal("autopilot.update request schema not found")
	}

	for name, body := range map[string]map[string]any{
		"empty update":  {},
		"unknown field": {"unknown": true},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateRequiredBodyFields(&operationRuntime{detail: update}, body)
			if err == nil {
				t.Fatalf("body should fail Loop object validation: %#v", body)
			}
			if name == "empty update" && strings.Contains(err.Error(), "<root>") {
				t.Fatalf("root placeholder leaked into validation error: %v", err)
			}
		})
	}

	trigger, ok := r.GetOperation("autopilot.trigger_config.create")
	if !ok || trigger.RequestBody == nil {
		t.Fatal("autopilot.trigger_config.create request schema not found")
	}
	for name, body := range map[string]map[string]any{
		"nested unknown field": {"kind": "webhook", "event_filters": []any{map[string]any{"event": "push", "garbage": true}}},
		"nested missing field": {"kind": "webhook", "event_filters": []any{map[string]any{}}},
		"nested string bound":  {"kind": "webhook", "event_filters": []any{map[string]any{"event": ""}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateRequiredBodyFields(&operationRuntime{detail: trigger}, body); err == nil {
				t.Fatalf("nested body should fail Loop validation: %#v", body)
			}
		})
	}
}

// TestBuildMultipartBody_MissingFile confirms the validation error surfaces
// when --file is empty.
func TestBuildMultipartBody_MissingFile(t *testing.T) {
	empty := ""
	rt := &operationRuntime{filePath: &empty}
	cmd := &cobra.Command{Use: "synth"}
	_, _, err := buildMultipartBody(cmd, rt)
	if err == nil {
		t.Fatal("expected error")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Type != "validation" {
		t.Errorf("expected validation ExitError, got %T: %v", err, err)
	}
}

// TestFileDownload_NoOutputFlag pins the -o footgun fix: file.download is a
// 302-only redirect (x-octo-binary-response for Location surfacing, but no
// consumable body), so the leaf must NOT register --output/-o. Registering it
// would accept the flag and silently write nothing.
func TestFileDownload_NoOutputFlag(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})

	download := findCmd(findCmd(root, "file"), "download")
	if download == nil {
		t.Fatal("file download command not registered")
	}
	if download.Flags().Lookup("output") != nil {
		t.Error("file download: --output must NOT be registered (302-only redirect, -o would silently no-op)")
	}
	if download.Flags().ShorthandLookup("o") != nil {
		t.Error("file download: -o must NOT be registered (302-only redirect, -o would silently no-op)")
	}
}

// TestDocsSceneExport_OutputFlagWritesFileToDisk confirms the fix does not harm
// the W3 export main use case: docs.scene.export delivers a 2xx image body, so
// it keeps --output/-o and the bytes land on disk.
func TestDocsSceneExport_OutputFlagWritesFileToDisk(t *testing.T) {
	wantBytes := []byte("\x89PNG\r\n\x1a\nfake-board")
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(wantBytes)
	})

	export := findCmd(findCmd(root, "docs"), "scene")
	export = findCmd(export, "export")
	if export == nil {
		t.Fatal("docs scene export command not registered")
	}
	if export.Flags().Lookup("output") == nil || export.Flags().ShorthandLookup("o") == nil {
		t.Fatal("docs scene export: expected --output/-o flag (2xx binary body op)")
	}

	dst := filepath.Join(t.TempDir(), "board.png")
	root.SetArgs([]string{"docs", "scene", "export", "abc", "--image-format", "png", "-o", dst})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !bytes.Equal(got, wantBytes) {
		t.Errorf("written bytes = %q, want %q", got, wantBytes)
	}
}

// fallback: a parent command with no RunE silently prints help and exits 0
// when given an unknown token, which can let automation treat a removed or
// mistyped command as a success. Regression test for the removal of
// thread.delete (where `octo-cli thread delete ...` previously printed help).
func TestParentCommand_RejectsUnknownSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"thread + removed leaf (delete)", []string{"thread", "delete", "g", "s"}},
		{"thread + bogus", []string{"thread", "bogus"}},
		{"group + bogus", []string{"group", "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				t.Fatalf("HTTP handler must not be called when subcommand is unknown")
			})
			root.SetArgs(tc.args)
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			err := root.Execute()
			if err == nil {
				t.Fatalf("expected error for %v, got nil; output=%s", tc.args, buf.String())
			}
			if !strings.Contains(err.Error(), "unknown subcommand") {
				t.Errorf("error should mention 'unknown subcommand', got %q", err.Error())
			}
		})
	}
}

// TestHTMLMutationUnwrapFailsOpenOnBodylessSuccess pins the fail-open direction
// of unwrapResponse. The server already committed the mutation, so an empty
// body, a 204, or a bare {"ok":true} must still be reported as success — the
// earlier fail-closed behaviour told the caller a completed delete had failed.
func TestHTMLMutationUnwrapFailsOpenOnBodylessSuccess(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "empty 200", status: 200, body: ""},
		{name: "204 no content", status: 204, body: ""},
		{name: "ok without data", status: 200, body: `{"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				if tt.body != "" {
					w.Header().Set("Content-Type", "application/json")
				}
				w.WriteHeader(tt.status)
				if tt.body != "" {
					_, _ = w.Write([]byte(tt.body))
				}
			})
			root.SetArgs([]string{"html", "rm", "doc-1"})
			if err := root.Execute(); err != nil {
				t.Fatalf("a committed mutation was reported as failed: %v (stderr: %s)", err, tf.ErrOut.String())
			}
			var env map[string]any
			if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
				t.Fatalf("response: %v: %s", err, tf.Out.String())
			}
			if ok, _ := env["ok"].(bool); !ok {
				t.Errorf("envelope not ok: %s", tf.Out.String())
			}
		})
	}
}

// TestHTMLPublishUnwrapRejectsNullAndScalarData pins the fail-closed direction:
// html.publish declares doc_id and slug required, and a null or scalar data
// carries neither. Reporting success there makes a caller persist an empty
// document reference and lose the document it just created.
func TestHTMLPublishUnwrapRejectsNullAndScalarData(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":"oops"}`, `{"data":123}`} {
		t.Run(body, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			root.SetArgs([]string{"html", "publish", "--html", "<p>x</p>"})
			if err := root.Execute(); err == nil {
				t.Fatalf("accepted %s as success: %s", body, tf.Out.String())
			}
			if got := tf.ErrOut.String(); !strings.Contains(got, "RESPONSE_UNWRAP") {
				t.Errorf("error was not RESPONSE_UNWRAP: %s", got)
			}
		})
	}
}

// TestHTMLCreateFailureReportsIdempotencyKey pins that an ambiguous create
// failure surfaces the key it used. Without it the generated key dies with the
// process and a committed-but-unacknowledged create becomes an unreachable
// orphan the caller can neither address nor delete.
func TestHTMLCreateFailureReportsIdempotencyKey(t *testing.T) {
	var sent string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		sent, _ = body["idempotency_key"].(string)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{}`))
	})
	root.SetArgs([]string{"html", "publish", "--html", "<p>x</p>"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected the 502 to fail")
	}
	if sent == "" {
		t.Fatal("no generated key reached the server")
	}
	if got := tf.ErrOut.String(); !strings.Contains(got, sent) {
		t.Errorf("error envelope does not carry the idempotency key %q, so the orphan is unrecoverable: %s", sent, got)
	}
}

// TestHTMLWhitespaceSlugIsNotTreatedAsRepublish pins the unified empty-value
// definition: a whitespace-only slug must not classify as republish and be sent
// as a document reference, which is what the untrimmed variant check did.
func TestHTMLWhitespaceSlugIsNotTreatedAsRepublish(t *testing.T) {
	var body map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"slug":"d_1","doc_id":"d_1"}}`))
	})
	root.SetArgs([]string{"html", "publish", "--data", `{"html":"new","slug":"   "}`})
	err := root.Execute()
	if err == nil {
		if key, _ := body["idempotency_key"].(string); key == "" {
			t.Errorf("whitespace slug was sent as a republish reference with no key: %v", body)
		}
	}
}
