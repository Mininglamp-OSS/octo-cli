package client

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// The outcome of a request is decided by its whole attempt history. Once one
// attempt may have taken effect, no later attempt can make the operation a
// definite "not processed" failure; only a definitive response ends the
// uncertainty.

// closingBackend answers its first request as mode says and then stops
// listening, so every later attempt fails to dial. "drop" reads the request and
// closes the connection without answering.
func closingBackend(t *testing.T, mode string, hits *int32) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(hits, 1)
		_ = srv.Listener.Close()
		switch mode {
		case "drop":
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
		case "404":
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE","message":"unavailable from mock"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDo_EarlierAmbiguousAttemptSurvivesLaterDialFailures(t *testing.T) {
	for _, mode := range []string{"503", "drop"} {
		for _, c := range []struct {
			name string
			req  Request
		}{
			{"replay-safe POST", Request{Method: http.MethodPost, Path: "/v1/bot/docs", Headers: map[string]string{"Idempotency-Key": "k-1"}, Body: map[string]any{"docType": "html_ppt"}, ReplaySafe: true}},
			{"PUT", Request{Method: http.MethodPut, Path: "/x", Body: map[string]any{"a": 1}}},
			{"DELETE", Request{Method: http.MethodDelete, Path: "/x"}},
		} {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				var hits int32
				srv := closingBackend(t, mode, &hits)
				req := c.req
				_, err := newTestClient(srv).Do(context.Background(), &req)
				if got := atomic.LoadInt32(&hits); got != 1 {
					t.Fatalf("backend deliveries = %d, want 1", got)
				}
				ee := output.AsExitError(err)
				if ee == nil || ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() || ee.NotProcessed {
					t.Fatalf("err = %+v, want RESULT_UNKNOWN after an attempt that may have applied", ee)
				}
			})
		}
	}
}

// A cancellation while waiting to resend sends nothing, so it cannot erase an
// earlier attempt that may have applied the write.
func TestDo_CancelAfterAmbiguousAttemptStaysUnknown(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := newTestClient(srv).Do(ctx, &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}, ReplaySafe: true})
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("backend deliveries = %d, want 1", got)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() || ee.NotProcessed {
		t.Fatalf("err = %+v, want RESULT_UNKNOWN", ee)
	}
}

// A definitive success after ambiguous attempts is a success.
func TestDo_DefinitiveSuccessEndsUncertainty(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	body, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}, ReplaySafe: true})
	if err != nil || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("want success, got %s %v", body, err)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("backend deliveries = %d, want 3", got)
	}
}

// Every attempt of a replay-safe write keeps failing ambiguously: retries are
// exhausted and the result is RESULT_UNKNOWN, the same code a single unkeyed
// delivery gets.
func TestDo_ReplaySafeWriteExhaustionIsUnknown(t *testing.T) {
	for _, mode := range []string{"drop", "502", "503", "504"} {
		t.Run(mode, func(t *testing.T) {
			var hits int32
			srv := writeRetryBackend(t, mode, &hits)
			_, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Headers: map[string]string{"Idempotency-Key": "k-2"}, Body: map[string]any{}, ReplaySafe: true})
			if got := atomic.LoadInt32(&hits); got != defaultMaxRetries+1 {
				t.Fatalf("backend deliveries = %d, want %d", got, defaultMaxRetries+1)
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() {
				t.Fatalf("err = %+v, want RESULT_UNKNOWN", ee)
			}
		})
	}
}

