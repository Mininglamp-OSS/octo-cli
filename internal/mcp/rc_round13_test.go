package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

func TestRound13UnknownWriteKeepsClassificationWithLargeError(t *testing.T) {
	env, err := json.Marshal(map[string]any{"ok": false, "error": map[string]any{"type": "remote", "code": "RESULT_UNKNOWN", "detail": strings.Repeat("failure", 180000)}})
	if err != nil {
		t.Fatal(err)
	}
	result := classifyExecuteResult(env, false, "message.send", "sha256:example", false, true, nil)
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "result_unknown" || payload["code"] != "RESULT_UNKNOWN" || payload["retryable"] != false || payload["note"] == nil {
		t.Fatalf("large backend error lost do-not-retry classification: %v", payload)
	}
	if len(result.Content[0].Text) > 1<<20 {
		t.Fatalf("failure result was not bounded: %d", len(result.Content[0].Text))
	}
}

type round13BlockingBody struct {
	entered chan<- struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *round13BlockingBody) Read(p []byte) (int, error) {
	b.once.Do(func() { b.entered <- struct{}{} })
	<-b.release
	return 0, io.EOF
}

func (*round13BlockingBody) Close() error { return nil }

func TestRound13UnauthenticatedReadsHaveInflightLimit(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeTwo)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 7)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 7 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			r.ContentLength = maxHTTPBodyBytes
			r.Body = &round13BlockingBody{entered: entered, release: release}
			h.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}
	defer func() { close(release); wg.Wait() }()
	for range 7 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("body read did not start")
		}
	}
	// Large readers exhaust their own byte budget, but a small ping still
	// receives its reserved capacity.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	r.ContentLength = maxHTTPBodyBytes
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("eighth large body read returned HTTP %d, want 503", w.Code)
	}
	if w.Header().Get("Retry-After") == "" || !strings.Contains(w.Header().Get("Content-Type"), "application/json") || !strings.Contains(w.Body.String(), `"jsonrpc":"2.0"`) {
		t.Fatalf("read-budget refusal is not JSON-RPC backpressure: headers=%v body=%s", w.Header(), w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("large body reads denied a small ping: HTTP %d", w.Code)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"ping"}`))
	r.ContentLength = -1 // a small request sent with chunked framing
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("large body reads denied a small chunked ping: HTTP %d", w.Code)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", smallReadThreshold+1)))
	r.ContentLength = -1
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatalf("large chunked body bypassed the exhausted large budget: HTTP %d", w.Code)
	}
}

func TestRound13MCPExecutionRequiresPinnedMultipartDescriptor(t *testing.T) {
	root, _ := uploadFixture(t)
	be := &backend{}
	backend := be.server(t)
	s := newTestServer(t).WithFacade(facadeBoth)
	s.WithUploadPolicy(root, false)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
	s.build = func(f *cmdutil.Factory) *cobra.Command {
		// Model a future composite that drops the pinned handoff. The MCP
		// execution policy must refuse a fallback os.Open of the path.
		f.MultipartFile = nil
		return testRoot(f)
	}
	for _, tool := range []string{"call_op", "execute"} {
		res := callTool(t, s, tool, map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file_path": "sub/f.txt"}})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "descriptor is missing") || be.path != "" {
			t.Fatalf("%s reopened an unpinned multipart path: %s backend=%q", tool, res.Content[0].Text, be.path)
		}
	}
}

func TestRound13LargeDryRunKeepsNoBackendMarker(t *testing.T) {
	env, err := json.Marshal(map[string]any{"ok": true, "data": strings.Repeat("planned", 180000)})
	if err != nil {
		t.Fatal(err)
	}
	result := classifyExecuteResult(env, true, "message.send", "sha256:example", true, true, nil)
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["dry_run"] != true || payload["dry_run_note"] == nil || result.IsError {
		t.Fatalf("large dry-run lost no-backend classification: %v", payload)
	}
	if len(result.Content[0].Text) > 1<<20 {
		t.Fatalf("dry-run result was not bounded: %d", len(result.Content[0].Text))
	}
}
