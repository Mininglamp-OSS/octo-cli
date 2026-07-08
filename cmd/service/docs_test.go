package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// The docs service is spec-driven (internal/registry/specs/docs.json) — there is
// no hand-written command code for it. These tests exercise the real embedded
// registry through the same rootWithService harness the other services use, so
// they assert what the generated command tree actually sends on the wire.

// --- command-tree shape ---

// TestDocs_TreeShape confirms the docs subtree and its nested resource groups
// (members, comments, versions, attachments) generate from the dotted
// operationIds without any Go registration code.
func TestDocs_TreeShape(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})

	docs := findCmd(root, "docs")
	if docs == nil {
		t.Fatal("missing docs service command")
	}
	for _, leaf := range []string{"create", "list", "get", "rename", "delete", "forward-grant"} {
		if !contains(childNames(docs), leaf) {
			t.Errorf("docs: missing leaf %q; got %v", leaf, childNames(docs))
		}
	}

	groups := map[string][]string{
		"members":     {"list", "set", "remove"},
		"comments":    {"list", "add", "edit", "delete"},
		"versions":    {"list", "create", "state", "rename", "delete", "restore"},
		"attachments": {"presign", "get", "resolve"},
	}
	for group, leaves := range groups {
		sub := findCmd(docs, group)
		if sub == nil {
			t.Errorf("docs: missing resource group %q", group)
			continue
		}
		for _, leaf := range leaves {
			if !contains(childNames(sub), leaf) {
				t.Errorf("docs %s: missing leaf %q; got %v", group, leaf, childNames(sub))
			}
		}
	}
}

// TestDocs_RegistryShape asserts every docs.* operationId parses into the
// command path the tree generator will build from it (segments after the
// service become nested resource commands, the last is the leaf verb) and that
// the method/path match the verified octo-docs-backend bot routes.
func TestDocs_RegistryShape(t *testing.T) {
	reg := registry.MustNew()

	type want struct{ method, path string }
	cases := map[string]want{
		"docs.create":              {"POST", "/v1/bot/docs"},
		"docs.list":                {"GET", "/v1/bot/docs"},
		"docs.get":                 {"GET", "/v1/bot/docs/{docId}"},
		"docs.rename":              {"PATCH", "/v1/bot/docs/{docId}"},
		"docs.delete":              {"DELETE", "/v1/bot/docs/{docId}"},
		"docs.members.list":        {"GET", "/v1/bot/docs/{docId}/members"},
		"docs.members.set":         {"PUT", "/v1/bot/docs/{docId}/members"},
		"docs.members.remove":      {"DELETE", "/v1/bot/docs/{docId}/members/{uid}"},
		"docs.forward-grant":       {"POST", "/v1/bot/docs/{docId}/forward-grant"},
		"docs.comments.list":       {"GET", "/v1/bot/docs/{docId}/comments"},
		"docs.comments.add":        {"POST", "/v1/bot/docs/{docId}/comments"},
		"docs.comments.edit":       {"PATCH", "/v1/bot/docs/{docId}/comments/{id}"},
		"docs.comments.delete":     {"DELETE", "/v1/bot/docs/{docId}/comments/{id}"},
		"docs.versions.list":       {"GET", "/v1/bot/docs/{docId}/versions"},
		"docs.versions.create":     {"POST", "/v1/bot/docs/{docId}/versions"},
		"docs.versions.state":      {"GET", "/v1/bot/docs/{docId}/versions/{versionId}/state"},
		"docs.versions.rename":     {"PATCH", "/v1/bot/docs/{docId}/versions/{versionId}"},
		"docs.versions.delete":     {"DELETE", "/v1/bot/docs/{docId}/versions/{versionId}"},
		"docs.versions.restore":    {"POST", "/v1/bot/docs/{docId}/versions/{versionId}/restore"},
		"docs.attachments.presign": {"POST", "/v1/bot/docs/{docId}/attachments/presign"},
		"docs.attachments.get":     {"GET", "/v1/bot/docs/{docId}/attachments/{attachId}"},
		"docs.attachments.resolve": {"POST", "/v1/bot/docs/{docId}/attachments/resolve"},
	}

	got := reg.ListOperations("docs")
	if len(got) != len(cases) {
		t.Errorf("docs operation count = %d, want %d", len(got), len(cases))
	}
	for id, w := range cases {
		d, ok := reg.GetOperation(id)
		if !ok {
			t.Errorf("operation %q not found in registry", id)
			continue
		}
		if d.Method != w.method || d.Path != w.path {
			t.Errorf("%s: got %s %s, want %s %s", id, d.Method, d.Path, w.method, w.path)
		}
		// Dotted id must map to "<service> [<resource>...] <verb>".
		segs := strings.Split(id, ".")
		if segs[0] != "docs" {
			t.Errorf("%s: first segment must be the service; got %q", id, segs[0])
		}
	}
}

