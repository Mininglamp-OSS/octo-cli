package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const loopQueryTestWorkspaceID = "99999999-9999-4999-8999-999999999999"

func newLoopQueryContractFactory(t *testing.T, handler http.HandlerFunc) *cmdutil.TestFactory {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	factory := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: server.URL, BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	factory.SetConfig(cfg)
	factory.SetCredential(cred)
	factory.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	return factory
}

func writeLoopQueryTestCollection(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
}

func TestLoopTaskSearchRejectsMissingQueryBeforeRequest(t *testing.T) {
	var requests int
	factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeLoopQueryTestCollection(w)
	})

	_, _, err := execRoot(t, factory, "loop", "task", "search", "--workspace-id", loopQueryTestWorkspaceID)
	if err == nil {
		t.Fatal("task search without <query> must fail")
	}
	if requests != 0 {
		t.Fatalf("missing query sent %d requests, want 0", requests)
	}
}

func TestLoopTaskSearchPositionalQuery(t *testing.T) {
	for _, query := range []string{"oauth timeout & status:open + 中文/?", ".", "-timeout"} {
		t.Run(query, func(t *testing.T) {
			var gotPath, gotQuery, gotWorkspace, gotPage, gotPageSize string
			factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotQuery = r.URL.Path, r.URL.Query().Get("q")
				gotWorkspace = r.Header.Get("X-Workspace-ID")
				gotPage, gotPageSize = r.URL.Query().Get("page"), r.URL.Query().Get("page_size")
				writeLoopQueryTestCollection(w)
			})
			args := []string{"loop", "task", "search", "--workspace-id", loopQueryTestWorkspaceID,
				"--page", "2", "--page-size", "5", "--", query}
			if _, _, err := execRoot(t, factory, args...); err != nil {
				t.Fatalf("task search positional query failed: %v", err)
			}
			if gotPath != "/fleet/api/v1/tasks/_search" || gotQuery != query {
				t.Fatalf("path=%q q=%q, want search endpoint and q=%q", gotPath, gotQuery, query)
			}
			if gotWorkspace != loopQueryTestWorkspaceID || gotPage != "2" || gotPageSize != "5" {
				t.Fatalf("workspace=%q page=%q page_size=%q", gotWorkspace, gotPage, gotPageSize)
			}
		})
	}
}

func TestLoopTaskSearchRejectsEmptyQueryBeforeRequest(t *testing.T) {
	requests := 0
	factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeLoopQueryTestCollection(w)
	})
	_, _, err := execRoot(t, factory, "loop", "task", "search", "", "--workspace-id", loopQueryTestWorkspaceID)
	var exitErr *output.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("empty query error = %v, want VALIDATION_ERROR", err)
	}
	if requests != 0 {
		t.Fatalf("empty query sent %d requests, want 0", requests)
	}
}

func TestLoopTaskSearchRejectsQueryFlagBeforeRequest(t *testing.T) {
	requests := 0
	factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeLoopQueryTestCollection(w)
	})
	_, _, err := execRoot(t, factory, "loop", "task", "search", "--q", "oauth", "--workspace-id", loopQueryTestWorkspaceID)
	if err == nil {
		t.Fatal("task search --q must fail")
	}
	if requests != 0 {
		t.Fatalf("query flag sent %d requests, want 0", requests)
	}
}
