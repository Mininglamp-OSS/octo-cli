package mcp_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBuiltBinary_XIN2544Committed429(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, noRetry := range []bool{false, true} {
		for _, transport := range []string{"cli", "stdio", "http"} {
			for _, tool := range []string{"execute", "call_op"} {
				if transport == "cli" && tool != "execute" {
					continue
				}
				t.Run(transport+"/"+tool+map[bool]string{false: "/default", true: "/no-retry"}[noRetry], func(t *testing.T) {
					var committed atomic.Int32
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, _ := io.ReadAll(r.Body)
						if r.Method != http.MethodPost || !strings.Contains(string(body), `"channel_id":"c1"`) {
							t.Errorf("unexpected mutation: %s %s", r.Method, body)
						}
						committed.Add(1)
						w.Header().Set("Retry-After", "7")
						w.WriteHeader(429)
						_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"mock committed before rate limit"}}`))
					}))
					defer backend.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					args := []string{"message", "send", "--data", xin2453Message}
					addr := freeAddr(t)
					if transport != "cli" {
						args = []string{"mcp", "serve", "--facade", "both"}
						if transport == "http" {
							args = append(args, "--http", addr)
						}
					}
					if noRetry {
						args = append(args, "--no-retry")
					}
					cmd := exec.CommandContext(ctx, bin, args...)
					cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
					call := xin2453Request(tool, `{"operation_id":"message.send","arguments":`+xin2453Message+`}`)
					var raw string
					switch transport {
					case "cli":
						out, err := cmd.CombinedOutput()
						if err == nil {
							t.Errorf("failed mutation must exit nonzero: %s", out)
						}
						raw = string(out)
					case "stdio":
						cmd.Stdin = strings.NewReader(call + "\n")
						out, err := cmd.Output()
						if err != nil {
							t.Fatal(err)
						}
						raw = xin2453Text(t, string(bytes.TrimSpace(out)))
					case "http":
						if err := cmd.Start(); err != nil {
							t.Fatal(err)
						}
						defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
						waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
						req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(call))
						req.Header.Set("Authorization", "Bearer bf_fixture")
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						out, _ := io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						raw = xin2453Text(t, string(out))
					}
					if committed.Load() != 1 {
						t.Errorf("commits=%d, want one", committed.Load())
					}
					for _, want := range []string{`"code": "RESULT_UNKNOWN"`, "mock committed before rate limit", "RATE_LIMITED", "Retry-After", "7s", "do not retry automatically"} {
						if !strings.Contains(raw, want) {
							t.Errorf("missing %s: %.1600s", want, raw)
						}
					}
					if transport != "cli" && tool == "execute" {
						for _, want := range []string{`"status": "result_unknown"`, `"retryable": false`, "Do NOT auto-retry"} {
							if !strings.Contains(raw, want) {
								t.Errorf("missing %s: %.1600s", want, raw)
							}
						}
					}
				})
			}
		}
	}
}
