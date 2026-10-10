package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestSearchQueryBounds(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		wantTerms   int
		truncated   bool
	}{
		{"ordinary", "SEND message", 2, false},
		{"terms", strings.Repeat("e ", 17), 16, true},
		{"runes", strings.Repeat("界", 513), 1, true},
		{"bytes", strings.Repeat("界", 1<<20), 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, terms, truncated := boundSearchQuery(tc.query)
			if len(q) > 8192 || utf8.RuneCountInString(q) > 512 || !utf8.ValidString(q) {
				t.Fatalf("query outside byte/rune/UTF-8 bounds: %d bytes, %d runes", len(q), utf8.RuneCountInString(q))
			}
			if len(terms) != tc.wantTerms || truncated != tc.truncated {
				t.Fatalf("terms=%d truncated=%v", len(terms), truncated)
			}
			if tc.name == "ordinary" && strings.Join(terms, " ") != "send message" {
				t.Fatalf("normal search semantics changed: %v", terms)
			}
		})
	}
	// Catch removing the pre-decode byte cap even if the later rune cap stays.
	// Allocate the hostile input before measuring; only preprocessing counts.
	huge := strings.Repeat("界", 1<<20)
	boundSearchQuery("warmup")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 4; i++ {
		boundSearchQuery(huge)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<20 {
		t.Fatalf("query preprocessing allocated %d bytes; byte cap must precede rune decoding", allocated)
	}
}

func searchRequest(query string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "search_ops", "arguments": map[string]any{"query": query}}})
	return b
}

func decodeSearchResponse(t *testing.T, raw []byte) toolResult {
	t.Helper()
	if len(raw) > 64<<10 {
		t.Fatalf("unbounded response: %d bytes", len(raw))
	}
	var resp rpcResponse
	if err := json.Unmarshal(raw, &resp); err != nil || resp.Error != nil || string(resp.ID) != "1" || resp.Jsonrpc != "2.0" {
		t.Fatalf("invalid JSON-RPC response: %v (%s)", err, raw)
	}
	var res toolResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSearchOps_TransportBounds(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	cli := &http.Client{Timeout: 2 * time.Second}
	for _, transport := range []string{"http", "stdio"} {
		for _, tc := range []struct {
			name, query string
			truncated   bool
			wantID      string
		}{
			{"ordinary", "SEND message", false, "message.send"},
			{"terms", strings.Repeat("message ", 16) + "not-a-match", true, "message.search"},
			{"ascii-large", strings.Repeat("e ", 2<<20), true, "bot.heartbeat"},
			{"multibyte-large", strings.Repeat("界 ", 1<<20), true, ""},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				request := searchRequest(tc.query)
				var raw []byte
				s := newTestServer(t)
				start := time.Now()
				if transport == "http" {
					// Real bearer-free HTTP path, including request timeout wiring.
					resp, err := cli.Post(ts.URL, "application/json", bytes.NewReader(request))
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != 200 {
						t.Fatalf("HTTP %d", resp.StatusCode)
					}
					var out bytes.Buffer
					if _, err := out.ReadFrom(resp.Body); err != nil {
						t.Fatal(err)
					}
					raw = out.Bytes()
				} else {
					var out bytes.Buffer
					if err := s.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
						t.Fatal(err)
					}
					raw = out.Bytes()
				}
				if elapsed := time.Since(start); elapsed > 2*time.Second {
					t.Fatalf("search exceeded bound: %s", elapsed)
				}
				res := decodeSearchResponse(t, raw)
				obj := toolText(t, res)
				if res.IsError {
					t.Fatalf("search error: %v", obj)
				}
				truncated, _ := obj["truncated"].(bool)
				if truncated != tc.truncated {
					t.Fatalf("truncated=%v, want %v", truncated, tc.truncated)
				}
				ops, ok := obj["operations"].([]any)
				if !ok || len(ops) > 20 {
					t.Fatalf("invalid operations: %v", obj)
				}
				found := false
				for _, op := range ops {
					if op.(map[string]any)["id"] == tc.wantID {
						found = true
					}
				}
				if tc.wantID != "" && !found {
					t.Fatalf("missing %s: %v", tc.wantID, ops)
				}
				if tc.truncated && !strings.Contains(obj["note"].(string), "8192 bytes / 512 runes / 16 terms") {
					t.Fatalf("missing bound notice: %v", obj)
				}
				t.Logf("%d query bytes completed in %s", len(tc.query), time.Since(start))
			})
		}
	}
}

// Deterministically cancel inside a loop, rather than racing a timer against
// the small embedded catalog. The real cancelled/deadline contexts are below.
type searchCancelContext struct {
	context.Context
	checks, after int
}

func (c *searchCancelContext) Err() error {
	c.checks++
	if c.checks >= c.after {
		return context.Canceled
	}
	return nil
}

func assertSearchCancelled(t *testing.T, res toolResult) {
	t.Helper()
	obj := toolText(t, res)
	if !res.IsError || obj["error"].(map[string]any)["code"] != "SEARCH_CANCELLED" || len(obj["operations"].([]any)) != 0 {
		t.Fatalf("cancellation must return an error without partial results: %v", obj)
	}
}

func TestSearchOps_Cancellation(t *testing.T) {
	s := newTestServer(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, ctx := range []context.Context{cancelled, expired} {
		for _, query := range []string{"", "message send"} {
			assertSearchCancelled(t, s.searchOps(ctx, "", query))
		}
	}
	// Entry check succeeds; cancellation occurs during operation iteration.
	ctx := &searchCancelContext{Context: context.Background(), after: 3}
	assertSearchCancelled(t, s.searchOps(ctx, "message", ""))
	if ctx.checks != 3 {
		t.Fatalf("scan continued after cancellation: %d checks", ctx.checks)
	}
	// Cancellation in the final operation must propagate as an error too.
	ops := s.reg.ListOperations("message")
	last := ops[len(ops)-1]
	ctx = &searchCancelContext{Context: context.Background(), after: len(ops) + 2}
	assertSearchCancelled(t, s.searchOps(ctx, "message", last.ID))
}

func TestMatchOp_CancellationBetweenTerms(t *testing.T) {
	op := registry.OperationInfo{ID: "message.send"}
	ctx := &searchCancelContext{Context: context.Background(), after: 2}
	matched, err := matchOp(ctx, op, []string{"message", "send"})
	if matched || !errors.Is(err, context.Canceled) || ctx.checks != 2 {
		t.Fatalf("term scan must stop on cancellation: matched=%v err=%v checks=%d", matched, err, ctx.checks)
	}
}

func TestSearchOps_HTTPRequestCancellation(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"client-cancel", "request-timeout"} {
		t.Run(mode, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", bytes.NewReader(searchRequest("send message")))
			if mode == "client-cancel" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			} else {
				h.requestTimeout = -time.Second
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("HTTP %d", w.Code)
			}
			assertSearchCancelled(t, decodeSearchResponse(t, w.Body.Bytes()))
		})
	}
}
