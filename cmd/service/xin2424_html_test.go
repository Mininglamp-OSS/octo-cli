package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// xin2424HTMLRow pins one octo-docs-html route (verified against
// octo-docs-html@4bc19a2 internal/transport/httpx/server.go) to the operation
// that exposes it through the /docs-html gateway prefix.
type xin2424HTMLRow struct {
	row          string
	opID         string
	method       string
	path         string
	risk         string
	binary       bool
	binaryBody   bool
	requiredPath []string
	requiredQry  []string
	optionalQry  []string
	requiredBody []string
	optionalBody []string
}

var xin2424HTMLRows = []xin2424HTMLRow{
	{row: "html#85", opID: "html.version.source", method: "GET", path: "/docs-html/v1/docs/{doc_id}/versions/{version}/source", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id", "version"}},
	{row: "html#86", opID: "html.version.revert", method: "POST", path: "/docs-html/v1/docs/{doc_id}/versions/{version}/revert", risk: "write", requiredPath: []string{"doc_id", "version"}},
	{row: "html#87", opID: "html.diff", method: "GET", path: "/docs-html/v1/docs/{doc_id}/diff", risk: "read", requiredPath: []string{"doc_id"}, requiredQry: []string{"from", "to"}},
	{row: "html#101", opID: "html.comment.reanchor", method: "PATCH", path: "/docs-html/v1/comments", risk: "write", requiredBody: []string{"slug", "id", "anchor"}, optionalBody: []string{"version"}},
	{row: "html#102", opID: "html.comment.rm", method: "DELETE", path: "/docs-html/v1/comments", risk: "high-risk-write", requiredQry: []string{"slug"}, optionalQry: []string{"id", "all", "version"}},
	{row: "html#103", opID: "html.comment.react", method: "POST", path: "/docs-html/v1/reactions", risk: "write", requiredBody: []string{"slug", "comment_id", "emoji"}, optionalBody: []string{"version"}},
	{row: "html#107", opID: "html.page.draft", method: "GET", path: "/docs-html/d/{doc_id}/draft", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id"}},
	{row: "html#108", opID: "html.page.draft_head", method: "HEAD", path: "/docs-html/d/{doc_id}/draft", risk: "read", binary: true, requiredPath: []string{"doc_id"}},
	{row: "html#109", opID: "html.page.version", method: "GET", path: "/docs-html/d/{doc_id}/v/{version}", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id", "version"}},
	{row: "html#110", opID: "html.page.version_head", method: "HEAD", path: "/docs-html/d/{doc_id}/v/{version}", risk: "read", binary: true, requiredPath: []string{"doc_id", "version"}},
	{row: "html#111", opID: "html.page.export", method: "GET", path: "/docs-html/d/{doc_id}/v/{version}/export", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id", "version"}, optionalQry: []string{"download"}},
	{row: "html#111", opID: "html.page.fork", method: "GET", path: "/docs-html/d/{doc_id}/v/{version}/fork", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id", "version"}, optionalQry: []string{"download"}},
	{row: "html#112", opID: "html.asset.get", method: "GET", path: "/docs-html/d/{doc_id}/assets/{sha256}", risk: "read", binary: true, binaryBody: true, requiredPath: []string{"doc_id", "sha256"}},
	{row: "html#113", opID: "html.asset.head", method: "HEAD", path: "/docs-html/d/{doc_id}/assets/{sha256}", risk: "read", binary: true, requiredPath: []string{"doc_id", "sha256"}},
}

func TestXIN2424HTMLOperationContracts(t *testing.T) {
	r := registry.MustNew()
	for _, row := range xin2424HTMLRows {
		t.Run(row.row+"/"+row.opID, func(t *testing.T) {
			op, ok := r.GetOperation(row.opID)
			if !ok {
				t.Fatalf("operation %s missing", row.opID)
			}
			if op.Service != "html" || op.Method != row.method || op.Path != row.path {
				t.Fatalf("route = %s %s %s, want html %s %s", op.Service, op.Method, op.Path, row.method, row.path)
			}
			if op.Risk != row.risk {
				t.Errorf("risk = %q, want %q", op.Risk, row.risk)
			}
			if op.BinaryResponse != row.binary || op.BinaryBody != row.binaryBody {
				t.Errorf("binary = %v/%v, want %v/%v", op.BinaryResponse, op.BinaryBody, row.binary, row.binaryBody)
			}
			if op.Multipart {
				t.Error("operation must not be multipart")
			}
			if op.Summary == "" {
				t.Error("summary missing")
			}
			params := map[string]registry.ParamInfo{}
			for _, p := range op.Parameters {
				params[p.In+":"+p.Name] = p
				if p.Secret {
					t.Errorf("unexpected secret parameter %s", p.Name)
				}
			}
			wantParams := len(row.requiredPath) + len(row.requiredQry) + len(row.optionalQry)
			if len(op.Parameters) != wantParams {
				t.Errorf("parameters = %v, want %d", op.Parameters, wantParams)
			}
			for _, name := range row.requiredPath {
				if p, ok := params["path:"+name]; !ok || !p.Required {
					t.Errorf("path param %s missing or optional", name)
				}
			}
			for _, name := range row.requiredQry {
				if p, ok := params["query:"+name]; !ok || !p.Required {
					t.Errorf("query param %s missing or optional", name)
				}
			}
			for _, name := range row.optionalQry {
				if p, ok := params["query:"+name]; !ok || p.Required {
					t.Errorf("query param %s missing or required", name)
				}
			}
			if len(row.requiredBody) == 0 {
				if op.RequestBody != nil {
					t.Errorf("unexpected request body %v", op.RequestBody)
				}
				return
			}
			if op.RequestBody == nil || !op.RequestBodyRequired {
				t.Fatal("request body missing or optional")
			}
			if strings.Join(op.RequestBody.Required, ",") != strings.Join(row.requiredBody, ",") {
				t.Errorf("body required = %v, want %v", op.RequestBody.Required, row.requiredBody)
			}
			for _, name := range append(append([]string{}, row.requiredBody...), row.optionalBody...) {
				if _, ok := op.RequestBody.Properties[name]; !ok {
					t.Errorf("body property %s missing", name)
				}
			}
			if len(op.RequestBody.Properties) != len(row.requiredBody)+len(row.optionalBody) {
				t.Errorf("body properties = %v", op.RequestBody.Properties)
			}
		})
	}
}

func TestXIN2424HTMLPathConstraints(t *testing.T) {
	r := registry.MustNew()
	revert, _ := r.GetOperation("html.version.revert")
	for _, p := range revert.Parameters {
		if p.Name == "version" && p.Type != "integer" {
			t.Errorf("revert version type = %q, want integer (latest is refused)", p.Type)
		}
	}
	source, _ := r.GetOperation("html.version.source")
	for _, p := range source.Parameters {
		if p.Name == "version" && p.Type != "string" {
			t.Errorf("source version type = %q, want string (accepts latest)", p.Type)
		}
	}
	asset, _ := r.GetOperation("html.asset.get")
	for _, p := range asset.Parameters {
		if p.Name == "sha256" && p.Pattern != "^[0-9a-f]{64}$" {
			t.Errorf("sha256 pattern = %q", p.Pattern)
		}
	}
	react, _ := r.GetOperation("html.comment.react")
	if emoji := react.RequestBody.Properties["emoji"]; emoji.MaxLength != 32 {
		t.Errorf("emoji maxLength = %v, want 32", emoji.MaxLength)
	}
}

func TestXIN2424HTMLRequestAssembly(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	cases := []struct {
		name       string
		args       []string
		method     string
		path       string
		query      map[string]string
		body       map[string]any
		respType   string
		respBody   string
		expectBody bool
	}{
		{name: "revert", args: []string{"html", "version", "revert", "doc1", "3"}, method: "POST", path: "/docs-html/v1/docs/doc1/versions/3/revert", respType: "application/json", respBody: `{"data":{"slug":"doc1","version":5,"new_version":5,"base_version":3,"change_source":"revert"}}`},
		{name: "diff", args: []string{"html", "diff", "doc1", "--from", "1", "--to", "2"}, method: "GET", path: "/docs-html/v1/docs/doc1/diff", query: map[string]string{"from": "1", "to": "2"}, respType: "application/json", respBody: `{"data":{"from":1,"to":2,"summary":{"added":0,"removed":0,"modified":1},"changes":[],"code_hunks":[]}}`},
		{name: "reanchor", args: []string{"html", "comment", "reanchor", "--slug", "doc1", "--id", "c1", "--data", `{"anchor":{"kind":"element","aid":"a1"},"version":"latest"}`}, method: "PATCH", path: "/docs-html/v1/comments", body: map[string]any{"slug": "doc1", "id": "c1", "anchor": map[string]any{"kind": "element", "aid": "a1"}, "version": "latest"}, expectBody: true, respType: "application/json", respBody: `{"data":{"id":"c1"}}`},
		{name: "react", args: []string{"html", "comment", "react", "--slug", "doc1", "--comment-id", "c1", "--emoji", "ok"}, method: "POST", path: "/docs-html/v1/reactions", body: map[string]any{"slug": "doc1", "comment_id": "c1", "emoji": "ok"}, expectBody: true, respType: "application/json", respBody: `{"data":{"ok":true,"reactions":{"ok":["bot"]}}}`},
		{name: "comment rm", args: []string{"html", "comment", "rm", "--slug", "doc1", "--id", "c1"}, method: "DELETE", path: "/docs-html/v1/comments", query: map[string]string{"slug": "doc1", "id": "c1"}, respType: "application/json", respBody: `{"data":{"ok":true}}`},
		{name: "version source", args: []string{"html", "version", "source", "doc1", "latest"}, method: "GET", path: "/docs-html/v1/docs/doc1/versions/latest/source", respType: "text/plain; charset=utf-8", respBody: "<p>x</p>"},
		{name: "page draft", args: []string{"html", "page", "draft", "doc1"}, method: "GET", path: "/docs-html/d/doc1/draft", respType: "text/html; charset=utf-8", respBody: "<html></html>"},
		{name: "page draft head", args: []string{"html", "page", "draft-head", "doc1"}, method: "HEAD", path: "/docs-html/d/doc1/draft", respType: "text/html; charset=utf-8"},
		{name: "page version", args: []string{"html", "page", "version", "doc1", "latest"}, method: "GET", path: "/docs-html/d/doc1/v/latest", respType: "text/html; charset=utf-8", respBody: "<html></html>"},
		{name: "page version head", args: []string{"html", "page", "version-head", "doc1", "2"}, method: "HEAD", path: "/docs-html/d/doc1/v/2", respType: "text/html; charset=utf-8"},
		{name: "page export", args: []string{"html", "page", "export", "doc1", "2", "--download", "0"}, method: "GET", path: "/docs-html/d/doc1/v/2/export", query: map[string]string{"download": "0"}, respType: "text/html; charset=utf-8", respBody: "<html></html>"},
		{name: "page fork", args: []string{"html", "page", "fork", "doc1", "2"}, method: "GET", path: "/docs-html/d/doc1/v/2/fork", respType: "text/html; charset=utf-8", respBody: "<html></html>"},
		{name: "asset get", args: []string{"html", "asset", "get", "doc1", sha}, method: "GET", path: "/docs-html/d/doc1/assets/" + sha, respType: "image/png", respBody: "\x89PNG"},
		{name: "asset head", args: []string{"html", "asset", "head", "doc1", sha}, method: "HEAD", path: "/docs-html/d/doc1/assets/" + sha, respType: "image/png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				q := r.URL.Query()
				if len(q) != len(tc.query) {
					t.Errorf("query = %v, want %v", q, tc.query)
				}
				for k, v := range tc.query {
					if q.Get(k) != v {
						t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
					}
				}
				raw, _ := io.ReadAll(r.Body)
				if tc.expectBody {
					var got map[string]any
					if err := json.Unmarshal(raw, &got); err != nil {
						t.Fatalf("body %q: %v", raw, err)
					}
					want, _ := json.Marshal(tc.body)
					gotJSON, _ := json.Marshal(got)
					if !bytes.Equal(want, gotJSON) {
						t.Errorf("body = %s, want %s", gotJSON, want)
					}
				} else if len(raw) != 0 {
					t.Errorf("unexpected body %q", raw)
				}
				w.Header().Set("Content-Type", tc.respType)
				w.WriteHeader(http.StatusOK)
				if r.Method != http.MethodHead {
					_, _ = io.WriteString(w, tc.respBody)
				}
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v (out=%s)", err, tf.Out.String())
			}
			if hits != 1 {
				t.Fatalf("server hits = %d, want 1", hits)
			}
		})
	}
}
