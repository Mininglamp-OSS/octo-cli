package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildOctoCLI compiles the octo-cli binary once into a temp path. It runs the
// facade end-to-end through the real built binary (not just in-process handlers)
// as the reviewer required for acceptance coverage.
func buildOctoCLI(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping built-binary smoke in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "octo-cli")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/Mininglamp-OSS/octo-cli/cmd/octo-cli")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build octo-cli: %v\n%s", err, out)
	}
	return bin
}

// isolatedEnv returns an environment with an empty config dir and an env bot
// token, so no host profile is used and stdio credential resolution is
// deterministic.
func isolatedEnv(t *testing.T) []string {
	t.Helper()
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "OCTO_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "OCTO_CONFIG_DIR="+t.TempDir(), "OCTO_BOT_TOKEN=bf_smoke")
}

func toolNamesFromToolsList(t *testing.T, raw []byte) []string {
	t.Helper()
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode tools/list: %v\n%s", err, raw)
	}
	names := make([]string, len(resp.Result.Tools))
	for i, tl := range resp.Result.Tools {
		names[i] = tl.Name
	}
	return names
}

var facadeWant = map[string][]string{
	"three": {"search_ops", "describe_op", "call_op"},
	"two":   {"get_skill", "execute"},
	"both":  {"search_ops", "describe_op", "call_op", "get_skill", "execute"},
}

// TestBuiltBinary_StdioFacadeToolsList drives the built binary over stdio for
// each facade and asserts tools/list exposes exactly that facade's tools.
func TestBuiltBinary_StdioFacadeToolsList(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, facade := range []string{"three", "two", "both"} {
		t.Run(facade, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "mcp", "serve", "--facade", facade)
			cmd.Env = isolatedEnv(t)
			cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Run(); err != nil {
				t.Fatalf("run stdio server: %v\n%s", err, out.String())
			}
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				if strings.Contains(line, `"id":2`) {
					got = toolNamesFromToolsList(t, []byte(line))
				}
			}
			if strings.Join(got, ",") != strings.Join(facadeWant[facade], ",") {
				t.Errorf("stdio facade %q tools = %v, want %v", facade, got, facadeWant[facade])
			}
		})
	}
}

// TestBuiltBinary_HTTPFacadeToolsList drives the built binary over HTTP
// (JSON-RPC over POST) for each facade and asserts the tool surface, exercising
// the real transport, origin gate, and per-connection server.
func TestBuiltBinary_HTTPFacadeToolsList(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, facade := range []string{"three", "two", "both"} {
		t.Run(facade, func(t *testing.T) {
			addr := freeAddr(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := exec.CommandContext(ctx, bin, "mcp", "serve", "--http", addr, "--facade", facade)
			cmd.Env = isolatedEnv(t)
			var errBuf bytes.Buffer
			cmd.Stderr = &errBuf
			if err := cmd.Start(); err != nil {
				t.Fatalf("start http server: %v", err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

			url := "http://" + addr + "/"
			body := waitPostJSON(t, url, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, 10*time.Second)
			got := toolNamesFromToolsList(t, body)
			if strings.Join(got, ",") != strings.Join(facadeWant[facade], ",") {
				t.Errorf("http facade %q tools = %v, want %v (stderr: %s)", facade, got, facadeWant[facade], errBuf.String())
			}
		})
	}
}

// TestBuiltBinary_InvalidFacadeRejected proves the built binary rejects an
// invalid --facade with a nonzero exit and a validation message.
func TestBuiltBinary_InvalidFacadeRejected(t *testing.T) {
	bin := buildOctoCLI(t)
	cmd := exec.Command(bin, "mcp", "serve", "--facade", "nope")
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("invalid --facade must exit nonzero; output=%s", out)
	}
	if !strings.Contains(string(out), "invalid --facade") {
		t.Errorf("expected an invalid-facade message, got %s", out)
	}
}

func TestBuiltBinary_FacadeExecutionSmoke(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, facade := range []string{"three", "two", "both"} {
		for _, transport := range []string{"stdio", "http"} {
			t.Run(facade+"/"+transport, func(t *testing.T) {
				tool, describe := "execute", "get_skill"
				if facade == "three" {
					tool, describe = "call_op", "describe_op"
				}
				requests := []string{
					`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + describe + `","arguments":{"intent":"describe","operation_id":"message.send"}}}`,
					`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + tool + `","arguments":{"operation_id":"message.send","arguments":{"channel_id":"model-channel","channel_type":1,"payload":{"text":"preview"}}}}}`,
				}
				if facade != "three" {
					requests = append(requests,
						`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"file.upload","arguments":{"file_path":"missing.bin"}}}}`,
						`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"message.send","schema_fingerprint":"stale"}}}`,
						`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"html.asset.add","arguments":{"file_path":"missing.bin"}}}}`)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				args := []string{"mcp", "serve", "--facade", facade, "--dry-run", "--force-channel-id", "forced-channel"}
				addr := freeAddr(t)
				if transport == "http" {
					args = append(args, "--http", addr)
				}
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Env = append(isolatedEnv(t), "OCTO_MCP_UPLOAD_ROOT="+t.TempDir()+"/absent/root")
				var raw []byte
				if transport == "stdio" {
					cmd.Stdin = strings.NewReader(strings.Join(requests, "\n") + "\n")
					var err error
					raw, err = cmd.Output()
					if err != nil {
						t.Fatal(err)
					}
				} else {
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
					waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
					for _, request := range requests {
						req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr, strings.NewReader(request))
						req.Header.Set("Authorization", "Bearer bf_smoke")
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						if err != nil {
							t.Fatal(err)
						}
						raw = append(raw, bytes.TrimSpace(body)...)
						raw = append(raw, '\n')
					}
				}
				lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
				if len(lines) != len(requests) {
					t.Fatalf("got %d responses, want %d", len(lines), len(requests))
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
						t.Fatalf("invalid smoke response: %s", line)
					}
					text := resp.Result.Content[0].Text
					want := []string{"message.send", `"dry_run": true`, `"multipart": true`, `"schema_drift"`, `"validation_error"`}[i]
					if !strings.Contains(text, want) {
						t.Fatalf("response %d missing %q: %s", i+1, want, text)
					}
					if i == 1 && (!strings.Contains(text, "forced-channel") || strings.Contains(text, "model-channel")) {
						t.Fatalf("operator context did not win: %s", text)
					}
				}
			})
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick free port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitPostJSON polls the HTTP endpoint until the server is up, then returns the
// response body. A loopback Origin passes the anti-DNS-rebinding gate.
func waitPostJSON(t *testing.T, url, payload string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("POST", url, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://localhost")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return buf.Bytes()
	}
	t.Fatalf("server never became ready: %v", lastErr)
	return nil
}