// TestDocs_SpaceHeaderDeclaredFalse confirms the docs spec declares the bot
// mount's server-resolved space (x-octo-space-header:false), matching bot.json.
func TestDocs_SpaceHeaderDeclaredFalse(t *testing.T) {
	reg := registry.MustNew()
	d, ok := reg.GetOperation("docs.create")
	if !ok {
		t.Fatal("docs.create not in registry")
	}
	if d.SpaceHeader {
		t.Error("docs spec must set x-octo-space-header:false (bot mount server-resolves the space)")
	}
	if d.BaseURLEnv != "OCTO_API_BASE_URL" {
		t.Errorf("docs base-url env = %q, want OCTO_API_BASE_URL", d.BaseURLEnv)
	}
}

// TestDocs_NoPagination confirms none of the docs list ops declare
// x-octo-pagination: their envelopes ({total,items} / {items,nextCursor}) do
// not match the engine's {data,pagination} walker, so --page-all is not offered.
func TestDocs_NoPagination(t *testing.T) {
	reg := registry.MustNew()
	for _, id := range []string{"docs.list", "docs.comments.list", "docs.versions.list"} {
		d, ok := reg.GetOperation(id)
		if !ok {
			t.Fatalf("%s not in registry", id)
		}
		if d.Pagination != nil {
			t.Errorf("%s must not declare pagination (non-standard envelope)", id)
		}
	}
	// And the generated command must not expose --page-all.
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})
	list := findCmd(findCmd(root, "docs"), "list")
	if list == nil {
		t.Fatal("docs list command missing")
	}
	if list.Flags().Lookup("page-all") != nil {
		t.Error("docs list must not have --page-all")
	}
}

// --- operation execution ---

// TestDocsCreate_PostsBodyNoSpaceHeader checks docs.create hits POST /v1/bot/docs
// with the promoted body field, carries the bearer token, and sends no
// X-Space-Id even when the active credential carries a SpaceID — the docs bot
// mount resolves the space server-side (x-octo-space-header:false), so the
// header must be gated off rather than merely absent because the test bot has no
// space. The companion assertion below proves the same spaced credential DOES
// send X-Space-Id on a default/true operation, so the gating is real in both
// directions and no existing service silently loses the header.
func TestDocsCreate_PostsBodyNoSpaceHeader(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotSpace string
	var gotBody map[string]any
	root, _, _ := rootWithServiceSpaced(t, "space-1", func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotSpace = r.Header.Get("X-Space-Id")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"d1","title":"Runbook"}`))
	})
	root.SetArgs([]string{"docs", "create", "--title", "Runbook", "--folderId", "f_1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/v1/bot/docs" {
		t.Errorf("got %s %s, want POST /v1/bot/docs", gotMethod, gotPath)
	}
	if gotBody["title"] != "Runbook" || gotBody["folderId"] != "f_1" {
		t.Errorf("body = %v", gotBody)
	}
	if gotAuth != "Bearer app_test" {
		t.Errorf("Authorization = %q, want Bearer app_test", gotAuth)
	}
	if gotSpace != "" {
		t.Errorf("X-Space-Id must not be sent for docs even with a spaced credential; got %q", gotSpace)
	}
}

// TestSpacedCredential_SendsSpaceHeaderOnDefaultOp is the companion to the docs
// suppression test: with the SAME spaced credential, an operation whose spec
// declares x-octo-space-header:true (matter, the canonical space-scoped domain)
// still sends X-Space-Id. This guards against the gating over-reaching and
// stripping the header from services that must keep it — only an explicit
// x-octo-space-header:false suppresses it.
func TestSpacedCredential_SendsSpaceHeaderOnDefaultOp(t *testing.T) {
	var gotSpace string
	root, _, _ := rootWithServiceSpaced(t, "space-1", func(w http.ResponseWriter, r *http.Request) {
		gotSpace = r.Header.Get("X-Space-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"matter":{"id":"m1"}}`))
	})
	root.SetArgs([]string{"matter", "create", "--title", "Case"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q, want space-1 for a space-header:true op", gotSpace)
	}
}

// TestDocsList_QueryParamsFromFlags checks the page-based list flags land in the
// query string.
func TestDocsList_QueryParamsFromFlags(t *testing.T) {
	var gotQuery, gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
	})
	root.SetArgs([]string{"docs", "list", "--page", "2", "--pageSize", "50", "--sort", "updatedAt:asc"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/bot/docs" {
		t.Errorf("path = %s", gotPath)
	}
	for _, want := range []string{"page=2", "pageSize=50", "sort=updatedAt%3Aasc"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}
}

