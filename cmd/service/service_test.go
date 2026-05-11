package service

import (
	"bytes"
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
