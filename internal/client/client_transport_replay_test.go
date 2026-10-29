package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// reusedConnBackend serves a GET, so the client keeps the connection alive,
// and then reads a whole POST on that same connection before dropping it
// without an answer. It records each POST and the key header it carried.
type reusedConnBackend struct {
	srv   *httptest.Server
	mu    sync.Mutex
	posts int
	keys  []string
}

func newReusedConnBackend(t *testing.T, keyHeader string) *reusedConnBackend {
	t.Helper()
	b := &reusedConnBackend{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		b.mu.Lock()
		b.posts++
		b.keys = append(b.keys, r.Header.Get(keyHeader))
		b.mu.Unlock()
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	t.Cleanup(b.srv.Close)
	return b
}

// net/http resends a request it considers idempotent on its own when a reused
// keep-alive connection fails after the request was written, and it considers
// any request carrying an Idempotency-Key or X-Idempotency-Key header
// idempotent. A write the retry loop would not resend must reach the backend
// once, whichever body shape it has, with retries on or off, and the key header
// must still arrive.
func TestDo_TransportDoesNotReplayUnsafeKeyedWriteOnReusedConnection(t *testing.T) {
	bodies := map[string]func(*Request){
		"json": func(r *Request) { r.Body = map[string]any{"body": "hi"} },
		"raw":  func(r *Request) { r.RawBody = []byte("raw-body"); r.ContentType = "application/octet-stream" },
		"none": func(*Request) {},
	}
	for _, key := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		for name, setBody := range bodies {
			for _, noRetry := range []bool{false, true} {
				t.Run(key+"/"+name+"/noRetry="+map[bool]string{false: "off", true: "on"}[noRetry], func(t *testing.T) {
					b := newReusedConnBackend(t, key)
					c := New(&config.Config{APIBaseURL: b.srv.URL}, nil, Options{NoRetry: noRetry})
					c.httpClient = b.srv.Client()
					if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/v1/bot/docs/d1/scene"}); err != nil {
						t.Fatalf("warm-up GET: %v", err)
					}
					req := &Request{Method: http.MethodPost, Path: "/v1/bot/docs/d1/comments", Headers: map[string]string{key: "k-7"}}
					setBody(req)
					_, err := c.Do(context.Background(), req)
					b.mu.Lock()
					posts, keys := b.posts, append([]string(nil), b.keys...)
					b.mu.Unlock()
					if posts != 1 {
						t.Fatalf("POST deliveries = %d, want exactly 1", posts)
					}
					if keys[0] != "k-7" {
						t.Errorf("backend saw %s = %q, want k-7", key, keys[0])
					}
					if ee := output.AsExitError(err); ee == nil || ee.Code != "RESULT_UNKNOWN" {
						t.Errorf("err = %+v, want RESULT_UNKNOWN", ee)
					}
				})
			}
		}
	}
}

// --no-retry means one delivery at every layer, also for a GET the Transport
// would otherwise resend on a reused connection. Without --no-retry the GET
// keeps its retries.
func TestDo_NoRetryGetIsNotReplayedByTransport(t *testing.T) {
	for _, noRetry := range []bool{true, false} {
		var gets int32
		var mu sync.Mutex
		first := true
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			warm := first
			first = false
			mu.Unlock()
			if warm {
				_, _ = w.Write([]byte(`{"ok":true}`))
				return
			}
			atomic.AddInt32(&gets, 1)
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		}))
		c := New(&config.Config{APIBaseURL: srv.URL}, nil, Options{NoRetry: noRetry})
		c.httpClient = srv.Client()
		if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/warm"}); err != nil {
			t.Fatalf("warm-up GET: %v", err)
		}
		_, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/x"})
		srv.Close()
		got := atomic.LoadInt32(&gets)
		if noRetry && got != 1 {
			t.Errorf("--no-retry GET deliveries = %d, want 1", got)
		}
		if !noRetry && got < 2 {
			t.Errorf("GET deliveries = %d, want the retries kept", got)
		}
		if err == nil {
			t.Errorf("noRetry=%v: want a failure", noRetry)
		}
	}
}

// forbidTransportReplay keeps the key header on the wire and does not change
// what a safe request sends.
func TestForbidTransportReplayKeepsWireShape(t *testing.T) {
	var got http.Header
	var gotBody []byte
	var gotLen int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		gotLen = r.ContentLength
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(&config.Config{APIBaseURL: srv.URL}, nil, Options{NoRetry: true})
	c.httpClient = srv.Client()

	if _, err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Headers: map[string]string{"Idempotency-Key": "k", "X-Idempotency-Key": "x"}, Body: map[string]any{"a": 1}}); err != nil {
		t.Fatal(err)
	}
	if got.Get("Idempotency-Key") != "k" || got.Get("X-Idempotency-Key") != "x" || string(gotBody) != `{"a":1}` || gotLen != int64(len(`{"a":1}`)) {
		t.Fatalf("POST arrived as headers=%v body=%q length=%d", got, gotBody, gotLen)
	}
	if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/x"}); err != nil {
		t.Fatal(err)
	}
	if len(gotBody) != 0 || got.Get("Transfer-Encoding") != "" || got.Get("Content-Length") != "" {
		t.Fatalf("GET arrived with a body: headers=%v body=%q", got, gotBody)
	}
}

// A replay-safe keyed write may be resent, but only by the retry loop, which
// records every attempt. If net/http resent it on its own after the reused
// connection dropped the response, the loop would see only the final refused
// dial and report a request that reached the backend as never sent.
func TestDo_ReplaySafeWriteOnReusedConnectionKeepsItsHistory(t *testing.T) {
	var posts int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		atomic.AddInt32(&posts, 1)
		_ = srv.Listener.Close()
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()
	c := New(&config.Config{APIBaseURL: srv.URL}, nil, Options{})
	c.httpClient = srv.Client()
	if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/fleet/api/v1/docs/d1"}); err != nil {
		t.Fatalf("warm-up GET: %v", err)
	}
	_, err := c.Do(context.Background(), &Request{
		Method: http.MethodPost, Path: "/fleet/api/v1/capabilities/install",
		Headers:    map[string]string{"Idempotency-Key": "cap-1", "X-Workspace-ID": "w1"},
		Body:       map[string]any{"definition": map[string]any{"name": "c"}},
		ReplaySafe: true,
	})
	if got := atomic.LoadInt32(&posts); got != 1 {
		t.Fatalf("POST deliveries = %d, want 1", got)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() || ee.NotProcessed {
		t.Fatalf("err = %+v, want RESULT_UNKNOWN for a write that reached the backend", ee)
	}
}

// The explicit loop still retries a replay-safe write on a reused connection,
// and the key and body reach the backend on every attempt.
func TestDo_ReplaySafeWriteOnReusedConnectionStillRetries(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		mu.Lock()
		seen = append(seen, r.Header.Get("Idempotency-Key")+"|"+string(body))
		n := len(seen)
		mu.Unlock()
		if n == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := New(&config.Config{APIBaseURL: srv.URL}, nil, Options{})
	c.httpClient = srv.Client()
	if _, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/warm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Headers: map[string]string{"Idempotency-Key": "cap-2"}, Body: map[string]any{"a": 1}, ReplaySafe: true}); err != nil {
		t.Fatalf("want success on the retry, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != `cap-2|{"a":1}` || seen[1] != seen[0] {
		t.Fatalf("deliveries = %q, want two identical keyed deliveries", seen)
	}
}
