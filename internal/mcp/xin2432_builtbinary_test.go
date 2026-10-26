package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBuiltBinary_XIN2432HiddenLoopOperationsRefused drives the real binary
// over stdio and HTTP, and under a task credential. The ten hidden Loop
// management operations are neither advertised nor describable nor executable,
// and never reach the mock backend. The same run exercises the corrected
// docs exports: docs.ppt.export inline, docs.scene.export as portable
// Excalidraw returned inline, and an Excalidraw dry-run that makes no request.
func TestBuiltBinary_XIN2432HiddenLoopOperationsRefused(t *testing.T) {
	bin := buildOctoCLI(t)
	hidden := []string{
		"workspace.create", "workspace.update", "workspace.delete",
		"runtime.update", "runtime.delete", "runtime.archive_and_delete",
		"runtime.update_request.create", "runtime.model_request.create",
		"runtime.local_skill_request.create", "runtime.local_skill_import.create",
	}
	type step struct {
		request string
		want    []string
		reject  []string
	}
	var steps []step
	id := 1
	next := func() int { id++; return id }
	steps = append(steps,
		step{toolCall(next(), "get_skill", map[string]any{"intent": "search", "domain": "loop", "query": "runtime"}), nil, quoted(hidden)},
		step{toolCall(next(), "get_skill", map[string]any{"intent": "search", "domain": "loop", "query": "workspace"}), nil, quoted(hidden)},
	)
	for _, op := range hidden {
		steps = append(steps,
			step{toolCall(next(), "get_skill", map[string]any{"intent": "describe", "operation_id": op}), []string{op}, []string{`"schema_fingerprint"`}},
			step{toolCall(next(), "execute", map[string]any{"operation_id": op, "arguments": map[string]any{"runtime_id": "r1", "workspace_id": "w1"}}), nil, []string{`"status": "ok"`}},
		)
	}
	steps = append(steps,
		step{toolCall(next(), "execute", map[string]any{"operation_id": "docs.ppt.export", "arguments": map[string]any{"docId": "deck"}}),
			[]string{`"status": "ok"`, `"inline": true`, `\u003ctitle\u003edeck`}, nil},
		step{toolCall(next(), "execute", map[string]any{"operation_id": "docs.scene.export", "arguments": map[string]any{"docId": "board", "format": "excalidraw"}}),
			[]string{`"status": "ok"`, `"inline": true`, `\"type\": \"excalidraw\"`, `"baseVersion": "BV"`}, nil},
		step{toolCall(next(), "execute", map[string]any{"operation_id": "docs.scene.export", "dry_run": true, "arguments": map[string]any{"docId": "board", "format": "excalidraw"}}),
			[]string{`"dry_run": true`, `"image_format": "excalidraw"`}, nil},
	)

	t.Run("task-credential", func(t *testing.T) {
		var mu sync.Mutex
		hits := 0
		backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			mu.Lock()
			hits++
			mu.Unlock()
		}))
		defer backend.Close()
		env := append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL, "OCTO_CREDENTIAL_MODE=task", "OCTO_TASK_TOKEN=mat_task_smoke")
		run := func(args ...string) (string, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Env = env
			cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
			out, err := cmd.CombinedOutput()
			return string(out), err
		}
		// The MCP server itself is not available to a task credential.
		if out, err := run("mcp", "serve", "--facade", "two"); err == nil || !strings.Contains(out, "only allows Loop commands") {
			t.Errorf("mcp serve under a task credential must refuse to start: err=%v out=%s", err, out)
		}
		// Nor is any hidden management command on the Loop surface it does allow.
		// Flags are left out on purpose so the refusal under test is the
		// missing command, not an unrecognised flag.
		for _, argv := range [][]string{
			{"loop", "workspace", "create"},
			{"loop", "workspace", "update", "w1"},
			{"loop", "workspace", "delete", "w1"},
			{"loop", "runtime", "update", "r1"},
			{"loop", "runtime", "delete", "r1"},
			{"loop", "runtime", "archive-and-delete", "r1"},
			{"loop", "runtime", "update-request", "create", "r1"},
			{"loop", "runtime", "model-request", "create", "r1"},
			{"loop", "runtime", "local-skill-request", "create", "r1"},
			{"loop", "runtime", "local-skill-import", "create", "r1"},
		} {
			out, err := run(argv...)
			if err == nil || (!strings.Contains(out, "not a known command") && !strings.Contains(out, "unknown subcommand")) {
				t.Errorf("%s must be an unknown command under a task credential: err=%v out=%s", strings.Join(argv, " "), err, out)
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if hits != 0 {
			t.Errorf("task-credential refusals reached the backend %d times", hits)
		}
	})

	for _, mode := range []string{"stdio", "http"} {
		t.Run(mode, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Method+" "+r.URL.Path)
				mu.Unlock()
				switch r.URL.Path {
				case "/v1/bot/docs/deck/ppt/export":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = w.Write([]byte("<!doctype html><title>deck</title>"))
				case "/v1/bot/docs/board/scene":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"elements":[{"id":"a","type":"rectangle"}],"files":{},"appState":{},"baseVersion":"BV"}`))
				default:
					w.WriteHeader(http.StatusTeapot)
				}
			}))
			defer backend.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			args := []string{"mcp", "serve", "--facade", "two"}
			addr := freeAddr(t)
			if mode == "http" {
				args = append(args, "--http", addr)
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			env := append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
			cmd.Env = env

			var lines [][]byte
			if mode == "stdio" {
				var in strings.Builder
				for _, s := range steps {
					in.WriteString(s.request + "\n")
				}
				cmd.Stdin = strings.NewReader(in.String())
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("stdio server: %v", err)
				}
				lines = bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			} else {
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
				waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
				for _, s := range steps {
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(s.request))
					req.Header.Set("Authorization", "Bearer bf_smoke")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					lines = append(lines, bytes.TrimSpace(body))
				}
			}
			if len(lines) != len(steps) {
				t.Fatalf("got %d responses, want %d", len(lines), len(steps))
			}
			for i, line := range lines {
				var resp struct {
					Error  any `json:"error"`
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(line, &resp); err != nil || resp.Error != nil || len(resp.Result.Content) != 1 {
					t.Fatalf("step %d: invalid response: %s", i+1, line)
				}
				text := resp.Result.Content[0].Text
				for _, w := range steps[i].want {
					if !strings.Contains(text, w) {
						t.Errorf("step %d missing %q: %s", i+1, w, text)
					}
				}
				for _, r := range steps[i].reject {
					if strings.Contains(text, r) {
						t.Errorf("step %d must not contain %q: %s", i+1, r, text)
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			want := "GET /v1/bot/docs/deck/ppt/export\nGET /v1/bot/docs/board/scene"
			if strings.Join(seen, "\n") != want {
				t.Errorf("backend requests =\n%s\nwant exactly\n%s", strings.Join(seen, "\n"), want)
			}
		})
	}
}

func quoted(ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = `"` + id + `"`
	}
	return out
}
