package service

import (
	"bytes"
	"context"
	"encoding/json"
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
