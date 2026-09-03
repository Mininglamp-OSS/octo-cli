package service

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
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
	for _, leaf := range []string{"create", "list", "search", "get", "rename", "delete", "forward-grant"} {
		if !contains(childNames(docs), leaf) {
			t.Errorf("docs: missing leaf %q; got %v", leaf, childNames(docs))
		}
	}

	groups := map[string][]string{
		"content":     {"get", "edit"},
		"sheet":       {"get", "edit"},
		"scene":       {"get", "edit", "export"},
		"members":     {"list", "set", "remove"},
		"share":       {"get", "set"},
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
		"docs.search":              {"POST", "/v1/bot/docs/search"},
		"docs.get":                 {"GET", "/v1/bot/docs/{docId}"},
		"docs.rename":              {"PATCH", "/v1/bot/docs/{docId}"},
		"docs.delete":              {"DELETE", "/v1/bot/docs/{docId}"},
		"docs.content.get":         {"GET", "/v1/bot/docs/{docId}/content"},
		"docs.content.edit":        {"PATCH", "/v1/bot/docs/{docId}/content"},
		"docs.sheet.get":           {"GET", "/v1/bot/docs/{docId}/sheet"},
		"docs.sheet.edit":          {"PATCH", "/v1/bot/docs/{docId}/sheet"},
		"docs.scene.get":           {"GET", "/v1/bot/docs/{docId}/scene"},
		"docs.scene.edit":          {"PATCH", "/v1/bot/docs/{docId}/scene"},
		"docs.members.list":        {"GET", "/v1/bot/docs/{docId}/members"},
		"docs.members.set":         {"PUT", "/v1/bot/docs/{docId}/members"},
		"docs.members.remove":      {"DELETE", "/v1/bot/docs/{docId}/members/{uid}"},
		"docs.share.get":           {"GET", "/v1/bot/docs/{docId}/share"},
		"docs.share.set":           {"PUT", "/v1/bot/docs/{docId}/share"},
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
		"docs.scene.export":        {"GET", "/v1/bot/docs/{docId}/export"},
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

	list, ok := reg.GetOperation("docs.members.list")
	if !ok || list.ResponseSchema == nil {
		t.Fatal("docs.members.list response schema missing")
	}
	items, ok := list.ResponseSchema.Properties["items"]
	if !ok || items.Items == nil {
		t.Fatalf("docs.members.list items schema = %#v, want array item schema", items)
	}
	principalSpaceID, ok := items.Items.Properties["principalSpaceId"]
	if !ok || principalSpaceID.Type != "string" {
		t.Fatalf("docs.members.list item principalSpaceId = %#v, want string", principalSpaceID)
	}
}

func TestDocsMembersSet_PrincipalSpaceIDIsOptionalBodyField(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantSpace string
	}{
		{name: "defaults to authenticated Bot Space", args: []string{"docs", "members", "set", "doc-1", "--uid", "user-1", "--role", "writer"}},
		{name: "sends explicit principal Space", args: []string{"docs", "members", "set", "doc-1", "--uid", "user-1", "--role", "writer", "--principal-space-id", "space-2"}, wantSpace: "space-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]any
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			got, present := body["principalSpaceId"]
			if tc.wantSpace == "" {
				if present {
					t.Fatalf("omitted --principal-space-id must leave Bot Space resolution to the backend: %v", body)
				}
				return
			}
			if got != tc.wantSpace {
				t.Fatalf("principalSpaceId = %v, want %q", got, tc.wantSpace)
			}
		})
	}
}

func TestDocsMembersRemove_RequiresAndSendsPrincipalSpaceID(t *testing.T) {
	t.Run("requires principal Space", func(t *testing.T) {
		requests := 0
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			requests++
		})
		root.SetArgs([]string{"docs", "members", "remove", "doc-1", "user-1"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "required flag") {
			t.Fatalf("missing --principal-space-id error = %v, want required flag error", err)
		}
		if requests != 0 {
			t.Fatalf("missing required flag sent %d requests, want 0", requests)
		}
	})

	t.Run("sends exact principal Space", func(t *testing.T) {
		var gotQuery url.Values
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.Query()
			_, _ = w.Write([]byte(`{"ok":true}`))
		})
		root.SetArgs([]string{"docs", "members", "remove", "doc-1", "user-1", "--principal-space-id", "space-2"})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if got := gotQuery.Get("principalSpaceId"); got != "space-2" {
			t.Fatalf("principalSpaceId query = %q, want space-2", got)
		}
	})
}