// TestDocsGet_PathArgInURL checks the single positional arg lands in the path.
func TestDocsGet_PathArgInURL(t *testing.T) {
	var gotMethod, gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"abc"}`))
	})
	root.SetArgs([]string{"docs", "get", "abc"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/bot/docs/abc" {
		t.Errorf("got %s %s, want GET /v1/bot/docs/abc", gotMethod, gotPath)
	}
}

// TestDocsMembersSet_PutUpsertBody checks the PUT-upsert method, path, and
// {uid, role} body.
func TestDocsMembersSet_PutUpsertBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	root.SetArgs([]string{"docs", "members", "set", "d1", "--uid", "u9", "--role", "writer"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "PUT" || gotPath != "/v1/bot/docs/d1/members" {
		t.Errorf("got %s %s, want PUT /v1/bot/docs/d1/members", gotMethod, gotPath)
	}
	if gotBody["uid"] != "u9" || gotBody["role"] != "writer" {
		t.Errorf("body = %v", gotBody)
	}
}

// TestDocsMembersRemove_TwoPathArgs checks both positional args land in the path.
func TestDocsMembersRemove_TwoPathArgs(t *testing.T) {
	var gotMethod, gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	root.SetArgs([]string{"docs", "members", "remove", "d1", "u9"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/v1/bot/docs/d1/members/u9" {
		t.Errorf("got %s %s, want DELETE /v1/bot/docs/d1/members/u9", gotMethod, gotPath)
	}
}

// TestDocsCommentsAdd_ReplyBody checks a reply carries {body, parentId}.
func TestDocsCommentsAdd_ReplyBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":42}`))
	})
	root.SetArgs([]string{"docs", "comments", "add", "d1", "--body", "Agreed", "--parentId", "7"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/bot/docs/d1/comments" {
		t.Errorf("path = %s", gotPath)
	}
	if gotBody["body"] != "Agreed" {
		t.Errorf("body field = %v", gotBody["body"])
	}
	// Promoted integer flag must serialize as a JSON number.
	if pid, ok := gotBody["parentId"].(float64); !ok || pid != 7 {
		t.Errorf("parentId = %v (%T), want 7", gotBody["parentId"], gotBody["parentId"])
	}
}

// TestDocsCommentsDelete_HardQuery checks the hard-delete query flag and the two
// path args.
func TestDocsCommentsDelete_HardQuery(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"id":5}`))
	})
	root.SetArgs([]string{"docs", "comments", "delete", "d1", "5", "--hard", "1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/v1/bot/docs/d1/comments/5" {
		t.Errorf("got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "hard=1") {
		t.Errorf("query %q missing hard=1", gotQuery)
	}
}

// TestDocsVersionsRestore_PostNestedPath checks a two-path-arg nested POST.
func TestDocsVersionsRestore_PostNestedPath(t *testing.T) {
	var gotMethod, gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"restoredFrom":3,"newDocVersionSeq":9}`))
	})
	root.SetArgs([]string{"docs", "versions", "restore", "d1", "3"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/v1/bot/docs/d1/versions/3/restore" {
		t.Errorf("got %s %s, want POST /v1/bot/docs/d1/versions/3/restore", gotMethod, gotPath)
	}
}

// TestDocsAttachmentsResolve_ArrayBody checks the string-array body field
// serializes as a JSON array.
func TestDocsAttachmentsResolve_ArrayBody(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"items":[],"notFound":[]}`))
	})
	root.SetArgs([]string{"docs", "attachments", "resolve", "d1", "--attachIds", "a1", "--attachIds", "a2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/bot/docs/d1/attachments/resolve" {
		t.Errorf("path = %s", gotPath)
	}
	arr, ok := gotBody["attachIds"].([]any)
	if !ok || len(arr) != 2 || arr[0] != "a1" || arr[1] != "a2" {
		t.Errorf("attachIds = %v", gotBody["attachIds"])
	}
}

// TestDocsAttachmentsPresign_BodyTypes checks the presign body promotes fileName
// and mime as strings and sizeBytes as a JSON number.
func TestDocsAttachmentsPresign_BodyTypes(t *testing.T) {
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"attachId":"at1","uploadUrl":"https://x"}`))
	})
	root.SetArgs([]string{
		"docs", "attachments", "presign", "d1",
		"--fileName", "report.pdf", "--mime", "application/pdf", "--sizeBytes", "2048",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["fileName"] != "report.pdf" || gotBody["mime"] != "application/pdf" {
		t.Errorf("body = %v", gotBody)
	}
	if sz, ok := gotBody["sizeBytes"].(float64); !ok || sz != 2048 {
		t.Errorf("sizeBytes = %v (%T), want 2048", gotBody["sizeBytes"], gotBody["sizeBytes"])
	}
}
