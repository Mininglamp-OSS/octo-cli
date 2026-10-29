package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

type xin2549RT func(*http.Request) (*http.Response, error)

func (f xin2549RT) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestXIN2549DefiniteRefusals(t *testing.T) {
	for _, method := range []string{"POST", "PATCH"} {
		for _, status := range []int{400, 401, 409} {
			t.Run(fmt.Sprintf("%s/%d", method, status), func(t *testing.T) {
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					w.WriteHeader(status)
					w.Write([]byte(`{"error":"refused"}`))
				}))
				defer srv.Close()
				_, err := newTestClient(srv).Do(context.Background(), &Request{Method: method, Path: "/fixture"})
				ee := output.AsExitError(err)
				if ee == nil {
					t.Fatal("missing refusal")
				}
				t.Logf("deliveries=%d code=%s http_status=%d", hits.Load(), ee.Code, ee.HTTPStatus)
				if hits.Load() != 1 || ee.Code == "RESULT_UNKNOWN" || ee.HTTPStatus != status {
					t.Error("definite first refusal contract changed")
				}
			})
		}
	}
}

func TestXIN2549PreSendCancellation(t *testing.T) {
	for _, method := range []string{"POST", "PATCH"} {
		for _, mode := range []string{"default", "no-retry", "disable-retry", "replay-safe"} {
			for _, expired := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/expired=%t", method, mode, expired), func(t *testing.T) {
					var hits atomic.Int32
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) }))
					defer srv.Close()
					c := newTestClient(srv)
					c.options.NoRetry = mode == "no-retry"
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					if expired {
						var stop context.CancelFunc
						ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
						defer stop()
					}
					_, err := c.Do(ctx, &Request{Method: method, Path: "/mutation", Body: map[string]any{"value": "inert"}, DisableRetry: mode == "disable-retry", ReplaySafe: mode == "replay-safe"})
					ee := output.AsExitError(err)
					if ee == nil {
						t.Fatal("missing classified error")
					}
					t.Logf("deliveries=%d code=%s not_processed=%t", hits.Load(), ee.Code, ee.NotProcessed)
					if hits.Load() != 0 || ee.Code == "RESULT_UNKNOWN" || !ee.NotProcessed {
						t.Error("proven-unsent first attempt must retain NotProcessed and must not be RESULT_UNKNOWN")
					}
				})
			}
		}
	}
}

func TestXIN2549CancellationHistoryControls(t *testing.T) {
	for _, method := range []string{"POST", "PATCH"} {
		for _, phase := range []string{"during-delivery", "between-applied", "between-unsent"} {
			t.Run(method+"/"+phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var hits atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if phase == "during-delivery" {
						cancel()
						<-r.Context().Done()
						return
					}
					w.WriteHeader(503)
					w.Write([]byte(`{"error":"fixture"}`))
				}))
				defer srv.Close()
				c := newTestClient(srv)
				if phase == "between-applied" {
					base := c.httpClient.Transport
					c.httpClient.Transport = xin2549RT(func(r *http.Request) (*http.Response, error) {
						resp, err := base.RoundTrip(r)
						cancel()
						return resp, err
					})
				}
				if phase == "between-unsent" {
					ln, err := net.Listen("tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					addr := ln.Addr().String()
					ln.Close()
					c.cfg.APIBaseURL = "http://" + addr
					base := c.httpClient.Transport
					c.httpClient.Transport = xin2549RT(func(r *http.Request) (*http.Response, error) {
						resp, err := base.RoundTrip(r)
						cancel()
						return resp, err
					})
				}
				_, err := c.Do(ctx, &Request{Method: method, Path: "/mutation", ReplaySafe: phase != "during-delivery"})
				ee := output.AsExitError(err)
				if ee == nil {
					t.Fatal("missing error")
				}
				wantUnknown := phase != "between-unsent"
				wantHits := int32(1)
				if !wantUnknown {
					wantHits = 0
				}
				t.Logf("deliveries=%d code=%s not_processed=%t", hits.Load(), ee.Code, ee.NotProcessed)
				if hits.Load() != wantHits || (ee.Code == "RESULT_UNKNOWN") != wantUnknown || ee.NotProcessed == wantUnknown {
					t.Error("delivery history classification changed")
				}
			})
		}
	}
}
