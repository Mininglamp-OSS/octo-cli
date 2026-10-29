package mcp_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// XIN-2463 follow-up against the real binary, with an isolated counting mock:
//   - a replay-safe keyed write (capability.install) sent on a reused
//     keep-alive connection whose response is dropped, after which the backend
//     stops listening, is RESULT_UNKNOWN on execute and call_op: the retry loop
//     owns every attempt, so a hidden net/http replay cannot turn a delivered
//     write into a never-sent NETWORK_ERROR;
//   - a write whose first attempt answered 503 and whose replay is refused with
//     400/401/404/409 is result_unknown from execute (with the recovery note and
//     retryable:false), and RESULT_UNKNOWN from call_op and the CLI; the same
//     refusal on a first attempt keeps its ordinary status, and an eventual
//     success is a success.

const xin2464Install = `{"X-Workspace-ID":"11111111-1111-4111-8111-111111111111","Idempotency-Key":"cap-reused","definition":{"schema_version":"1.0","name":"c","skills":[{"name":"s","content":"x"}]}}`

type xin2464Backend struct {
	srv     *httptest.Server
	mu      sync.Mutex
	writes  int
	keys    []string
	bodies  []string
	actions []string
}

// newXIN2464Backend answers GET with an empty object and the n-th write with
// actions[n] (the last action repeats): "drop-close" stops listening and drops
// the connection without an answer, a status code is answered with a matching
// error, and "ok" succeeds.
func newXIN2464Backend(t *testing.T, actions ...string) *xin2464Backend {
	t.Helper()
	b := &xin2464Backend{actions: actions}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		b.mu.Lock()
		action := b.actions[min(b.writes, len(b.actions)-1)]
		b.writes++
		b.keys = append(b.keys, r.Header.Get("Idempotency-Key"))
		b.bodies = append(b.bodies, string(body))
		b.mu.Unlock()
		codes := map[string]string{"400": "VALIDATION_ERROR", "401": "UNAUTHORIZED", "404": "NOT_FOUND", "409": "CONFLICT", "503": "UNAVAILABLE"}
		switch action {
		case "drop-close":
			_ = b.srv.Listener.Close()
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		case "ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			status := map[string]int{"400": 400, "401": 401, "404": 404, "409": 409, "503": 503}[action]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + codes[action] + `","message":"mock ` + action + `"}}`))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *xin2464Backend) count() (writes int, keys, bodies []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writes, append([]string(nil), b.keys...), append([]string(nil), b.bodies...)
}

// xin2464Serve runs several requests through ONE `mcp serve` process, so they
// share its HTTP client and keep-alive connections, and returns the last
// response line.
func xin2464Serve(t *testing.T, bin, transport, facade, backendURL string, requests ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	addr := freeAddr(t)
	args := []string{"mcp", "serve", "--facade", facade}
	if transport == "http" {
		args = append(args, "--http", addr)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backendURL)
	if transport == "stdio" {
		// One line at a time, so the second call starts after the first ends.
		pw, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		reader := bufio.NewReader(stdout)
		var last string
		for _, req := range requests {
			_, _ = pw.Write([]byte(req + "\n"))
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("stdio server: %v", err)
			}
			last = strings.TrimSpace(line)
		}
		_ = pw.Close()
		_ = cmd.Wait()
		return last
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
	var last string
	for _, request := range requests {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(request))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer bf_smoke")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		line, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		last = string(line)
	}
	return last
}

func TestBuiltBinary_XIN2464ReusedConnectionAndFinalRefusal(t *testing.T) {
	bin := buildOctoCLI(t)
	tools := []struct{ facade, tool, unknown string }{
		{"two", "execute", `"status": "result_unknown"`},
		{"three", "call_op", `"code": "RESULT_UNKNOWN"`},
	}

	for _, transport := range []string{"stdio", "http"} {
		for _, c := range tools {
			t.Run(transport+"/reused connection/"+c.tool, func(t *testing.T) {
				backend := newXIN2464Backend(t, "drop-close")
				warm := xin2453Request(c.tool, `{"operation_id":"docs.get","arguments":{"docId":"d1"}}`)
				install := xin2453Request(c.tool, `{"operation_id":"capability.install","arguments":`+xin2464Install+`}`)
				text := xin2453Text(t, xin2464Serve(t, bin, transport, c.facade, backend.srv.URL, warm, install))
				if !strings.Contains(text, c.unknown) || strings.Contains(text, `"retryable": true`) {
					t.Errorf("want %s for a delivered install: %.700s", c.unknown, text)
				}
				if c.tool == "execute" && (!strings.Contains(text, `"retryable": false`) || strings.Contains(text, "execution_error")) {
					t.Errorf("want result_unknown with retryable:false: %.700s", text)
				}
				n, keys, bodies := backend.count()
				if n != 1 || keys[0] != "cap-reused" || !strings.Contains(bodies[0], `"skills"`) {
					t.Errorf("install deliveries = %d keys=%v bodies=%v, want one keyed install with its body", n, keys, bodies)
				}
			})
		}
	}

	// Final refusal after an ambiguous first attempt, and the controls.
	type outcome struct{ execute, callOp, cli string }
	unknown := outcome{`"status": "result_unknown"`, `"code": "RESULT_UNKNOWN"`, `"code": "RESULT_UNKNOWN"`}
	cases := []struct {
		name    string
		actions []string
		writes  int
		want    outcome
	}{
		{"503 then 400", []string{"503", "400"}, 2, unknown},
		{"503 then 401", []string{"503", "401"}, 2, unknown},
		{"503 then 404", []string{"503", "404"}, 2, unknown},
		{"503 then 409", []string{"503", "409"}, 2, unknown},
		{"first 400", []string{"400"}, 1, outcome{`"status": "validation_error"`, `"code": "VALIDATION_ERROR"`, `"code": "VALIDATION_ERROR"`}},
		{"first 401", []string{"401"}, 1, outcome{`"status": "auth_error"`, `"code": "UNAUTHORIZED"`, `"code": "UNAUTHORIZED"`}},
		{"first 404", []string{"404"}, 1, outcome{`"status": "execution_error"`, `"code": "NOT_FOUND"`, `"code": "NOT_FOUND"`}},
		{"first 409", []string{"409"}, 1, outcome{`"status": "validation_error"`, `"code": "CONFLICT"`, `"code": "CONFLICT"`}},
		{"503 then success", []string{"503", "ok"}, 2, outcome{`"status": "ok"`, `"ok": true`, `"ok": true`}},
	}
	for _, c := range cases {
		isUnknown := c.want == unknown
		for _, transport := range []string{"stdio", "http"} {
			for _, tl := range tools {
				t.Run(transport+"/final refusal/"+tl.tool+"/"+c.name, func(t *testing.T) {
					backend := newXIN2464Backend(t, c.actions...)
					call := xin2453Request(tl.tool, `{"operation_id":"docs.delete","arguments":{"docId":"d1"}}`)
					text := xin2453Text(t, xin2464Serve(t, bin, transport, tl.facade, backend.srv.URL, call))
					want := c.want.callOp
					if tl.tool == "execute" {
						want = c.want.execute
					}
					if !strings.Contains(text, want) {
						t.Errorf("missing %s: %.700s", want, text)
					}
					if tl.tool == "execute" && isUnknown {
						for _, w := range []string{`"code": "RESULT_UNKNOWN"`, `"retryable": false`, "Do NOT auto-retry"} {
							if !strings.Contains(text, w) {
								t.Errorf("missing %s: %.700s", w, text)
							}
						}
					}
					if !isUnknown && strings.Contains(text, "RESULT_UNKNOWN") {
						t.Errorf("a definite outcome must not be unknown: %.700s", text)
					}
					if n, _, _ := backend.count(); n != c.writes {
						t.Errorf("DELETE deliveries = %d, want %d", n, c.writes)
					}
				})
			}
		}
		t.Run("cli/final refusal/"+c.name, func(t *testing.T) {
			backend := newXIN2464Backend(t, c.actions...)
			cmd := exec.Command(bin, "docs", "delete", "d1")
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.srv.URL)
			out, _ := cmd.CombinedOutput()
			if !strings.Contains(string(out), c.want.cli) {
				t.Errorf("missing %s: %s", c.want.cli, out)
			}
			if !isUnknown && strings.Contains(string(out), "RESULT_UNKNOWN") {
				t.Errorf("a definite outcome must not be unknown: %s", out)
			}
			if n, _, _ := backend.count(); n != c.writes {
				t.Errorf("DELETE deliveries = %d, want %d", n, c.writes)
			}
		})
	}
}
