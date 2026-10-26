package service

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

type xin2424DocsFixture struct {
	row          string
	op           string
	method       string
	path         string
	risk         string
	pathParams   []string
	reqQuery     []string
	reqBody      []string
	binary       bool
	secretParams []string
}

var xin2424DocsFixtures = []xin2424DocsFixture{
	{row: "docs#2", op: "docs.ppt.media.get", method: "GET", path: "/v1/bot/docs/{docId}/ppt/media/{attachId}", risk: "read", pathParams: []string{"docId", "attachId"}, binary: true},
	{row: "docs#6", op: "docs.invites.accept", method: "POST", path: "/v1/bot/docs/invites/{inviteToken}/accept", risk: "high-risk-write", pathParams: []string{"inviteToken"}, secretParams: []string{"inviteToken"}},
	{row: "docs#8", op: "docs.published", method: "POST", path: "/v1/bot/docs/{docId}/published", risk: "write", pathParams: []string{"docId"}},
	{row: "docs#11", op: "docs.view", method: "POST", path: "/v1/bot/docs/{docId}/view", risk: "write", pathParams: []string{"docId"}},
	{row: "docs#12", op: "docs.recent.list", method: "GET", path: "/v1/bot/docs/recent", risk: "read"},
	{row: "docs#13", op: "docs.recent.creators", method: "GET", path: "/v1/bot/docs/recent/creators", risk: "read"},
	{row: "docs#15", op: "docs.octo-doc.rename", method: "PATCH", path: "/v1/bot/docs/octo-doc/{octoDocSlug}", risk: "write", pathParams: []string{"octoDocSlug"}, reqBody: []string{"title"}},
	{row: "docs#25", op: "docs.forward-grant.batch", method: "POST", path: "/v1/bot/docs/{docId}/forward-grant/batch", risk: "high-risk-write", pathParams: []string{"docId"}, reqBody: []string{"grants"}},
	{row: "docs#26", op: "docs.access-requests.create", method: "POST", path: "/v1/bot/docs/{docId}/access-requests", risk: "write", pathParams: []string{"docId"}},
	{row: "docs#27", op: "docs.access-requests.list", method: "GET", path: "/v1/bot/docs/{docId}/access-requests", risk: "read", pathParams: []string{"docId"}},
	{row: "docs#28", op: "docs.access-requests.approve", method: "POST", path: "/v1/bot/docs/{docId}/access-requests/{requestId}/approve", risk: "high-risk-write", pathParams: []string{"docId", "requestId"}},
	{row: "docs#29", op: "docs.access-requests.deny", method: "POST", path: "/v1/bot/docs/{docId}/access-requests/{requestId}/deny", risk: "write", pathParams: []string{"docId", "requestId"}},
	{row: "docs#30", op: "docs.invites.create", method: "POST", path: "/v1/bot/docs/{docId}/invites", risk: "high-risk-write", pathParams: []string{"docId"}},
	{row: "docs#31", op: "docs.invites.list", method: "GET", path: "/v1/bot/docs/{docId}/invites", risk: "read", pathParams: []string{"docId"}},
	{row: "docs#32", op: "docs.invites.revoke", method: "DELETE", path: "/v1/bot/docs/{docId}/invites/{inviteToken}", risk: "write", pathParams: []string{"docId", "inviteToken"}, secretParams: []string{"inviteToken"}},
	{row: "docs#37", op: "docs.attachments.copy", method: "POST", path: "/v1/bot/docs/{docId}/attachments/copy", risk: "write", pathParams: []string{"docId"}, reqBody: []string{"sources"}},
	{row: "docs#38", op: "docs.attachments.ingest", method: "POST", path: "/v1/bot/docs/{docId}/attachments/ingest", risk: "write", pathParams: []string{"docId"}, reqBody: []string{"urls"}},
	{row: "docs#39", op: "docs.link-card", method: "POST", path: "/v1/bot/docs/{docId}/link-card", risk: "read", pathParams: []string{"docId"}, reqBody: []string{"url"}},
	{row: "docs#40", op: "docs.comment-task.events", method: "GET", path: "/v1/bot/docs/{docId}/comment-task/events", risk: "read", pathParams: []string{"docId"}, reqQuery: []string{"botUid", "commentId"}},
	{row: "docs#41", op: "docs.comment-task.get", method: "GET", path: "/v1/bot/docs/{docId}/comment-task", risk: "read", pathParams: []string{"docId"}, reqQuery: []string{"botUid", "commentId"}},
	{row: "docs#42", op: "docs.comment-task.report", method: "POST", path: "/v1/bot/docs/{docId}/comment-task", risk: "write", pathParams: []string{"docId"}, reqBody: []string{"idempotencyKey", "progress"}},
	{row: "docs#43", op: "docs.comments.markers", method: "GET", path: "/v1/bot/docs/{docId}/comments/markers", risk: "read", pathParams: []string{"docId"}},
	{row: "docs#44", op: "docs.comments.thread", method: "GET", path: "/v1/bot/docs/{docId}/comments/{id}/thread", risk: "read", pathParams: []string{"docId", "id"}},
	{row: "docs#58", op: "docs.summary-source", method: "GET", path: "/v1/bot/docs/{docId}/summary-source", risk: "read", pathParams: []string{"docId"}},
	{row: "docs#63", op: "docs.sheet.list", method: "GET", path: "/v1/bot/docs/{docId}/sheets", risk: "read", pathParams: []string{"docId"}},
	{row: "docs#73", op: "docs.file.export", method: "GET", path: "/v1/bot/docs/{docId}/export/file", risk: "read", pathParams: []string{"docId"}, reqQuery: []string{"format"}, binary: true},
	{row: "docs#75", op: "docs.scene.import", method: "POST", path: "/v1/bot/docs/{docId}/import/excalidraw", risk: "write", pathParams: []string{"docId"}, reqBody: []string{"elements", "files", "type", "version"}},
}

