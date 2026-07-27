package service

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestSummaryRegistryShape(t *testing.T) {
	reg := registry.MustNew()
	wants := map[string]string{
		"summary.list":   "/summary/api/v1/bot/summaries",
		"summary.get":    "/summary/api/v1/bot/summaries/{id}",
		"summary.result": "/summary/api/v1/bot/summaries/{id}/result",
	}
	for id, path := range wants {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if op.Method != http.MethodGet || op.Path != path || op.SpaceHeader || op.Risk != "read" || op.BaseURLEnv != "OCTO_API_BASE_URL" {
			t.Errorf("%s: got method=%s path=%s space=%v risk=%s base=%s", id, op.Method, op.Path, op.SpaceHeader, op.Risk, op.BaseURLEnv)
		}
	}
	if op, _ := reg.GetOperation("summary.list"); op.Pagination != nil {
		t.Error("summary.list uses a backend {total,items} page, so generic --page-all must stay disabled")
	}
}

func TestSummaryTreeShape(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {})
	summary := findCmd(root, "summary")
	if summary == nil {
		t.Fatal("missing summary service command")
	}
	for _, leaf := range []string{"list", "get", "result"} {
		if !contains(childNames(summary), leaf) {
			t.Errorf("summary: missing %q; got %v", leaf, childNames(summary))
		}
	}
	if list := findCmd(summary, "list"); list == nil || list.Flags().Lookup("page-all") != nil {
		t.Error("summary list must exist without --page-all")
	}
}

func TestSummaryListSendsBearerWithoutSpaceHeader(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotSpace string
	root, _, _ := rootWithServiceSpaced(t, "spoofed-space", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotAuth, gotSpace = r.Header.Get("Authorization"), r.Header.Get("X-Space-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"total":0,"items":[]}}`))
	})
	root.SetArgs([]string{"summary", "list", "--keyword", "review", "--page-size", "10"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/summary/api/v1/bot/summaries" || !strings.Contains(gotQuery, "keyword=review") || !strings.Contains(gotQuery, "page_size=10") {
		t.Errorf("request = %s?%s", gotPath, gotQuery)
	}
	if gotAuth == "" || gotSpace != "" {
		t.Errorf("auth=%q space=%q", gotAuth, gotSpace)
	}
}

func TestSummaryResultPath(t *testing.T) {
	var gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"content":"done"}}`))
	})
	root.SetArgs([]string{"summary", "result", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/summary/api/v1/bot/summaries/42/result" {
		t.Fatalf("path=%s", gotPath)
	}
}

func TestSummaryGetPath(t *testing.T) {
	var gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"task_id":42}}`))
	})
	root.SetArgs([]string{"summary", "get", "42"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/summary/api/v1/bot/summaries/42" {
		t.Fatalf("path=%s", gotPath)
	}
}
