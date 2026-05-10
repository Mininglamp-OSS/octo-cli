package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dmwork-org/octo-cli/internal/output"
)

// TestDo_RetryExhaustionPreservesExitError verifies that after exhausting
// retries on a retryable status, the returned error unwraps to the
// structured *ExitError (with correct Code and ExitCode) — so callers
// downstream can still switch on err type and exit with the right code.
func TestDo_RetryExhaustionPreservesExitError(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"msg":"upstream down","status":503}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	// Keep the retry loop short — NoRetry=false still uses the full 4 attempts.
	// We can't control retryClock (removed), so just rely on the bounded attempts.
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/flaky"})
	if err == nil {
		t.Fatalf("expected error after exhausting retries")
	}

	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("AsExitError(err) = nil; want unwrap to *ExitError (err=%T: %v)", err, err)
	}
	// 503 → UPSTREAM_UNAVAILABLE via status inference
	if ee.Code != "UPSTREAM_UNAVAILABLE" {
		t.Errorf("Code = %q, want UPSTREAM_UNAVAILABLE", ee.Code)
	}
	if ee.ExitCode() != 1 {
		t.Errorf("ExitCode = %d, want 1", ee.ExitCode())
	}
	if atomic.LoadInt32(&calls) < 2 {
		t.Errorf("expected multiple attempts, got %d", calls)
	}
	// Also ensure errors.As works through the retryable wrapper.
	var asEE *output.ExitError
	if !errors.As(err, &asEE) {
		t.Errorf("errors.As(err, &ExitError) = false")
	}
}
