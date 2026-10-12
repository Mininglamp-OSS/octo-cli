package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// unknownIDInputs are attacker-sized unknown operation_ids: ASCII (1 byte per
// rune, worst case for []rune expansion) and multibyte (non-ASCII lowercase
// path). Both carry upper-case runes so a full-input strings.ToLower cannot
// take its no-change fast path. Both are ~7 MiB, under the 8 MiB transport cap.
func unknownIDInputs() map[string]string {
	return map[string]string{
		"ascii":     strings.Repeat("Z", 7<<20),
		"multibyte": strings.Repeat("Ж界", (7<<20)/5),
	}
}

// boundedPrefix is the test's own copy of the expected bound, independent of
// boundOperationID, so removing the production bound cannot also shrink the
// baseline it is compared against.
func boundedPrefix(s string) string {
	if len(s) <= maxOperationIDBytes {
		return s
	}
	end := maxOperationIDBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end]
}

// allocatedBytes reports the minimum TotalAlloc delta over a few runs of fn, so
// a stray background allocation cannot fail the comparison.
func allocatedBytes(fn func()) uint64 {
	fn() // warm caches (registry walks, sync.Once) outside the measurement
	best := ^uint64(0)
	for i := 0; i < 3; i++ {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		fn()
		runtime.ReadMemStats(&after)
		if d := after.TotalAlloc - before.TotalAlloc; d < best {
			best = d
		}
	}
	return best
}

// inputProportionalSlack is the allowed allocation difference between a ~7 MiB
// operation_id and its already-bounded prefix. Unbounded lowercase/[]rune
// conversion of the full input costs 16-35 MiB here.
const inputProportionalSlack = 2 << 20

func TestBoundOperationID_ByteFirstOnRuneBoundary(t *testing.T) {
	if got, cut := boundOperationID("message.send"); got != "message.send" || cut {
		t.Fatalf("short id must pass through, got %q cut=%v", got, cut)
	}
	exact := strings.Repeat("a", maxOperationIDBytes)
	if got, cut := boundOperationID(exact); got != exact || cut {
		t.Fatalf("id at the byte cap must not be cut")
	}
	for _, r := range []string{"a", "é", "界", "😀"} {
		in := strings.Repeat(r, maxOperationIDBytes+1) // always > cap
		got, cut := boundOperationID(in)
		if !cut || len(got) > maxOperationIDBytes || !utf8.ValidString(got) || !strings.HasPrefix(in, got) {
			t.Fatalf("%q: bound must be a valid UTF-8 prefix within %d bytes, got %d bytes cut=%v", r, maxOperationIDBytes, len(got), cut)
		}
		if maxOperationIDBytes-len(got) >= len(r) {
			t.Fatalf("%q: bound dropped a whole rune that fit (%d bytes kept)", r, len(got))
		}
	}
	// The byte cap still covers the echo and ranking rune caps for 4-byte runes.
	if maxEchoedOperationIDRunes*utf8.UTFMax > maxOperationIDBytes || maxRankQueryRunes*utf8.UTFMax > maxOperationIDBytes {
		t.Fatal("maxOperationIDBytes must cover the rune caps")
	}
}

// TestUnknownOperationPayload_AllocationIsBounded is allocation-sensitive: the
// unknown-operation path must allocate the same for a ~7 MiB id as for its
// bounded prefix, which fails if the byte bound is removed or a full-input
// lowercase / []rune conversion is reintroduced ahead of it.
func TestUnknownOperationPayload_AllocationIsBounded(t *testing.T) {
	reg := registry.MustNew()
	ctx := context.Background()
	for name, huge := range unknownIDInputs() {
		t.Run(name, func(t *testing.T) {
			prefix := boundedPrefix(huge)
			run := func(id string) func() {
				return func() {
					if _, err := unknownOperationPayload(ctx, reg, id); err != nil {
						t.Fatal(err)
					}
				}
			}
			small := allocatedBytes(run(prefix))
			large := allocatedBytes(run(huge))
			if large > small+inputProportionalSlack {
				t.Fatalf("unknown operation path allocated %d bytes for %d-byte id vs %d for its bounded prefix; must not scale with input", large, len(huge), small)
			}
			p, err := unknownOperationPayload(ctx, reg, huge)
			if err != nil {
				t.Fatal(err)
			}
			msg := p["error"].(map[string]any)["message"].(string)
			if n := utf8.RuneCountInString(msg); n > maxEchoedOperationIDRunes+32 || !strings.Contains(msg, "…") {
				t.Fatalf("echoed id must be truncated with a marker, got %d runes", n)
			}
		})
	}
}

func describeRequest(tool, id string) []byte {
	key := "operation_id"
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": map[string]any{key: id}}})
	return b
}

func searchRequestSized(query string) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "search_ops", "arguments": map[string]any{"query": query}}})
	return b
}