func TestXIN2424DocsOperationsMatchBackendContract(t *testing.T) {
	reg := registry.MustNew()
	for _, fx := range xin2424DocsFixtures {
		t.Run(fx.row, func(t *testing.T) {
			d, ok := reg.GetOperation(fx.op)
			if !ok {
				t.Fatalf("%s: operation %s missing", fx.row, fx.op)
			}
			if d.Method != fx.method || d.Path != fx.path {
				t.Errorf("%s: %s %s, want %s %s", fx.op, d.Method, d.Path, fx.method, fx.path)
			}
			if d.Risk != fx.risk {
				t.Errorf("%s: risk %q, want %q", fx.op, d.Risk, fx.risk)
			}
			if d.Multipart {
				t.Errorf("%s: must not be multipart", fx.op)
			}
			if d.BinaryResponse != fx.binary {
				t.Errorf("%s: binary response %v, want %v", fx.op, d.BinaryResponse, fx.binary)
			}
			if d.SpaceHeader {
				t.Errorf("%s: docs Bot routes resolve Space server-side and must not send a Space header", fx.op)
			}
			var gotPath, gotQuery, gotSecret []string
			for _, p := range d.Parameters {
				switch {
				case p.In == "path" && p.Required:
					gotPath = append(gotPath, p.Name)
				case p.In == "query" && p.Required:
					gotQuery = append(gotQuery, p.Name)
				}
				if p.Secret {
					gotSecret = append(gotSecret, p.Name)
				}
				switch p.Name {
				case "channel_id", "channel_type", "on_behalf_of", "space_id":
					t.Errorf("%s: unexpected session-bound parameter %s", fx.op, p.Name)
				}
			}
			assertXIN2424Set(t, fx.op+" required path params", gotPath, fx.pathParams)
			assertXIN2424Set(t, fx.op+" required query params", gotQuery, fx.reqQuery)
			assertXIN2424Set(t, fx.op+" secret params", gotSecret, fx.secretParams)
			var gotBody []string
			if d.RequestBody != nil {
				gotBody = append(gotBody, d.RequestBody.Required...)
			}
			assertXIN2424Set(t, fx.op+" required body fields", gotBody, fx.reqBody)
		})
	}
}

func assertXIN2424Set(t *testing.T, label string, got, want []string) {
	t.Helper()
	g := append([]string(nil), got...)
	w := append([]string(nil), want...)
	sort.Strings(g)
	sort.Strings(w)
	if strings.Join(g, ",") != strings.Join(w, ",") {
		t.Errorf("%s = %v, want %v", label, g, w)
	}
}

func TestXIN2424DocsInviteAcceptWire(t *testing.T) {
	var method, path string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		_, _ = w.Write([]byte(`{"docId":"d_1","role":"writer","principalSpaceId":"s1"}`))
	})
	root.SetArgs([]string{"docs", "invites", "accept", "0123abcd"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if method != http.MethodPost || path != "/v1/bot/docs/invites/0123abcd/accept" {
		t.Fatalf("request = %s %s", method, path)
	}
}

func TestXIN2424DocsFileExportFlagAndQuery(t *testing.T) {
	var path, format string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		path, format = r.URL.Path, r.URL.Query().Get("format")
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte("# title\n"))
	})
	root.SetArgs([]string{"docs", "file", "export", "d_1", "--export-format", "md"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if path != "/v1/bot/docs/d_1/export/file" || format != "md" {
		t.Fatalf("request path=%s format=%s", path, format)
	}
}

func TestXIN2424DocsSceneImportSendsJSONEnvelope(t *testing.T) {
	var body map[string]any
	var contentType, mode string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		contentType, mode = r.Header.Get("Content-Type"), r.URL.Query().Get("mode")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_, _ = w.Write([]byte(`{"docId":"d_1","mode":"replace"}`))
	})
	root.SetArgs([]string{"docs", "scene", "import", "d_1", "--mode", "replace", "--data", `{"type":"excalidraw","version":2,"elements":[],"files":{}}`})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.HasPrefix(contentType, "application/json") || mode != "replace" {
		t.Fatalf("content-type=%q mode=%q", contentType, mode)
	}
	if body["type"] != "excalidraw" || body["version"] != float64(2) {
		t.Fatalf("body = %v", body)
	}
}