// TestDocsSceneExport_ImageFormatFlagNoGlobalShadow pins the fix for the
// `--format` collision: docs.scene.export's `format` query param carries
// x-octo-flag "image-format", so its CLI flag is --image-format (leaving the
// global persistent output --format reachable) while the wire param stays
// ?format=. The leaf must NOT declare a local --format flag.
func TestDocsSceneExport_ImageFormatFlagNoGlobalShadow(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})

	docs := findCmd(root, "docs")
	scene := findCmd(docs, "scene")
	if scene == nil {
		t.Fatal("missing docs scene resource group")
	}
	export := findCmd(scene, "export")
	if export == nil {
		t.Fatal("missing docs scene export leaf")
	}
	if export.LocalFlags().Lookup("image-format") == nil {
		t.Error("docs scene export: expected local --image-format flag")
	}
	if export.LocalFlags().Lookup("format") != nil {
		t.Error("docs scene export: --format must NOT be a local flag (it would shadow the global output --format)")
	}
}

// TestDocsSceneExport_ImageFormatMapsToWireFormat drives the command and asserts
// --image-format lands on the wire as ?format= (the api name is preserved).
func TestDocsSceneExport_ImageFormatMapsToWireFormat(t *testing.T) {
	var gotPath, gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte("<svg/>"))
	})
	root.SetArgs([]string{"docs", "scene", "export", "abc", "--image-format", "svg"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/bot/docs/abc/export" {
		t.Errorf("path = %s, want /v1/bot/docs/abc/export", gotPath)
	}
	if !strings.Contains(gotQuery, "format=svg") {
		t.Errorf("query %q missing format=svg", gotQuery)
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

func TestDocsSearch_FlagsAndRequestBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"items":[{"docId":"d1","title":"Plan","docType":"doc","updatedAt":123,"spaceId":"s1","highlight":"roadmap"}]}`))
	})

	search := findCmd(findCmd(root, "docs"), "search")
	if search == nil {
		t.Fatal("docs search command missing")
	}
	for _, name := range []string{"keyword", "doc-type", "cursor", "page-size", "page-all", "page-limit"} {
		if search.Flags().Lookup(name) == nil {
			t.Errorf("docs search missing --%s", name)
		}
	}
	if search.Flags().Lookup("q") != nil || search.Flags().Lookup("docType") != nil {
		t.Error("docs search must expose caller-facing flag names, not wire names")
	}

	root.SetArgs([]string{"docs", "search", "--keyword", "roadmap", "--doc-type", "doc", "--doc-type", "board", "--doc-type", "html", "--cursor", "c1", "--page-size", "25"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/bot/docs/search" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["q"] != "roadmap" || gotBody["cursor"] != "c1" || gotBody["pageSize"] != float64(25) {
		t.Errorf("body = %#v", gotBody)
	}
	types, ok := gotBody["docType"].([]any)
	if !ok || len(types) != 3 || types[0] != "doc" || types[1] != "board" || types[2] != "html" {
		t.Errorf("docType = %#v", gotBody["docType"])
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Total int `json:"total"`
			Items []struct {
				DocID string `json:"docId"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK || env.Data.Total != 1 || len(env.Data.Items) != 1 || env.Data.Items[0].DocID != "d1" {
		t.Errorf("single-page response did not preserve backend object: %+v", env)
	}
}

func TestDocsSearch_PageAllUsesBodyCursorAndStopsWithoutNextCursor(t *testing.T) {
	pages := []string{
		`{"total":3,"items":[{"docId":"d1"},{"docId":"d2"}],"nextCursor":"next-2"}`,
		`{"total":3,"items":[{"docId":"d3"}]}`,
	}
	var cursors []string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cursor, _ := body["cursor"].(string)
		cursors = append(cursors, cursor)
		if queryCursor := r.URL.Query().Get("cursor"); queryCursor != "" {
			t.Errorf("cursor leaked into query: %q", queryCursor)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(pages[len(cursors)-1]))
	})
	root.SetArgs([]string{"docs", "search", "--keyword", "plan", "--page-all"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "next-2" {
		t.Fatalf("body cursor progression = %v", cursors)
	}
	var env struct {
		Data []struct {
			DocID string `json:"docId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if len(env.Data) != 3 {
		t.Errorf("merged data = %+v", env.Data)
	}
}

func TestDocsSearch_PageAllRejectsRepeatedCursor(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":99,"items":[{"docId":"d"}],"nextCursor":"same"}`))
	})
	root.SetArgs([]string{"docs", "search", "--keyword", "plan", "--page-all", "--page-limit", "10"})
	err := root.Execute()
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != "PAGINATION_LOOP" {
		t.Fatalf("error = %#v, want PAGINATION_LOOP", err)
	}
	if calls != 2 {
		t.Errorf("repeated cursor should fail after 2 requests, got %d", calls)
	}
}

func TestDocsSearch_FinalPageStopsWithoutCursor(t *testing.T) {
	calls := 0
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"items":[{"docId":"d1"}],"nextCursor":""}`))
	})
	root.SetArgs([]string{"docs", "search", "--keyword", "plan", "--page-all"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if calls != 1 {
		t.Fatalf("final page should stop after 1 request, got %d", calls)
	}
	var env struct {
		Data []struct {
			DocID string `json:"docId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].DocID != "d1" {
		t.Errorf("merged data = %+v", env.Data)
	}
}

