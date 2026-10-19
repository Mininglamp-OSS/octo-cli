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

func TestRound14SlowSmallBodiesDoNotDenyOtherClients(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
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
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
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
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
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
