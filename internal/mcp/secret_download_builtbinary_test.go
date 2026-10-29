package mcp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func secretRedis(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("isolated Redis requires redis-server")
	}
	addr := freeAddr(t)
	cmd := exec.Command(path, "--bind", "127.0.0.1", "--port", strings.Split(addr, ":")[1], "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatal("start isolated Redis")
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	cli := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: -1})
	defer cli.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cli.Ping(t.Context()).Err() == nil {
			return "redis://" + addr + "/0"
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("isolated Redis unavailable")
	return ""
}

type secretLogs struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *secretLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *secretLogs) safe() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw := l.b.String()
	return !strings.Contains(raw, "fixture-private-value") && !strings.Contains(raw, "/mcp/secret-download/") && !strings.Contains(raw, "bf_smoke")
}

func secretHTTP(t *testing.T, bin, addr, facade string, env []string, extra ...string) {
	t.Helper()
	args := append([]string{"mcp", "serve", "--http", addr, "--facade", facade, "--verbose"}, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	logs := &secretLogs{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatal("start fixture MCP")
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if !logs.safe() {
			t.Error("secret or capability entered HTTP diagnostics")
		}
	})
	waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, 10*time.Second)
}

func secretRPC(t *testing.T, bin, transport, facade, addr string, env []string, rawArgs, bearer string, globals ...string) map[string]any {
	t.Helper()
	wire := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_secret_download_url","arguments":` + rawArgs + `}}`
	var out []byte
	if transport == "stdio" {
		args := append([]string{"mcp", "serve", "--facade", facade, "--verbose"}, globals...)
		cmd := exec.Command(bin, args...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(wire + "\n")
		var diagnostics bytes.Buffer
		cmd.Stderr = &diagnostics
		var err error
		out, err = cmd.Output()
		if err != nil {
			t.Fatal("stdio fixture failed")
		}
		if diagnostics.Len() != 0 {
			t.Fatal("unexpected stdio diagnostics")
		}
	} else {
		req, _ := http.NewRequestWithContext(t.Context(), "POST", "http://"+addr, strings.NewReader(wire))
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal("MCP fixture request failed")
		}
		defer resp.Body.Close()
		out, _ = io.ReadAll(resp.Body)
	}
	var rpc struct {
		Error  any `json:"error"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &rpc) != nil || rpc.Error != nil || len(rpc.Result.Content) != 1 {
		t.Fatal("dedicated secret tool unavailable or invalid response")
	}
	var payload map[string]any
	if json.Unmarshal([]byte(rpc.Result.Content[0].Text), &payload) != nil {
		t.Fatal("invalid secret result")
	}
	if strings.Contains(string(out), "fixture-private-value") {
		t.Fatal("raw secret entered MCP response")
	}
	return payload
}

func TestBuiltBinary_SecretDownloadCrossReplica(t *testing.T) {
	bin := buildOctoCLI(t)
	dsn := secretRedis(t)
	var hits atomic.Int32
	var rotated atomic.Bool
	canary := []byte("fixture-private-value\x00\nUTF8-密钥")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/bot/secrets/resolve" {
			t.Error("unexpected resolve route")
		}
		if r.Header.Get("Authorization") != "Bearer bf_smoke" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"value":"fixture-private-value"}`))
			return
		}
		var query struct {
			Query string `json:"query"`
		}
		_ = json.NewDecoder(r.Body).Decode(&query)
		switch query.Query {
		case "ambiguous":
			w.WriteHeader(422)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"details": map[string]any{"candidates": []any{map[string]any{"secret_id": "id1", "display_name": "name", "value": "fixture-private-value", "masked": "forbidden"}}}}})
			return
		case "missing", "other-owner":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"value":"fixture-private-value"}`))
			return
		}
		value := string(canary)
		if rotated.Load() {
			value = "rotated-private-value"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"secret_id": "id1", "value": value}})
	}))
	defer backend.Close()
	addrB := freeAddr(t)
	env := append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL, "OCTO_MCP_SECRET_REDIS_URL="+dsn, "OCTO_MCP_SECRET_PUBLIC_ORIGIN=http://"+addrB, "OCTO_MCP_SECRET_TTL=601s")
	secretHTTP(t, bin, addrB, "both", env)
	for _, facade := range []string{"three", "two", "both"} {
		for _, transport := range []string{"http", "stdio"} {
			t.Run(facade+"/"+transport, func(t *testing.T) {
				addrA := freeAddr(t)
				secretHTTP(t, bin, addrA, facade, env)
				before := hits.Load()
				preview := secretRPC(t, bin, transport, facade, addrA, env, `{"query":"alias","dry_run":true}`, "bf_smoke")
				if preview["status"] != "dry_run" || hits.Load() != before {
					t.Fatal("dry run performed resolution or returned wrong status")
				}
				for _, bad := range []string{`{"query":"x","dry_run":null}`, `{"query":"x","Dry_Run":true}`, `{"query":"x","dry_run":true,"dry_run":false}`, `{"query":"x","ttl":1}`, `{"query":null}`} {
					rejected := secretRPC(t, bin, transport, facade, addrA, env, bad, "bf_smoke")
					if rejected["status"] != "validation_error" || hits.Load() != before {
						t.Fatal("strict input sent a request")
					}
				}
				globalAddr := freeAddr(t)
				secretHTTP(t, bin, globalAddr, facade, env, "--dry-run")
				global := secretRPC(t, bin, transport, facade, globalAddr, env, `{"query":"alias"}`, "bf_smoke", "--dry-run")
				if global["status"] != "dry_run" || hits.Load() != before {
					t.Fatal("global dry run sent work")
				}
				result := secretRPC(t, bin, transport, facade, addrA, env, `{"query":"alias"}`, "bf_smoke")
				link, ok := result["download_url"].(string)
				if !ok || !strings.HasPrefix(link, "http://"+addrB+"/mcp/secret-download/") {
					t.Fatal("missing configured cross-replica URL")
				}
				if result["expires_in_seconds"] != float64(601) {
					t.Fatal("configured 601s not accepted")
				}
				rotated.Store(true)
				for i := 0; i < 2; i++ {
					resp, err := http.Get(link)
					if err != nil {
						t.Fatal("download failed")
					}
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if resp.StatusCode != 200 || !bytes.Equal(body, canary) {
						t.Fatal("private exact-byte/snapshot check failed")
					}
					if resp.Header.Get("Content-Type") != "application/octet-stream" || resp.Header.Get("Content-Disposition") != `attachment; filename="secret.bin"` || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("X-Content-Type-Options") != "nosniff" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
						t.Fatal("download headers missing")
					}
				}
				// Generic curl writes into an exclusive private client fixture.
				destination := filepath.Join(t.TempDir(), "secret.bin")
				file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					t.Fatal("private destination failed")
				}
				_ = file.Close()
				curl := exec.Command("curl", "--config", "-", "--silent", "--fail", "--max-time", "10", "--proto", "=http", "--output", destination)
				curl.Stdin = strings.NewReader("url = \"" + link + "\"\n")
				if out, err := curl.CombinedOutput(); err != nil || len(out) != 0 {
					t.Fatal("generic downloader failed or emitted diagnostics")
				}
				privateBytes, err := os.ReadFile(destination)
				info, statErr := os.Stat(destination)
				if err != nil || statErr != nil || info.Mode().Perm() != 0o600 || !bytes.Equal(privateBytes, canary) {
					t.Fatal("private client file boolean check failed")
				}
				updated := secretRPC(t, bin, transport, facade, addrA, env, `{"query":"id1"}`, "bf_smoke")
				freshLink, ok := updated["download_url"].(string)
				if !ok {
					t.Fatal("ID retry failed")
				}
				freshResp, err := http.Get(freshLink)
				if err != nil {
					t.Fatal("rotated download failed")
				}
				fresh, _ := io.ReadAll(freshResp.Body)
				_ = freshResp.Body.Close()
				if freshResp.StatusCode != 200 || !bytes.Equal(fresh, []byte("rotated-private-value")) {
					t.Fatal("new issuance missed rotation")
				}
				rotated.Store(false)
				for _, q := range []string{"missing", "other-owner", "ambiguous"} {
					r := secretRPC(t, bin, transport, facade, addrA, env, `{"query":"`+q+`"}`, "bf_smoke")
					want := "not_found"
					if q == "ambiguous" {
						want = "ambiguous"
					}
					if r["status"] != want {
						t.Fatal("wrong safe resolution outcome")
					}
					raw, _ := json.Marshal(r)
					if strings.Contains(string(raw), "forbidden") || strings.Contains(string(raw), "download_url") {
						t.Fatal("unsafe error metadata")
					}
				}
				if transport == "http" {
					for _, bearer := range []string{"", "uk_unsupported", "bf_bad"} {
						r := secretRPC(t, bin, transport, facade, addrA, env, `{"query":"alias"}`, bearer)
						if r["status"] != "auth_error" {
							t.Fatal("credential isolation failed")
						}
					}
				}
			})
		}
	}

	// Explicit stored-profile selection must override an unrelated ambient token
	// and API origin; all storage is in this fixture config directory.
	profileEnv := append(append([]string{}, env...), "OCTO_TOKEN=bf_bad_ambient", "OCTO_API_BASE_URL=http://127.0.0.1:1")
	login := exec.Command(bin, "auth", "login", "--profile", "fixture", "--bot-id", "fixture-bot", "--with-token", "--api-base-url", backend.URL)
	login.Env = profileEnv
	login.Stdin = strings.NewReader("bf_smoke\n")
	if _, err := login.Output(); err != nil {
		t.Fatal("fixture profile setup failed")
	}
	for _, selector := range [][]string{{"--profile", "fixture"}, {"--bot-id", "fixture-bot"}} {
		result := secretRPC(t, bin, "stdio", "both", "", profileEnv, `{"query":"id1"}`, "", selector...)
		if result["status"] != "ok" {
			t.Fatal("stdio credential selector was lost")
		}
	}
	taskCmd := exec.Command(bin, "mcp", "serve")
	taskCmd.Env = append(append([]string{}, env...), "OCTO_CREDENTIAL_MODE=task", "OCTO_TASK_TOKEN=fixture-task-token")
	if out, err := taskCmd.CombinedOutput(); err == nil || bytes.Contains(out, []byte("fixture-task-token")) {
		t.Fatal("task-mode MCP restriction failed")
	}

}