func TestDocsSearch_PageLimitPreemptsLoopDetection(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":99,"items":[{"docId":"d"}],"nextCursor":"same"}`))
	})
	root.SetArgs([]string{"docs", "search", "--keyword", "plan", "--page-all", "--page-limit", "1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("page-limit boundary must not report a loop: %v", err)
	}
	if calls != 1 {
		t.Fatalf("page-limit should stop after 1 request, got %d", calls)
	}
}

func TestDocsSearch_PageAllSeedsManualCursor(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["cursor"] != "same" {
			t.Errorf("cursor = %#v, want same", body["cursor"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total":1,"items":[{"docId":"d1"}],"nextCursor":"same"}`))
	})
	root.SetArgs([]string{"docs", "search", "--keyword", "plan", "--cursor", "same", "--page-all"})
	err := root.Execute()
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != "PAGINATION_LOOP" {
		t.Fatalf("error = %#v, want PAGINATION_LOOP", err)
	}
	if calls != 1 {
		t.Fatalf("manual repeated cursor should fail after 1 request, got %d", calls)
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

// TestMessageSend_SendsSpaceHeader is the regression guard for the fix: the
// message spec declares x-octo-space-header:true, so a spaced credential MUST
// send X-Space-Id on a message op. sendMessage for a multi-space bot uses the
// header as the DM multi-space selection hint; dropping it silently
// mis-attributes the message. This uses message (a real product service),
// unlike the matter companion above, so a future flip of the message flag is
// caught here rather than masked by the always-true matter domain.
func TestMessageSend_SendsSpaceHeader(t *testing.T) {
	var gotSpace, gotPath string
	root, _, _ := rootWithServiceSpaced(t, "space-1", func(w http.ResponseWriter, r *http.Request) {
		gotSpace = r.Header.Get("X-Space-Id")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message_id":1,"message_seq":1}`))
	})
	root.SetArgs([]string{"message", "send", "--data", `{"channel_id":"c1","channel_type":1,"payload":{"type":1,"content":"hi"}}`})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/v1/bot/sendMessage" {
		t.Errorf("path = %q, want /v1/bot/sendMessage", gotPath)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q, want space-1 — message must keep sending the header for multi-space DM selection", gotSpace)
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
	var gotMethod, gotPath, gotSpace string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotSpace = r.URL.Query().Get("principalSpaceId")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	root.SetArgs([]string{"docs", "members", "remove", "d1", "u9", "--principal-space-id", "s1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "DELETE" || gotPath != "/v1/bot/docs/d1/members/u9" {
		t.Errorf("got %s %s, want DELETE /v1/bot/docs/d1/members/u9", gotMethod, gotPath)
	}
	if gotSpace != "s1" {
		t.Errorf("principalSpaceId = %q, want s1", gotSpace)
	}
}

// TestDocsShareGet_ReadPath checks docs.share.get is a plain GET on the
// /share sub-resource with the docId in the path and no request body — a read
// must not ship a payload, so the handler asserts the received body is empty.
func TestDocsShareGet_ReadPath(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody []byte
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","shareScope":"restricted","shareRole":"read"}`))
	})
	root.SetArgs([]string{"docs", "share", "get", "d1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/bot/docs/d1/share" {
		t.Errorf("got %s %s, want GET /v1/bot/docs/d1/share", gotMethod, gotPath)
	}
	if len(gotBody) != 0 {
		t.Errorf("GET must send an empty request body; got %q", gotBody)
	}
}

// TestDocsShareSet_RestrictedBody checks --scope restricted maps to a PUT on
// /share with the byte-exact {shareScope} wire key and no shareRole (a
// restricted doc ignores the role; the backend normalizes it to read).
func TestDocsShareSet_RestrictedBody(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","shareScope":"restricted","shareRole":"read"}`))
	})
	root.SetArgs([]string{"docs", "share", "set", "d1", "--scope", "restricted"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "PUT" || gotPath != "/v1/bot/docs/d1/share" {
		t.Errorf("got %s %s, want PUT /v1/bot/docs/d1/share", gotMethod, gotPath)
	}
	if gotBody["shareScope"] != "restricted" {
		t.Errorf("body shareScope = %v, want restricted", gotBody["shareScope"])
	}
	if _, present := gotBody["shareRole"]; present {
		t.Errorf("body must omit shareRole when --role is not passed; got %v", gotBody)
	}
}

// TestDocsShareSet_AnyoneEditBody checks --scope anyone_in_space --role edit
// maps both flags to their wire keys (shareScope / shareRole), proving the
// x-octo-flag aliases (--scope, --role) do not leak into the body.
func TestDocsShareSet_AnyoneEditBody(t *testing.T) {
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","shareScope":"anyone_in_space","shareRole":"edit"}`))
	})
	root.SetArgs([]string{"docs", "share", "set", "d1", "--scope", "anyone_in_space", "--role", "edit"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["shareScope"] != "anyone_in_space" || gotBody["shareRole"] != "edit" {
		t.Errorf("body = %v, want {shareScope:anyone_in_space, shareRole:edit}", gotBody)
	}
	// The flag aliases must never appear as body keys.
	if _, bad := gotBody["scope"]; bad {
		t.Errorf("body leaked the --scope flag alias as a wire key: %v", gotBody)
	}
	if _, bad := gotBody["role"]; bad {
		t.Errorf("body leaked the --role flag alias as a wire key: %v", gotBody)
	}
}

// TestDocsShareSet_FlagAliases confirms the leaf exposes --scope / --role (from
// x-octo-flag) and NOT the camelCase property names, while --data stays
// available for the raw-body escape hatch.
func TestDocsShareSet_FlagAliases(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {})
	docs := findCmd(root, "docs")
	share := findCmd(docs, "share")
	if share == nil {
		t.Fatal("missing docs share resource group")
	}
	set := findCmd(share, "set")
	if set == nil {
		t.Fatal("missing docs share set leaf")
	}
	for _, want := range []string{"scope", "role", "data"} {
		if set.Flags().Lookup(want) == nil {
			t.Errorf("docs share set: expected --%s flag", want)
		}
	}
	for _, bad := range []string{"shareScope", "shareRole", "share-scope", "share-role"} {
		if set.Flags().Lookup(bad) != nil {
			t.Errorf("docs share set: --%s must NOT exist (aliased to --scope/--role)", bad)
		}
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

// TestDocsCommentsAdd_RootAnchorText checks a bot root comment carries the
// anchorText resolution inputs {body, anchorText, blockPath, occurrence} in the
// body, with occurrence promoted to a JSON number.
func TestDocsCommentsAdd_RootAnchorText(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":7}`))
	})
	root.SetArgs([]string{
		"docs", "comments", "add", "d1",
		"--body", "please clarify",
		"--anchorText", "the quoted span",
		"--blockPath", "0,2",
		"--occurrence", "2",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/v1/bot/docs/d1/comments" {
		t.Errorf("got %s %s, want POST /v1/bot/docs/d1/comments", gotMethod, gotPath)
	}
	if gotBody["body"] != "please clarify" {
		t.Errorf("body = %v", gotBody["body"])
	}
	if gotBody["anchorText"] != "the quoted span" {
		t.Errorf("anchorText = %v", gotBody["anchorText"])
	}
	if gotBody["blockPath"] != "0,2" {
		t.Errorf("blockPath = %v", gotBody["blockPath"])
	}
	// Promoted integer flag must serialize as a JSON number.
	if occ, ok := gotBody["occurrence"].(float64); !ok || occ != 2 {
		t.Errorf("occurrence = %v (%T), want 2", gotBody["occurrence"], gotBody["occurrence"])
	}
	// No anchorStart/anchorEnd on the text-resolution path.
	if _, ok := gotBody["anchorStart"]; ok {
		t.Errorf("anchorStart should be absent, got %v", gotBody["anchorStart"])
	}
}

// TestDocsCommentsAdd_RootLegacyAnchors checks the legacy explicit-anchor path
// still carries {anchorStart, anchorEnd} unchanged.
func TestDocsCommentsAdd_RootLegacyAnchors(t *testing.T) {
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":9}`))
	})
	root.SetArgs([]string{
		"docs", "comments", "add", "d1",
		"--body", "note",
		"--anchorStart", "AA==",
		"--anchorEnd", "Ag==",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotBody["anchorStart"] != "AA==" || gotBody["anchorEnd"] != "Ag==" {
		t.Errorf("legacy anchors = start:%v end:%v", gotBody["anchorStart"], gotBody["anchorEnd"])
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

// TestDocsContentGet_ReadsBodyAndBaseVersion checks docs.content.get hits
// GET /v1/bot/docs/{docId}/content (reader), sends no body, and surfaces the
// backend's {doc, baseVersion} response through the success envelope so the
// caller can capture the base-version token for a follow-up edit.
func TestDocsContentGet_ReadsBodyAndBaseVersion(t *testing.T) {
	var gotMethod, gotPath string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"d1","doc":{"type":"doc","content":[]},"schemaVersion":3,"baseVersion":"BV_ABC=="}`))
	})
	root.SetArgs([]string{"docs", "content", "get", "d1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/bot/docs/d1/content" {
		t.Errorf("got %s %s, want GET /v1/bot/docs/d1/content", gotMethod, gotPath)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			BaseVersion string `json:"baseVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK || env.Data.BaseVersion != "BV_ABC==" {
		t.Errorf("expected baseVersion BV_ABC== in envelope, got %+v", env)
	}
}

// TestDocsContentEdit_SendsOpsBatchAndIfMatch checks docs.content.edit hits
// PATCH /v1/bot/docs/{docId}/content, carries the ops batch (passed via the
// generic --data escape hatch) as a JSON array in the body, and sends the
// base-version token as the If-Match header — the spec-declared header
// capability wiring --base-version to If-Match, not a body/query field.
func TestDocsContentEdit_SendsOpsBatchAndIfMatch(t *testing.T) {
	var gotMethod, gotPath, gotIfMatch string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIfMatch = r.Header.Get("If-Match")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","bytes":128,"baseVersion":"BV_NEXT==","newDocVersionSeq":9}`))
	})
	ops := `{"ops":[{"type":"insert","at":{"path":[],"position":"inside_end"},"content":[{"type":"paragraph","content":[{"type":"text","text":"hi"}]}]}]}`
	root.SetArgs([]string{"docs", "content", "edit", "d1", "--base-version", "BV_ABC==", "--data", ops})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "PATCH" || gotPath != "/v1/bot/docs/d1/content" {
		t.Errorf("got %s %s, want PATCH /v1/bot/docs/d1/content", gotMethod, gotPath)
	}
	if gotIfMatch != "BV_ABC==" {
		t.Errorf("If-Match = %q, want BV_ABC== (base version must reach the wire as the If-Match header)", gotIfMatch)
	}
	opsArr, ok := gotBody["ops"].([]any)
	if !ok || len(opsArr) != 1 {
		t.Fatalf("ops = %v, want a 1-element array", gotBody["ops"])
	}
	op0, ok := opsArr[0].(map[string]any)
	if !ok || op0["type"] != "insert" {
		t.Errorf("ops[0] = %v, want an insert op", opsArr[0])
	}
	// The base version travels in the header, not the JSON body.
	if _, present := gotBody["baseVersion"]; present {
		t.Errorf("baseVersion must not be duplicated into the JSON body; got %v", gotBody["baseVersion"])
	}
}

// TestDocsContentEdit_RequiresBaseVersion confirms --base-version is required:
// the command fails before any request when the base-version token is missing,
// mirroring the backend's mandatory optimistic-concurrency guard.
func TestDocsContentEdit_RequiresBaseVersion(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	root.SetArgs([]string{"docs", "content", "edit", "d1", "--data", `{"ops":[]}`})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --base-version is omitted")
	}
	if called {
		t.Error("server must not be called when required --base-version is missing")
	}
}

// TestDocsSheetGet_WholeSheet checks docs.sheet.get hits
// GET /v1/bot/docs/{docId}/sheet (reader), sends no body and no pagination
// query when neither flag is set, and surfaces {sheetCells, sheetDims,
// baseVersion} through the success envelope.
func TestDocsSheetGet_WholeSheet(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"d1","sheetCells":{"default!0:0":{"v":"A1"}},"sheetDims":{"c0":120},"baseVersion":"BV_ABC=="}`))
	})
	root.SetArgs([]string{"docs", "sheet", "get", "d1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/bot/docs/d1/sheet" {
		t.Errorf("got %s %s, want GET /v1/bot/docs/d1/sheet", gotMethod, gotPath)
	}
	// No pagination flags set -> no limit/cursor on the wire (whole-sheet read).
	if strings.Contains(gotQuery, "limit") || strings.Contains(gotQuery, "cursor") {
		t.Errorf("query = %q, want no limit/cursor for a whole-sheet read", gotQuery)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			BaseVersion string `json:"baseVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK || env.Data.BaseVersion != "BV_ABC==" {
		t.Errorf("expected baseVersion BV_ABC== in envelope, got %+v", env)
	}
}