// A DELETE whose first attempt committed and lost its response replays into
// 404. That 404 does not prove the DELETE failed.
func TestDo_DeleteReplayInto404IsUnknown(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			conn, _, _ := w.(http.Hijacker).Hijack()
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"no such doc"}}`))
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodDelete, Path: "/v1/bot/docs/d1"})
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("backend deliveries = %d, want 2", got)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() || !strings.Contains(ee.Message, "no such doc") {
		t.Fatalf("err = %+v, want RESULT_UNKNOWN keeping the 404 message", ee)
	}

	// Control: a 404 on the first attempt is a definite refusal.
	atomic.StoreInt32(&hits, 1)
	_, err = newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodDelete, Path: "/v1/bot/docs/d1"})
	if ee := output.AsExitError(err); ee == nil || ee.Code != "NOT_FOUND" || ee.OutcomeUnknown() {
		t.Fatalf("err = %+v, want a definite NOT_FOUND", ee)
	}
}

// Only a failed dial proves that nothing reached the server. A connection
// reset after the whole request was written may follow a write the server
// applied, so it is neither resent nor reported as not processed.
func TestDo_ResetAfterBodyIsNotADialFailure(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(&hits, 1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetLinger(0) // RST instead of FIN
		}
		_ = conn.Close()
	}))
	defer srv.Close()
	_, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{"a": 1}})
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("backend deliveries = %d, want 1", got)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "RESULT_UNKNOWN" || ee.NotProcessed {
		t.Fatalf("err = %+v, want RESULT_UNKNOWN", ee)
	}

	// The same distinction on the error values the transport produces.
	for _, c := range []struct {
		op      string
		applied bool
	}{{"dial", false}, {"read", true}, {"write", true}} {
		var calls int32
		cl := New(&config.Config{APIBaseURL: "http://api.example.test"}, nil, Options{})
		cl.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			atomic.AddInt32(&calls, 1)
			return nil, &net.OpError{Op: c.op, Net: "tcp", Err: errors.New("connection reset by peer")}
		})}
		_, err := cl.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}})
		ee := output.AsExitError(err)
		wantCalls := int32(defaultMaxRetries + 1)
		if c.applied {
			wantCalls = 1
		}
		if got := atomic.LoadInt32(&calls); got != wantCalls {
			t.Errorf("%s: attempts = %d, want %d", c.op, got, wantCalls)
		}
		if ee == nil || ee.NotProcessed == c.applied || (ee.Code == "RESULT_UNKNOWN") != c.applied {
			t.Errorf("%s: err = %+v, want applied=%v", c.op, ee, c.applied)
		}
	}
}

// A POST is still resent after a failed dial and a replay-safe POST after an
// ambiguous failure, so the retry diagnostics for both must keep request
// secrets and response-secret operations masked.
func TestDo_POSTRetryLogMasksSecrets(t *testing.T) {
	const marker = "post-retry-log-secret-6c2f"
	for _, c := range []struct {
		name string
		req  Request
		err  error
	}{
		{
			"dial failure, request secret",
			Request{Method: http.MethodPost, Path: "/v1/drive/share/" + marker + "/accept", SecretValues: []string{marker}, Body: map[string]any{}},
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/drive/share/" + marker + "/accept", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
		},
		{
			"dial failure, response secret operation",
			Request{Method: http.MethodPost, Path: "/v1/user/drive/files/42/upload/parts-presign", ResponseSecretFields: []string{"parts[].url"}, Body: map[string]any{}},
			&url.Error{Op: "Post", URL: "https://objects.example.test/object?" + marker, Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}},
		},
		{
			"replay-safe transport error, request secret",
			Request{Method: http.MethodPost, Path: "/v1/x/" + marker, SecretValues: []string{marker}, Body: map[string]any{}, ReplaySafe: true},
			&url.Error{Op: "Post", URL: "https://api.example.test/v1/x/" + marker, Err: errors.New("connection reset by peer")},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var diagnostics bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			cl := New(&config.Config{APIBaseURL: "https://api.example.test"}, nil, Options{Verbose: true, ErrOut: &diagnostics})
			cl.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				cancel() // one retry diagnostic, then the backoff is cancelled
				return nil, c.err
			})}
			req := c.req
			_, err := cl.Do(ctx, &req)
			if err == nil {
				t.Fatal("expected a failure")
			}
			if !strings.Contains(diagnostics.String(), "retry #1") {
				t.Fatalf("retry diagnostics were not exercised: %q", diagnostics.String())
			}
			if strings.Contains(diagnostics.String(), marker) || strings.Contains(err.Error(), marker) {
				t.Fatalf("secret leaked:\n%s\n%v", diagnostics.String(), err)
			}
		})
	}
}
