package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// XIN-2441 against the real binary, with an isolated mock backend that must
// see no request:
//   - an argument set over the name budget is refused before authentication
//     on an HTTP connection with no Authorization header, and over stdio, on
//     both the execute and call_op facades;
//   - an MCP execute dry run of capability.install whose MCP server secrets
//     use case-fold spellings returns no secret value;
//   - CLI --dry-run and --verbose print none of them either.

const xin2441Canary = "XIN2441_UNIQUE_CANARY"

func xin2441CapabilityArgs() map[string]any {
	return map[string]any{
		"X-Workspace-ID":  "22222222-2222-4222-8222-222222222222",
		"Idempotency-Key": "k1",
		"definition": map[string]any{"schema_version": "1.0", "name": "pack", "experts": []any{map[string]any{
			"name": "e1",
			"mcp_config": map[string]any{"mcpServers": map[string]any{
				"remote":  map[string]any{"url": "https://example.invalid/mcp", "HEADERS": map[string]any{"Authorization": "Bearer " + xin2441Canary + "_HEADERS"}},
				"remote2": map[string]any{"URL": "https://example.invalid/" + xin2441Canary + "_URL", "headerſ": map[string]any{"A": xin2441Canary + "_UNICODE"}},
				"local":   map[string]any{"command": "npx", "Env": map[string]any{"TOKEN": xin2441Canary + "_ENV"}},
			}},
		}}},
		"bindings": map[string]any{"experts": []any{map[string]any{"expert_name": "e1", "runtime_id": "11111111-1111-4111-8111-111111111111"}}},
	}
}

func xin2441OversizedArgs() map[string]any {
	args := map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"type": 1, "content": "hi"}}
	for i := 0; i < 30000; i++ {
		args[fmt.Sprintf("extra_%06d", i)] = 0
	}
	return args
}

func xin2441Call(id int, tool string, args map[string]any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	return string(b)
}

func TestBuiltBinary_XIN2441BoundedValidationAndSecretAliases(t *testing.T) {
	bin := buildOctoCLI(t)
	var mu sync.Mutex
	var hits []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusTeapot)
	}))
	defer backend.Close()

	type step struct {
		facade, request string
		want, reject    []string
		auth            bool
	}
	oversized := xin2441OversizedArgs()
	steps := []step{
		{"two", xin2441Call(1, "execute", map[string]any{"operation_id": "message.send", "arguments": oversized}),
			[]string{`"status": "validation_error"`, "too many arguments: 30003 names exceed the limit of 1024"}, []string{"auth_error", "result_unknown"}, false},
		{"three", xin2441Call(2, "call_op", map[string]any{"operation_id": "message.send", "arguments": oversized}),
			[]string{"VALIDATION_ERROR", "too many arguments"}, []string{"UNAUTHORIZED"}, false},
		{"two", xin2441Call(3, "execute", map[string]any{"operation_id": "capability.install", "dry_run": true, "arguments": xin2441CapabilityArgs()}),
			[]string{`"status": "ok"`, `"dry_run": true`, "REDACTED", "npx"}, []string{xin2441Canary}, true},
	}

	for _, mode := range []string{"stdio", "http"} {
		for _, s := range steps {
			t.Run(mode+"/"+s.facade+"/"+s.request[:40], func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				defer cancel()
				addr := freeAddr(t)
				args := []string{"mcp", "serve", "--facade", s.facade}
				if mode == "http" {
					args = append(args, "--http", addr)
				}
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL, "OCTO_MCP_HTTP_REQUEST_TIMEOUT=100ms")
				var line []byte
				start := time.Now()
				if mode == "stdio" {
					cmd.Stdin = strings.NewReader(s.request + "\n")
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("stdio server: %v", err)
					}
					line = bytes.TrimSpace(out)
				} else {
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
					waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
					start = time.Now()
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(s.request))
					req.Header.Set("Content-Type", "application/json")
					if s.auth {
						req.Header.Set("Authorization", "Bearer bf_smoke")
					}
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					line, _ = io.ReadAll(resp.Body)
					_ = resp.Body.Close()
				}
				t.Logf("%s %s answered in %s (request %d bytes)", mode, s.facade, time.Since(start), len(s.request))
				var resp struct {
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(line, &resp); err != nil || len(resp.Result.Content) != 1 {
					t.Fatalf("invalid response: %.400s", line)
				}
				text := resp.Result.Content[0].Text
				for _, w := range s.want {
					if !strings.Contains(text, w) {
						t.Errorf("missing %q: %.600s", w, text)
					}
				}
				for _, r := range s.reject {
					if strings.Contains(string(line), r) {
						t.Errorf("must not contain %q: %.600s", r, text)
					}
				}
			})
		}
	}

	t.Run("cli", func(t *testing.T) {
		data, _ := json.Marshal(map[string]any{"definition": xin2441CapabilityArgs()["definition"], "bindings": xin2441CapabilityArgs()["bindings"]})
		for _, flag := range []string{"--dry-run", "--verbose"} {
			cmd := exec.Command(bin, "loop", "capability", "install", "--workspace-id", "22222222-2222-4222-8222-222222222222",
				"--idempotency-key", "k1", "--data", string(data), flag)
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
			out, _ := cmd.CombinedOutput()
			if strings.Contains(string(out), xin2441Canary) {
				t.Errorf("%s leaked a case-fold secret: %s", flag, out)
			}
			if flag == "--dry-run" && !strings.Contains(string(out), "REDACTED") {
				t.Errorf("--dry-run should show redactions: %s", out)
			}
			if flag == "--verbose" && !strings.Contains(string(out), "capabilities/install") {
				t.Errorf("--verbose should trace the request: %s", out)
			}
		}
	})

	mu.Lock()
	defer mu.Unlock()
	// Only the CLI --verbose run is a real call; every MCP step is refused or
	// a dry run and must not reach the backend.
	if len(hits) != 1 || !strings.HasSuffix(hits[0], "/capabilities/install") {
		t.Errorf("backend requests = %v, want exactly the one CLI --verbose install", hits)
	}
}