// TestDocsSheetGet_Paginated checks that --limit and --cursor reach the wire as
// query parameters (opting into a paged read), so a caller can page an oversized
// sheet that a whole-sheet read would reject with 413.
func TestDocsSheetGet_Paginated(t *testing.T) {
	var gotQuery url.Values
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"d1","sheetCells":{},"baseVersion":"BV==","hasMore":true,"nextCursor":"CURS_NEXT"}`))
	})
	root.SetArgs([]string{"docs", "sheet", "get", "d1", "--limit", "500", "--cursor", "CURS_PREV"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotQuery.Get("limit") != "500" {
		t.Errorf("limit = %q, want 500", gotQuery.Get("limit"))
	}
	if gotQuery.Get("cursor") != "CURS_PREV" {
		t.Errorf("cursor = %q, want CURS_PREV", gotQuery.Get("cursor"))
	}
}

// TestDocsSheetEdit_SendsCellsBatchAndIfMatch checks docs.sheet.edit hits
// PATCH /v1/bot/docs/{docId}/sheet, carries the cells batch (via --data) as a
// JSON object in the body, and sends the base-version token as the If-Match
// header (the --base-version flag wired to If-Match, not a body/query field).
func TestDocsSheetEdit_SendsCellsBatchAndIfMatch(t *testing.T) {
	var gotMethod, gotPath, gotIfMatch string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIfMatch = r.Header.Get("If-Match")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","bytes":64,"baseVersion":"BV_NEXT==","newDocVersionSeq":4}`))
	})
	cells := `{"cells":{"default!0:0":{"v":"hi"},"default!1:0":null}}`
	root.SetArgs([]string{"docs", "sheet", "edit", "d1", "--base-version", "BV_ABC==", "--data", cells})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "PATCH" || gotPath != "/v1/bot/docs/d1/sheet" {
		t.Errorf("got %s %s, want PATCH /v1/bot/docs/d1/sheet", gotMethod, gotPath)
	}
	if gotIfMatch != "BV_ABC==" {
		t.Errorf("If-Match = %q, want BV_ABC== (base version must reach the wire as the If-Match header)", gotIfMatch)
	}
	cellsMap, ok := gotBody["cells"].(map[string]any)
	if !ok || len(cellsMap) != 2 {
		t.Fatalf("cells = %v, want a 2-entry object", gotBody["cells"])
	}
	set0, ok := cellsMap["default!0:0"].(map[string]any)
	if !ok || set0["v"] != "hi" {
		t.Errorf("cells[default!0:0] = %v, want {v:hi}", cellsMap["default!0:0"])
	}
	if v, present := cellsMap["default!1:0"]; !present || v != nil {
		t.Errorf("cells[default!1:0] = %v, want an explicit null (delete)", cellsMap["default!1:0"])
	}
	// The base version travels in the header, not the JSON body.
	if _, present := gotBody["baseVersion"]; present {
		t.Errorf("baseVersion must not be duplicated into the JSON body; got %v", gotBody["baseVersion"])
	}
}

