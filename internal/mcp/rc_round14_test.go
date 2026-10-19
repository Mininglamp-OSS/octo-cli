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
)

func TestRound14MultipartDryRunKeepsControlFieldsWhenPlanIsLarge(t *testing.T) {
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	t.Setenv("OCTO_TOKEN", "bf_fixture")
	s := facadeServer(t, facadeTwo)
	s.allowLocalUpload = true
	result := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload", "dry_run": true,
		"arguments": map[string]any{"file_path": "/missing.bin", "type": strings.Repeat("x", 1200<<10)},
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if result.IsError || payload["status"] != "ok" || payload["dry_run"] != true || payload["dry_run_note"] == nil {
		t.Fatalf("large multipart preview lost the no-send contract: %v", payload)
	}
	if len(result.Content[0].Text) > 1<<20 {
		t.Fatalf("multipart preview response is unbounded: %d", len(result.Content[0].Text))
	}
	marker, _ := payload["envelope"].(map[string]any)
	if marker["code"] != "ENVELOPE_TOO_LARGE" {
		t.Fatalf("missing envelope truncation marker: %v", marker)
	}
}

func TestRound14MultipartDryRunKeepsControlFieldsOnLargeValidationError(t *testing.T) {
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	t.Setenv("OCTO_TOKEN", "bf_fixture")
	s := facadeServer(t, facadeTwo)
	s.allowLocalUpload = true
	result := callTool(t, s, "execute", map[string]any{
		"operation_id": "file.upload", "dry_run": true,
		"arguments": map[string]any{"file_path": "/missing.bin", strings.Repeat("x", 1200<<10): "unknown"},
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatal(err)
	}
	if !result.IsError || payload["status"] != "validation_error" || payload["dry_run"] != true {
		t.Fatalf("large multipart validation lost the no-send contract: %v", payload)
	}
	if len(result.Content[0].Text) > 1<<20 {
		t.Fatalf("multipart validation response is unbounded: %d", len(result.Content[0].Text))
	}
}

func TestRound14SlowSmallBodiesDoNotDenyOtherClients(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeTwo)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
			r.ContentLength = 40
			r.Body = &round13BlockingBody{entered: entered, release: release}
			h.ServeHTTP(httptest.NewRecorder(), r)
		}()
	}
	defer func() { close(release); wg.Wait() }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("body read did not start")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("two stalled small bodies denied an unrelated request: HTTP %d", w.Code)
	}
}

func TestRound14OversizeHeaderDoesNotConsumeReadBudget(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeTwo)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", io.NopCloser(strings.NewReader("x")))
	r.ContentLength = maxHTTPBodyBytes + 1
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized Content-Length returned HTTP %d", w.Code)
	}
}

func TestRound14ChunkedBodyLimitAtEightMiB(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeTwo)
	if err != nil {
		t.Fatal(err)
	}
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	for _, size := range []int{smallReadThreshold + 1, maxHTTPBodyBytes, maxHTTPBodyBytes + 1} {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat(" ", size-len(ping))+ping))
		r.ContentLength = -1
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusOK
		if size > maxHTTPBodyBytes {
			want = http.StatusRequestEntityTooLarge
		}
		if w.Code != want {
			t.Errorf("chunked body of %d bytes returned HTTP %d, want %d", size, w.Code, want)
		}
	}
}

func TestRound14PinnedHTMLMountRejectsUnknownSelector(t *testing.T) {
	tc := TrustedContext{ChannelID: "G1", ChannelType: "2"}
	for _, op := range []string{"html.publish", "html.draft.create"} {
		if err := tc.validateOperationBinding(op, map[string]any{"mount_type": "unknown"}); err == nil {
			t.Errorf("%s accepted an unknown mount selector under a trusted channel", op)
		}
	}
}
