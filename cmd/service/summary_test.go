package service

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestSummaryRegistryShape(t *testing.T) {
	reg := registry.MustNew()
	wants := map[string]string{
		"summary.create": "/summary/api/v1/bot/summaries",
		"summary.list":   "/summary/api/v1/bot/summaries",
		"summary.get":    "/summary/api/v1/bot/summaries/{id}",
		"summary.result": "/summary/api/v1/bot/summaries/{id}/result",
	}
	for id, path := range wants {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		wantMethod, wantRisk := http.MethodGet, "read"
		if id == "summary.create" {
			wantMethod, wantRisk = http.MethodPost, "write"
		}
		if op.Method != wantMethod || op.Path != path || op.SpaceHeader || op.Risk != wantRisk || op.BaseURLEnv != "OCTO_API_BASE_URL" {
			t.Errorf("%s: got method=%s path=%s space=%v risk=%s base=%s", id, op.Method, op.Path, op.SpaceHeader, op.Risk, op.BaseURLEnv)
		}
	}
	if op, _ := reg.GetOperation("summary.list"); op.Pagination != nil {
		t.Error("summary.list uses a backend {total,items} page, so generic --page-all must stay disabled")
	}
	create, _ := reg.GetOperation("summary.create")
	if !create.RequestBodyRequired || create.RequestBody == nil {
		t.Fatal("summary.create must preserve the required request-body contract")
	}
	if sources := create.RequestBody.Properties["sources"]; sources.MinItems != 1 || sources.MaxItems != 30 {
		t.Errorf("sources item bounds = %d..%d, want 1..30", sources.MinItems, sources.MaxItems)
	}
	key := create.Parameters[0]
	if key.MinLength != 1 || key.MaxLength != 128 || key.Pattern == "" {
		t.Errorf("idempotency key constraints not preserved: %+v", key)
	}
}

func TestSummaryTreeShape(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {})
	summary := findCmd(root, "summary")
	if summary == nil {
		t.Fatal("missing summary service command")
	}
	for _, leaf := range []string{"create", "list", "get", "result"} {
		if !contains(childNames(summary), leaf) {
			t.Errorf("summary: missing %q; got %v", leaf, childNames(summary))
		}
	}
	list := findCmd(summary, "list")
	if list == nil {
		t.Fatal("summary list command must exist")
	} else if list.Flags().Lookup("page-all") != nil {
		t.Error("summary list must not expose --page-all (backend returns a {total, items} page, no cursor)")
	}
}

func TestSummaryCreateSendsIdempotencyHeaderAndBody(t *testing.T) {
	var gotPath, gotKey, gotSpace, gotBody string
	root, _, _ := rootWithServiceSpaced(t, "spoofed-space", func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey, gotSpace = r.URL.Path, r.Header.Get("Idempotency-Key"), r.Header.Get("X-Space-Id")
		bodyBytes, _ := io.ReadAll(r.Body)
		gotBody = string(bodyBytes)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"task_id":42,"status":0,"trigger_type":4}}`))
	})
	body := `{"title":"weekly","time_range":{"start":"2026-07-21T00:00:00Z","end":"2026-07-28T00:00:00Z"},"sources":[{"source_type":1,"source_id":"group-a"}]}`
	root.SetArgs([]string{"summary", "create", "--idempotency-key", "weekly-2026w31", "--data", body})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/summary/api/v1/bot/summaries" || gotKey != "weekly-2026w31" || gotSpace != "" || !strings.Contains(gotBody, `"group-a"`) {
		t.Fatalf("path=%q key=%q space=%q body=%s", gotPath, gotKey, gotSpace, gotBody)
	}
}

func TestSummaryCreateRejectsMissingTimeRangeAndSources(t *testing.T) {
	// Jerry-Xin's P1 on octo-cli PR #113: `summary create` must fail locally
	// when --data omits the required `time_range`/`sources` object/array
	// properties. registerBodyFlags only marks promoted required primitives in
	// help text, so resolveBody is the enforcement layer for all body fields.
	cases := []struct {
		name string
		data string
	}{
		{"empty body", ""},
		{"only title", `{"title":"weekly"}`},
		{"time_range without sources", `{"time_range":{"start":"2026-07-21T00:00:00Z","end":"2026-07-28T00:00:00Z"}}`},
		{"sources without time_range", `{"sources":[{"source_type":1,"source_id":"g1"}]}`},
		{"null time_range", `{"time_range":null,"sources":[{"source_type":1,"source_id":"g1"}]}`},
		{"empty time_range", `{"time_range":{},"sources":[{"source_type":1,"source_id":"g1"}]}`},
		{"empty sources", `{"time_range":{"start":"2026-07-21T00:00:00Z","end":"2026-07-28T00:00:00Z"},"sources":[]}`},
		{"source missing id", `{"time_range":{"start":"2026-07-21T00:00:00Z","end":"2026-07-28T00:00:00Z"},"sources":[{"source_type":1}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var backendHits atomic.Int32
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				backendHits.Add(1)
			})
			args := []string{"summary", "create", "--idempotency-key", "k"}
			if tc.data != "" {
				args = append(args, "--data", tc.data)
			}
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("summary create with data=%q must fail validation locally", tc.data)
			}
			if hits := backendHits.Load(); hits != 0 {
				t.Fatalf("request reached backend %d time(s) (data=%q)", hits, tc.data)
			}
			// Sanity check the error text mentions at least one required field,
			// so an agent gets an actionable message instead of a bare 400.
			msg := err.Error()
			if !strings.Contains(msg, "time_range") && !strings.Contains(msg, "sources") {
				t.Errorf("expected error to name the missing required field(s), got %q", msg)
			}
		})
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
