package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// A non-idempotent write must reach the backend once when the outcome of that
// delivery is ambiguous: the server read the whole request and then dropped the
// connection, answered 502/503/504, or did not answer before the deadline.
// Resending it could repeat the side effect (a second message, a second
// order), so the failure is reported as RESULT_UNKNOWN instead.

// writeRetryBackend reads every request body in full, counts it, and then
// misbehaves as mode says: "drop" closes the connection without a response,
// "hang" waits for the client to give up, and a status code is answered as is.
func writeRetryBackend(t *testing.T, mode string, hits *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		atomic.AddInt32(hits, 1)
		switch mode {
		case "drop":
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			_ = conn.Close()
		case "hang":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		case "502":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"msg":"bad gateway from mock"}`))
		case "503":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"msg":"unavailable from mock"}`))
		case "504":
			w.WriteHeader(http.StatusGatewayTimeout)
			_, _ = w.Write([]byte(`{"msg":"gateway timeout from mock"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDo_NonIdempotentWriteIsDeliveredOnceOnAmbiguousFailure(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for _, mode := range []string{"drop", "502", "503", "504"} {
			t.Run(method+"/"+mode, func(t *testing.T) {
				var hits int32
				srv := writeRetryBackend(t, mode, &hits)
				c := newTestClient(srv)
				_, err := c.Do(context.Background(), &Request{Method: method, Path: "/v1/bot/sendMessage", Body: map[string]any{"content": "once"}})
				if got := atomic.LoadInt32(&hits); got != 1 {
					t.Fatalf("backend deliveries = %d, want exactly 1", got)
				}
				ee := output.AsExitError(err)
				if ee == nil {
					t.Fatalf("want a structured error, got %v", err)
				}
				if ee.Code != "RESULT_UNKNOWN" || !ee.OutcomeUnknown() {
					t.Errorf("code = %q outcomeUnknown = %v, want RESULT_UNKNOWN and unknown", ee.Code, ee.OutcomeUnknown())
				}
				if !strings.Contains(ee.Hint, "do not retry automatically") {
					t.Errorf("hint = %q", ee.Hint)
				}
				if mode != "drop" && !strings.Contains(ee.Message, "from mock") {
					t.Errorf("the backend diagnostic was lost: %q", ee.Message)
				}
			})
		}
	}
}

func TestDo_NonIdempotentWriteIsDeliveredOnceOnTimeoutAndCancel(t *testing.T) {
	t.Run("client timeout", func(t *testing.T) {
		var hits int32
		srv := writeRetryBackend(t, "hang", &hits)
		c := New(&config.Config{APIBaseURL: srv.URL}, &credential.BotCredential{Token: "app_test"}, Options{Timeout: "200ms"})
		_, err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/v1/bot/sendMessage", Body: map[string]any{"content": "once"}})
		if got := atomic.LoadInt32(&hits); got != 1 {
			t.Fatalf("backend deliveries = %d, want exactly 1", got)
		}
		if ee := output.AsExitError(err); ee == nil || ee.Code != "RESULT_UNKNOWN" {
			t.Errorf("err = %v, want RESULT_UNKNOWN", err)
		}
	})
	t.Run("context cancelled in flight", func(t *testing.T) {
		var hits int32
		srv := writeRetryBackend(t, "hang", &hits)
		c := newTestClient(srv)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := c.Do(ctx, &Request{Method: http.MethodPost, Path: "/v1/bot/sendMessage", Body: map[string]any{"content": "once"}})
		if got := atomic.LoadInt32(&hits); got != 1 {
			t.Fatalf("backend deliveries = %d, want exactly 1", got)
		}
		if ee := output.AsExitError(err); ee == nil || ee.Code != "RESULT_UNKNOWN" {
			t.Errorf("err = %v, want RESULT_UNKNOWN", err)
		}
	})
}

// A write the server refused with 429 was not processed, so it is still
// retried, and Retry-After is honoured.
func TestDo_NonIdempotentWriteRetriesAfter429(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	start := time.Now()
	if _, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}}); err != nil {
		t.Fatalf("want success after a 429, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("deliveries = %d, want 2", got)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("Retry-After: 1 was not honoured (elapsed %s)", time.Since(start))
	}
}

// A dial failure never reached the server: it stays a plain NETWORK_ERROR that
// is known not to have been applied, and the write is still retried.
func TestDo_NonIdempotentWriteRetriesAndReportsNotSentOnDialFailure(t *testing.T) {
	var calls int32
	c := New(&config.Config{APIBaseURL: "http://127.0.0.1:1"}, nil, Options{})
	c.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	})}
	_, err := c.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}})
	if got := atomic.LoadInt32(&calls); got != defaultMaxRetries+1 {
		t.Errorf("attempts = %d, want %d", got, defaultMaxRetries+1)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "NETWORK_ERROR" || !ee.NotProcessed || ee.OutcomeUnknown() {
		t.Fatalf("err = %+v, want a NETWORK_ERROR known not to have been sent", ee)
	}
}

// Cancelling while waiting to resend a write that was refused with 429 leaves
// nothing applied, so the result is not reported as unknown.
func TestDo_NonIdempotentWriteCancelledDuringBackoffIsNotUnknown(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := newTestClient(srv).Do(ctx, &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{}})
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("deliveries = %d, want 1", got)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code == "RESULT_UNKNOWN" || ee.OutcomeUnknown() {
		t.Fatalf("err = %+v, want a cancellation known not to have applied the write", ee)
	}
}

// Idempotent methods keep the transport retries for transient failures.
func TestDo_IdempotentMethodsStillRetryTransientFailures(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if atomic.AddInt32(&hits, 1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer srv.Close()
			if _, err := newTestClient(srv).Do(context.Background(), &Request{Method: method, Path: "/x"}); err != nil {
				t.Fatalf("want success after one 503, got %v", err)
			}
			if got := atomic.LoadInt32(&hits); got != 2 {
				t.Errorf("deliveries = %d, want 2", got)
			}
		})
	}
}

// A write whose backend deduplicates every resend keeps the retries;
// DisableRetry still wins.
func TestDo_ReplaySafeWriteRetries(t *testing.T) {
	for _, disable := range []bool{false, true} {
		var hits int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&hits, 1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		_, err := newTestClient(srv).Do(context.Background(), &Request{Method: http.MethodPost, Path: "/x", Body: map[string]any{"idempotency_key": "k"}, ReplaySafe: true, DisableRetry: disable})
		srv.Close()
		want := int32(2)
		if disable {
			want = 1
		}
		if got := atomic.LoadInt32(&hits); got != want {
			t.Errorf("DisableRetry=%v: deliveries = %d, want %d (err %v)", disable, got, want, err)
		}
	}
}

func TestIsIdempotentMethod(t *testing.T) {
	for m, want := range map[string]bool{
		"": true, "GET": true, "get": true, "HEAD": true, "OPTIONS": true, "TRACE": true, "PUT": true, "DELETE": true,
		"POST": false, "PATCH": false, "post": false, "CONNECT": false,
	} {
		if got := isIdempotentMethod(m); got != want {
			t.Errorf("isIdempotentMethod(%q) = %v, want %v", m, got, want)
		}
	}
}
