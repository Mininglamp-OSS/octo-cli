package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound13PinnedThreadChannelWinsOverHeaderThread(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{ChannelID: "G1____T1", ChannelType: "5"}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.WithContextHeaders(true)
	r := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	r.Header.Set("X-Octo-Thread-Id", "T_ATTACKER")
	args := map[string]any{"group_no": "model-group", "short_id": "model-short"}
	forced := h.connectionServer(r).trusted.apply("thread.md-update", args)
	if args["group_no"] != "G1" || args["short_id"] != "T1" || !forced["short_id"] {
		t.Fatalf("pinned thread was redirected by a header: args=%v forced=%v", args, forced)
	}
}

func TestRound13ThreadIDDoesNotHalfPinThreadPath(t *testing.T) {
	tc := TrustedContext{ThreadID: "T1"}
	args := map[string]any{"group_no": "G-model", "short_id": "T-model"}
	forced := tc.apply("thread.get", args)
	if args["group_no"] != "G-model" || args["short_id"] != "T-model" || forced["short_id"] {
		t.Fatalf("standalone thread id half-pinned a group thread path: args=%v forced=%v", args, forced)
	}
}

func TestRound13ConflictingOperatorThreadBindingIsRejected(t *testing.T) {
	_, err := NewHTTPHandler(testRoot, TrustedContext{ChannelID: "G1____T1", ChannelType: "5", ThreadID: "T2"}, cmdutil.GlobalOptions{})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting operator thread binding must fail: %v", err)
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
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
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