// TestDocsSheetEdit_SendsP0ResourcesAndIfMatch pins the generic --data transport:
// freeze, filter and single/multi dropdown payloads reach one PATCH with the
// optimistic-concurrency header. Schema key presence is tested by the registry
// loader test; this request schema intentionally remains an open object.
func TestDocsSheetEdit_SendsP0ResourcesAndIfMatch(t *testing.T) {
	var gotMethod, gotPath, gotIfMatch string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIfMatch = r.Header.Get("If-Match")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"docId":"d1","bytes":512,"baseVersion":"BV_NEXT==","newDocVersionSeq":5}`))
	})
	payload := `{
		"freeze":{
			"default":{"startRow":1,"startColumn":1,"xSplit":1,"ySplit":1},
			"rowOnly":{"startRow":1,"startColumn":-1,"xSplit":0,"ySplit":1},
			"columnOnly":{"startRow":-1,"startColumn":1,"xSplit":1,"ySplit":0}
		},
		"filters":{"default":{"ref":{"startRow":0,"startColumn":0,"endRow":20,"endColumn":1},"filterColumns":[{"colId":1,"filters":{"filters":["待处理"]}}]}},
		"dataValidations":{"default":[
			{"uid":"single","type":"list","formula1":"[\"待处理\",\"已完成\"]","formula2":"#E4F4FE,#EFFBD0","showDropDown":true,"renderMode":2,"ranges":[{"startRow":1,"startColumn":0,"endRow":20,"endColumn":0}]},
			{"uid":"multiple","type":"listMultiple","formula1":"[\"待处理\",\"已完成\"]","formula2":"#E4F4FE,#EFFBD0","showDropDown":true,"renderMode":2,"ranges":[{"startRow":1,"startColumn":1,"endRow":20,"endColumn":1}]}
		]}
	}`
	root.SetArgs([]string{"docs", "sheet", "edit", "d1", "--base-version", "BV_P0==", "--data", payload})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/v1/bot/docs/d1/sheet" {
		t.Fatalf("got %s %s, want PATCH /v1/bot/docs/d1/sheet", gotMethod, gotPath)
	}
	if gotIfMatch != "BV_P0==" {
		t.Fatalf("If-Match = %q, want BV_P0==", gotIfMatch)
	}
	freeze, ok := gotBody["freeze"].(map[string]any)
	if !ok {
		t.Fatalf("freeze = %#v, want object", gotBody["freeze"])
	}
	wantFreeze := map[string]map[string]float64{
		"default":    {"startRow": 1, "startColumn": 1, "xSplit": 1, "ySplit": 1},
		"rowOnly":    {"startRow": 1, "startColumn": -1, "xSplit": 0, "ySplit": 1},
		"columnOnly": {"startRow": -1, "startColumn": 1, "xSplit": 1, "ySplit": 0},
	}
	for logicalID, wantFields := range wantFreeze {
		gotConfig, ok := freeze[logicalID].(map[string]any)
		if !ok {
			t.Errorf("freeze.%s = %#v, want object", logicalID, freeze[logicalID])
			continue
		}
		for field, want := range wantFields {
			if got := gotConfig[field]; got != want {
				t.Errorf("freeze.%s.%s = %#v, want %v", logicalID, field, got, want)
			}
		}
	}
	filters, ok := gotBody["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters = %#v, want default filter", gotBody["filters"])
	}
	filterDefault, ok := filters["default"].(map[string]any)
	if !ok {
		t.Fatalf("filters.default = %#v, want object", filters["default"])
	}
	filterRef, ok := filterDefault["ref"].(map[string]any)
	if !ok {
		t.Fatalf("filters.default.ref = %#v, want object", filterDefault["ref"])
	}
	for field, want := range map[string]float64{
		"startRow": 0, "startColumn": 0, "endRow": 20, "endColumn": 1,
	} {
		if got := filterRef[field]; got != want {
			t.Errorf("filters.default.ref.%s = %#v, want %v", field, got, want)
		}
	}
	filterColumns, ok := filterDefault["filterColumns"].([]any)
	if !ok || len(filterColumns) != 1 {
		t.Fatalf("filters.default.filterColumns = %#v, want one-element array", filterDefault["filterColumns"])
	}
	filterColumn, ok := filterColumns[0].(map[string]any)
	if !ok {
		t.Fatalf("filters.default.filterColumns[0] = %#v, want object", filterColumns[0])
	}
	if got := filterColumn["colId"]; got != float64(1) {
		t.Errorf("filters.default.filterColumns[0].colId = %#v, want 1", got)
	}
	filterCriteria, ok := filterColumn["filters"].(map[string]any)
	if !ok {
		t.Fatalf("filters.default.filterColumns[0].filters = %#v, want object", filterColumn["filters"])
	}
	if got, want := filterCriteria["filters"], []any{"待处理"}; !reflect.DeepEqual(got, want) {
		t.Errorf("filters.default.filterColumns[0].filters.filters = %#v, want %#v", got, want)
	}
	validations, ok := gotBody["dataValidations"].(map[string]any)
	if !ok {
		t.Fatalf("dataValidations = %#v, want object", gotBody["dataValidations"])
	}
	rules, ok := validations["default"].([]any)
	if !ok || len(rules) != 2 {
		t.Fatalf("dataValidations.default = %#v, want two rules", validations["default"])
	}
	wantRules := []struct {
		uid         string
		typeName    string
		startColumn float64
	}{
		{uid: "single", typeName: "list", startColumn: 0},
		{uid: "multiple", typeName: "listMultiple", startColumn: 1},
	}
	for i, wantRule := range wantRules {
		rule, ok := rules[i].(map[string]any)
		if !ok {
			t.Errorf("dataValidations.default[%d] = %#v, want object", i, rules[i])
			continue
		}
		for field, want := range map[string]any{
			"uid":          wantRule.uid,
			"type":         wantRule.typeName,
			"formula1":     `["待处理","已完成"]`,
			"formula2":     "#E4F4FE,#EFFBD0",
			"showDropDown": true,
			"renderMode":   float64(2),
		} {
			if got := rule[field]; !reflect.DeepEqual(got, want) {
				t.Errorf("dataValidations.default[%d].%s = %#v, want %#v", i, field, got, want)
			}
		}
		ranges, ok := rule["ranges"].([]any)
		if !ok || len(ranges) != 1 {
			t.Errorf("dataValidations.default[%d].ranges = %#v, want one-element array", i, rule["ranges"])
			continue
		}
		gotRange, ok := ranges[0].(map[string]any)
		if !ok {
			t.Errorf("dataValidations.default[%d].ranges[0] = %#v, want object", i, ranges[0])
			continue
		}
		wantRange := map[string]any{
			"startRow": float64(1), "startColumn": wantRule.startColumn,
			"endRow": float64(20), "endColumn": wantRule.startColumn,
		}
		if !reflect.DeepEqual(gotRange, wantRange) {
			t.Errorf("dataValidations.default[%d].ranges[0] = %#v, want %#v", i, gotRange, wantRange)
		}
	}
	if _, present := gotBody["baseVersion"]; present {
		t.Errorf("baseVersion must stay in If-Match, got body value %#v", gotBody["baseVersion"])
	}
}

// TestDocsSheetEdit_RequiresBaseVersion confirms --base-version is required:
// the command fails before any request when the base-version token is missing,
// mirroring the backend's mandatory optimistic-concurrency guard.
func TestDocsSheetEdit_RequiresBaseVersion(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	root.SetArgs([]string{"docs", "sheet", "edit", "d1", "--data", `{"cells":{}}`})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --base-version is omitted")
	}
	if called {
		t.Error("server must not be called when required --base-version is missing")
	}
}

// TestDocsSceneGet_ReadsSceneAndBaseVersion checks docs.scene.get hits
// GET /v1/bot/docs/{docId}/scene (reader), sends no body, and surfaces the
// backend's {elements, files, baseVersion, schemaVersion} response through the
// success envelope so the caller can capture the base-version token for a
// follow-up edit.
func TestDocsSceneGet_ReadsSceneAndBaseVersion(t *testing.T) {
	var gotMethod, gotPath string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"docId":"d1","elements":[{"id":"e1","type":"rectangle","version":3}],"files":{},"schemaVersion":1,"baseVersion":"BV_ABC=="}`))
	})
	root.SetArgs([]string{"docs", "scene", "get", "d1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "GET" || gotPath != "/v1/bot/docs/d1/scene" {
		t.Errorf("got %s %s, want GET /v1/bot/docs/d1/scene", gotMethod, gotPath)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			BaseVersion string `json:"baseVersion"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if !env.OK || env.Data.BaseVersion != "BV_ABC==" {
		t.Errorf("expected baseVersion BV_ABC== in envelope, got %+v", env)
	}
}

// TestDocsSceneEdit_SendsBatchAndIfMatch checks docs.scene.edit hits
// PATCH /v1/bot/docs/{docId}/scene, carries the element upsert/delete batch
// (passed via the generic --data escape hatch) in the body, and sends the
// base-version token as the If-Match header — the spec-declared header
// capability wiring --base-version to If-Match, not a body/query field.
func TestDocsSceneEdit_SendsBatchAndIfMatch(t *testing.T) {
	var gotMethod, gotPath, gotIfMatch string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		gotIfMatch = r.Header.Get("If-Match")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","bytes":256,"baseVersion":"BV_NEXT==","newDocVersionSeq":7}`))
	})
	batch := `{"elements":[{"id":"e1","type":"rectangle","version":4,"index":"a0"}],"deletedElementIds":["e2"],"files":{}}`
	root.SetArgs([]string{"docs", "scene", "edit", "d1", "--base-version", "BV_ABC==", "--data", batch})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != "PATCH" || gotPath != "/v1/bot/docs/d1/scene" {
		t.Errorf("got %s %s, want PATCH /v1/bot/docs/d1/scene", gotMethod, gotPath)
	}
	if gotIfMatch != "BV_ABC==" {
		t.Errorf("If-Match = %q, want BV_ABC== (base version must reach the wire as the If-Match header)", gotIfMatch)
	}
	elems, ok := gotBody["elements"].([]any)
	if !ok || len(elems) != 1 {
		t.Fatalf("elements = %v, want a 1-element array", gotBody["elements"])
	}
	e0, ok := elems[0].(map[string]any)
	if !ok || e0["id"] != "e1" {
		t.Errorf("elements[0] = %v, want the e1 upsert", elems[0])
	}
	del, ok := gotBody["deletedElementIds"].([]any)
	if !ok || len(del) != 1 || del[0] != "e2" {
		t.Errorf("deletedElementIds = %v, want [e2]", gotBody["deletedElementIds"])
	}
	// The base version travels in the header, not the JSON body.
	if _, present := gotBody["baseVersion"]; present {
		t.Errorf("baseVersion must not be duplicated into the JSON body; got %v", gotBody["baseVersion"])
	}
}

