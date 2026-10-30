package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// XIN-2441: argument-name validation runs before credential resolution, so its
// work must be bounded by an explicit name budget, use a fold index instead of
// pairwise comparison, and stop when the request context ends.

// countingCtx reports cancellation once Err has been called more than live
// times, so a test can end the context deterministically part-way through
// validation instead of racing a wall-clock deadline.
type countingCtx struct {
	context.Context
	live  int64
	calls atomic.Int64
}

func (c *countingCtx) Err() error {
	if c.calls.Add(1) > c.live {
		return context.DeadlineExceeded
	}
	return nil
}

func manyArgs(n int, name func(int) string) map[string]any {
	args := make(map[string]any, n)
	for i := 0; i < n; i++ {
		args[name(i)] = 0
	}
	return args
}

func extraName(i int) string { return fmt.Sprintf("extra_%06d", i) }

func TestXIN2441ArgumentNameBudget(t *testing.T) {
	body := map[string]registry.SchemaInfo{"space_id": {}}
	at := manyArgs(maxArgumentNames, extraName)
	if err := checkArgumentNames(context.Background(), at, map[string]bool{}, nil, nil, body); err != nil {
		t.Fatalf("%d distinct names must be accepted: %v", maxArgumentNames, err)
	}
	over := manyArgs(maxArgumentNames+1, extraName)
	err := checkArgumentNames(context.Background(), over, map[string]bool{}, nil, nil, body)
	if err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("%d names must exceed the budget, got %v", maxArgumentNames+1, err)
	}
	// The budget is checked before any per-name work: a context that would
	// report cancellation on its second check never gets one.
	ctx := &countingCtx{Context: context.Background(), live: 1}
	err = checkArgumentNames(ctx, manyArgs(30000, extraName), map[string]bool{}, nil, nil, body)
	if err == nil || !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("30000 names must be refused by the budget, got %v", err)
	}
	if n := ctx.calls.Load(); n != 1 {
		t.Errorf("budget refusal checked the context %d times, want 1 (before any per-name work)", n)
	}
}

// TestXIN2441AliasDetectionAtBudget keeps the case-fold rules at full size: a
// single colliding pair among the maximum number of names is still found,
// whether it collides with a declared name or with another argument, including
// through Unicode special folds.
func TestXIN2441AliasDetectionAtBudget(t *testing.T) {
	body := map[string]registry.SchemaInfo{"space_id": {}, "kind": {}}
	cases := []struct {
		name string
		add  map[string]any
		want string
	}{
		{"declared long-s alias", map[string]any{"ſpace_id": "x"}, `is not the declared spelling of "space_id"`},
		{"declared Kelvin alias", map[string]any{"KIND": "x"}, `is not the declared spelling of "kind"`},
		{"two undeclared names", map[string]any{"zz_Key": 1, "ZZ_key": 2}, "differ only by case folding"},
		{"undeclared vs filler", map[string]any{"EXTRA_000007": 1}, "differ only by case folding"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := manyArgs(maxArgumentNames-len(c.add), func(i int) string { return fmt.Sprintf("extra_%06d", i) })
			for k, v := range c.add {
				args[k] = v
			}
			err := checkArgumentNames(context.Background(), args, map[string]bool{}, nil, nil, body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
	// Legitimate declared spellings, and distinct names, are untouched.
	args := manyArgs(maxArgumentNames-2, extraName)
	args["space_id"], args["kind"] = "s", "k"
	if err := checkArgumentNames(context.Background(), args, map[string]bool{}, nil, nil, body); err != nil {
		t.Fatalf("canonical names must pass: %v", err)
	}
}

func TestXIN2441ArgumentValidationHonorsCancellation(t *testing.T) {
	body := map[string]registry.SchemaInfo{"space_id": {}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := checkArgumentNames(ctx, map[string]any{"space_id": "s"}, map[string]bool{}, nil, nil, body)
	var cancelled errArgumentsCancelled
	if !asCancelled(err, &cancelled) {
		t.Fatalf("an already-cancelled context must stop validation, got %v", err)
	}

	// Cancellation part-way: the context is live for the entry check and the
	// first periodic check only, so validation must stop at the second
	// interval rather than run to completion.
	mid := &countingCtx{Context: context.Background(), live: 2}
	err = checkArgumentNames(mid, manyArgs(maxArgumentNames, extraName), map[string]bool{}, nil, nil, body)
	if !asCancelled(err, &cancelled) {
		t.Fatalf("cancellation during validation must stop it, got %v", err)
	}
	if n, want := mid.calls.Load(), int64(3); n != want {
		t.Errorf("validation checked the context %d times, want %d (stopped at the first check after cancellation)", n, want)
	}
}

func asCancelled(err error, target *errArgumentsCancelled) bool {
	c, ok := err.(errArgumentsCancelled) //nolint:errorlint // checkArgumentNames returns it unwrapped
	if ok {
		*target = c
	}
	return ok
}

// TestXIN2441ToolCallsBoundedBeforeAuth drives both tool facades over the
// dispatcher with an HTTP-style connection that carries no credential: an
// oversized argument set and a cancelled request are refused locally, as
// validation rather than an auth failure or a sent write, and nothing reaches
// the backend.
func TestXIN2441ToolCallsBoundedBeforeAuth(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	defer srv.Close()
	s := facadeServer(t, facadeBoth)
	s.factoryFn = fakeBackendFactory(srv.URL, "")

	big := manyArgs(30000, extraName)
	big["channel_id"], big["channel_type"], big["payload"] = "c1", 1, map[string]any{"type": 1, "content": "hi"}
	for _, tool := range []string{"execute", "call_op"} {
		start := time.Now()
		res := callTool(t, s, tool, map[string]any{"operation_id": "message.send", "arguments": big})
		out := toolText(t, res)
		text := res.Content[0].Text
		if !res.IsError || !strings.Contains(text, "too many arguments") || !strings.Contains(text, "VALIDATION_ERROR") {
			t.Errorf("%s with 30000 names must be a budget refusal, got %s", tool, truncateRunes(text, 400))
		}
		if tool == "execute" && out["status"] != "validation_error" {
			t.Errorf("execute status = %v, want validation_error", out["status"])
		}
		t.Logf("%s refused 30000 names in %s", tool, time.Since(start))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tool := range []string{"execute", "call_op"} {
		res := dispatchToolCtx(t, ctx, s, tool, map[string]any{"operation_id": "message.send",
			"arguments": map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"type": 1, "content": "hi"}}})
		text := res.Content[0].Text
		if !res.IsError || !strings.Contains(text, "REQUEST_CANCELLED") {
			t.Errorf("%s on a cancelled request must be REQUEST_CANCELLED, got %s", tool, text)
		}
		if tool == "execute" {
			if st := toolText(t, res)["status"]; st != "validation_error" {
				t.Errorf("a cancelled, unsent write must not be %v", st)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("bounded refusals reached the backend %d times", hits)
	}
}

// dispatchToolCtx is callTool with a caller-supplied request context.
func dispatchToolCtx(t *testing.T, ctx context.Context, s *Server, name string, args any) toolResult {
	t.Helper()
	argsRaw, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(argsRaw)})
	resp, has := s.Dispatch(ctx, rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if !has || resp.Error != nil {
		t.Fatalf("tools/call failed: has=%v err=%v", has, resp.Error)
	}
	var res toolResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res
}
