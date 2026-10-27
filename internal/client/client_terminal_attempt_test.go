package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// The fixture commits before returning its error. No response code alone can
// erase that delivery from the final result, even on the very first attempt.
func TestDo_TerminalAttemptKeepsAppliedState(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPatch} {
		for _, mode := range []string{"default", "no-retry", "disable-retry", "replay-safe-no-retry"} {
			for _, status := range []int{429, 500, 501} {
				t.Run(fmt.Sprintf("%s/%s/%d", method, mode, status), func(t *testing.T) {
					var committed atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						if string(body) != `{"value":"committed"}` {
							t.Errorf("unexpected mutation body: %s", body)
						}
						committed.Add(1)
						w.Header().Set("Retry-After", "7")
						w.WriteHeader(status)
						_, _ = fmt.Fprintf(w, `{"error":{"code":"MOCK_%d","message":"committed before refusal"}}`, status)
					}))
					defer srv.Close()
					c := newTestClient(srv)
					c.options.NoRetry = mode == "no-retry" || mode == "replay-safe-no-retry"
					req := &Request{Method: method, Path: "/mutate", Body: map[string]any{"value": "committed"}, DisableRetry: mode == "disable-retry", ReplaySafe: mode == "replay-safe-no-retry"}
					_, err := c.Do(context.Background(), req)
					ee := output.AsExitError(err)
					if committed.Load() != 1 {
						t.Fatalf("commits=%d, want one", committed.Load())
					}
					if ee == nil || ee.Code != "RESULT_UNKNOWN" || ee.HTTPStatus != status || ee.NotProcessed || !ee.OutcomeUnknown() {
						t.Fatalf("terminal committed mutation must be unknown: %+v", ee)
					}
					if !strings.Contains(ee.Message, "committed before refusal") || !strings.Contains(string(ee.Detail), fmt.Sprintf("MOCK_%d", status)) || !strings.Contains(ee.Hint, "do not retry automatically") {
						t.Errorf("lost diagnostic or recovery: %+v", ee)
					}
					if status == 429 && (!strings.Contains(ee.Hint, "Retry-After") || !strings.Contains(ee.Hint, "7s")) {
						t.Errorf("lost Retry-After: %+v", ee)
					}
				})
			}
		}
	}
}