// TestDocsSceneEdit_RequiresBaseVersion confirms --base-version is required:
// the command fails before any request when the base-version token is missing,
// mirroring the backend's mandatory optimistic-concurrency guard.
func TestDocsSceneEdit_RequiresBaseVersion(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	root.SetArgs([]string{"docs", "scene", "edit", "d1", "--data", `{"elements":[]}`})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error when --base-version is omitted")
	}
	if called {
		t.Error("server must not be called when required --base-version is missing")
	}
}

// TestDocsSceneEdit_RejectsIndexlessElement confirms an upsert element with no
// `index` is rejected locally (non-zero exit) before any request is sent, so
// index-less whiteboard elements can no longer reach the backend (XIN-792).
func TestDocsSceneEdit_RejectsIndexlessElement(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	batch := `{"elements":[{"id":"e1","type":"rectangle","version":4}]}`
	root.SetArgs([]string{"docs", "scene", "edit", "d1", "--base-version", "BV_ABC==", "--data", batch})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for an element missing `index`")
	}
	if called {
		t.Error("server must not be called when an element is missing `index`")
	}
}

// TestDocsSceneEdit_RejectsInvalidIndex confirms the exact repair artifact from
// XIN-792 (`r00000003`) — a malformed fractional-index — is rejected locally.
func TestDocsSceneEdit_RejectsInvalidIndex(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	batch := `{"elements":[{"id":"e1","type":"rectangle","version":4,"index":"r00000003"}]}`
	root.SetArgs([]string{"docs", "scene", "edit", "d1", "--base-version", "BV_ABC==", "--data", batch})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for an element with an invalid `index`")
	}
	if called {
		t.Error("server must not be called when an element carries an invalid `index`")
	}
}

// TestDocsSceneEdit_DeleteOnlyBatchAllowed confirms a batch with no `elements`
// to upsert (only soft-deletes) still passes — index validation applies only to
// upserted elements.
func TestDocsSceneEdit_DeleteOnlyBatchAllowed(t *testing.T) {
	called := false
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"docId":"d1","bytes":8,"baseVersion":"BV_NEXT==","newDocVersionSeq":8}`))
	})
	root.SetArgs([]string{"docs", "scene", "edit", "d1", "--base-version", "BV_ABC==", "--data", `{"deletedElementIds":["e2"]}`})
	if err := root.Execute(); err != nil {
		t.Fatalf("delete-only batch should pass validation: %v", err)
	}
	if !called {
		t.Error("server should be called for a valid delete-only batch")
	}
}