// TestUnknownOperation_TransportAllocationIsBounded drives the real
// bearer-free HTTP handler and the stdio loop. The baseline is a same-size
// search_ops request (identical transport/JSON decode cost, already bounded),
// so the difference isolates the unknown-operation rendering and ranking. The
// same comparison at the bounded prefix size gives the constant ranking cost.
func TestUnknownOperation_TransportAllocationIsBounded(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()
	stdio := newTestServer(t)

	send := map[string]func(t *testing.T, body []byte) []byte{
		"http": func(t *testing.T, body []byte) []byte {
			resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var out bytes.Buffer
			_, _ = out.ReadFrom(resp.Body)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("HTTP %d: %s", resp.StatusCode, out.String())
			}
			return out.Bytes()
		},
		"stdio": func(t *testing.T, body []byte) []byte {
			var out bytes.Buffer
			if err := stdio.ServeStdio(context.Background(), bytes.NewReader(append(body, '\n')), &out); err != nil {
				t.Fatal(err)
			}
			return out.Bytes()
		},
	}
	for transport, do := range send {
		for _, tool := range []string{"describe_op", "call_op"} {
			for name, huge := range unknownIDInputs() {
				t.Run(transport+"/"+tool+"/"+name, func(t *testing.T) {
					prefix := boundedPrefix(huge)
					cost := func(id string) uint64 {
						opBody, searchBody := describeRequest(tool, id), searchRequestSized(id)
						op := allocatedBytes(func() { do(t, opBody) })
						base := allocatedBytes(func() { do(t, searchBody) })
						if op < base {
							return 0
						}
						return op - base
					}
					constant, large := cost(prefix), cost(huge)
					t.Logf("unknown-op extra allocation: %d bytes at %d-byte id, %d bytes at bounded prefix", large, len(huge), constant)
					if large > constant+inputProportionalSlack {
						t.Fatalf("%s %s allocated %d extra bytes for a %d-byte id (bounded prefix: %d); must not scale with input", transport, tool, large, len(huge), constant)
					}
					res := decodeSearchResponse(t, do(t, describeRequest(tool, huge)))
					p := toolText(t, res)
					if !res.IsError || p["error"].(map[string]any)["code"] != "UNKNOWN_OPERATION" {
						t.Fatalf("want UNKNOWN_OPERATION, got %v", p)
					}
				})
			}
		}
	}
}

// countingCtx reports cancellation after `after` calls to Err, so tests can
// prove ctx is consulted inside the ranking loop rather than only around it.
type countingCtx struct {
	context.Context
	calls atomic.Int64
	after int64
}

func (c *countingCtx) Err() error {
	if c.calls.Add(1) > c.after {
		return context.Canceled
	}
	return nil
}

func TestNearestOperations_ChecksContextPerOperation(t *testing.T) {
	reg := registry.MustNew()
	n := int64(len(reg.EnabledOperations()))
	never := &countingCtx{Context: context.Background(), after: 1 << 62}
	if _, err := nearestOperations(never, reg, "message.snd"); err != nil {
		t.Fatal(err)
	}
	if got := never.calls.Load(); got < n {
		t.Fatalf("ranking consulted ctx %d times over %d operations; it must check every iteration", got, n)
	}
	mid := &countingCtx{Context: context.Background(), after: n / 2}
	got, err := nearestOperations(mid, reg, "message.snd")
	if err == nil || got != nil {
		t.Fatalf("cancellation mid-ranking must return an error and no partial candidates, got %v, %v", got, err)
	}
	if calls := mid.calls.Load(); calls != n/2+1 {
		t.Fatalf("ranking must stop at the first cancelled check, ctx consulted %d times", calls)
	}
}

func assertCancelledLookup(t *testing.T, res toolResult) {
	t.Helper()
	p := toolText(t, res)
	e, _ := p["error"].(map[string]any)
	if !res.IsError || e["code"] != "REQUEST_CANCELLED" || e["type"] != "cancelled" || p["ok"] != false {
		t.Fatalf("want structured REQUEST_CANCELLED, got %v", p)
	}
	if _, ok := p["candidates"]; ok {
		t.Fatalf("a cancelled lookup must not return partial candidates: %v", p)
	}
}

func TestUnknownOperation_CancelledContext(t *testing.T) {
	s := newTestServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	huge := strings.Repeat("界", 1<<20)
	for _, id := range []string{"message.snd", huge} {
		assertCancelledLookup(t, s.describeOp(ctx, id))
		assertCancelledLookup(t, s.callOp(ctx, id, nil))
	}
	// Cancellation that lands during ranking, not just before it.
	n := int64(len(s.reg.EnabledOperations()))
	assertCancelledLookup(t, s.describeOp(&countingCtx{Context: context.Background(), after: n / 3}, "message.snd"))
	assertCancelledLookup(t, s.callOp(&countingCtx{Context: context.Background(), after: n / 3}, "message.snd", nil))

	// Transport parity: a cancelled request context reaches the lookup over
	// both the HTTP handler and the stdio loop.
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"describe_op", "call_op"} {
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(describeRequest(tool, "message.snd"))).WithContext(ctx)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assertCancelledLookup(t, decodeSearchResponse(t, rec.Body.Bytes()))

		var out bytes.Buffer
		if err := s.ServeStdio(ctx, bytes.NewReader(append(describeRequest(tool, "message.snd"), '\n')), &out); err != nil {
			t.Fatal(err)
		}
		assertCancelledLookup(t, decodeSearchResponse(t, out.Bytes()))
	}
	// A live context still ranks normally.
	p := toolText(t, s.describeOp(context.Background(), "message.snd"))
	if c, _ := p["candidates"].([]any); len(c) == 0 || c[0] != "message.send" {
		t.Fatalf("live lookup must still rank candidates, got %v", p["candidates"])
	}
}
