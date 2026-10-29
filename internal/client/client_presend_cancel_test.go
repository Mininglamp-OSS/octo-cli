package client

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

type cancelBeforeSendWriter struct {
	cancel             context.CancelFunc
	attempts, cancelAt int
}

func (w *cancelBeforeSendWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "POST http") {
		w.attempts++
		if w.attempts == w.cancelAt {
			w.cancel()
		}
	}
	return len(p), nil
}

// Cancel after request construction, immediately before the transport seam.
// On the second attempt this must preserve both possible-delivery and dial-only histories.
func TestDo_CancellationBeforeEachSendKeepsHistory(t *testing.T) {
	for _, prior := range []string{"none", "dial", "503"} {
		t.Run(prior, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.WriteHeader(503)
			}))
			defer srv.Close()
			c := newTestClient(srv)
			cancelAt := 2
			if prior == "none" {
				cancelAt = 1
			}
			writer := &cancelBeforeSendWriter{cancel: cancel, cancelAt: cancelAt}
			c.options.Verbose, c.options.ErrOut = true, writer
			base := c.httpClient.Transport
			var calls int
			c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if prior == "dial" {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("fixture dial failure")}
				}
				return base.RoundTrip(r)
			})
			_, err := c.Do(ctx, &Request{Method: "POST", Path: "/fixture", ReplaySafe: true})
			ee := output.AsExitError(err)
			wantCalls, wantHits := cancelAt-1, int32(0)
			unknown := prior == "503"
			if unknown {
				wantHits = 1
			}
			if calls != wantCalls || hits.Load() != wantHits || writer.attempts != cancelAt {
				t.Fatalf("transport calls=%d deliveries=%d prepared attempts=%d; want %d/%d/%d", calls, hits.Load(), writer.attempts, wantCalls, wantHits, cancelAt)
			}
			if ee == nil || (ee.Code == "RESULT_UNKNOWN") != unknown || ee.NotProcessed == unknown {
				t.Fatalf("wrong cancellation classification: %+v", ee)
			}
		})
	}
}
