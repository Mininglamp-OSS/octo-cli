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

// XIN-2453 against the real binary, over stdio and HTTP, with an isolated mock
// backend that counts every request it reads:
//   - F1: call_op refuses a dry_run / schema_fingerprint switch (or any other
//     unknown or repeated key) next to operation_id instead of dropping it and
//     sending the operation live;
//   - F2: a Unicode case-fold spelling of a tool switch inside the operation
//     arguments ("ſchema_fingerprint", U+017F long s) is refused like its ASCII
//     spellings, on execute and call_op;
//   - F3: a non-idempotent write whose delivery is ambiguous (the backend read
//     the whole request and then dropped the connection, or answered 502/503)
//     reaches the backend exactly once and is reported as an unknown result,
//     from execute, call_op and the CLI alike.
// Every refusal asserts zero backend requests, not only the error shape.

const xin2453Message = `{"channel_id":"c1","channel_type":1,"payload":{"type":1,"content":"xin2453"}}`

type xin2453Backend struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits int
	mode string
}

func newXIN2453Backend(t *testing.T) *xin2453Backend {
	t.Helper()
	b := &xin2453Backend{}
	b.srv = binaryPolicyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		b.mu.Lock()
		b.hits++
		mode := b.mode
		b.mu.Unlock()
		switch mode {
		case "drop":
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		case "502":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"mock"}`))
		case "503":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":"mock"}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"message_id":123}`))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *xin2453Backend) reset(mode string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hits, b.mode = 0, mode
}

func (b *xin2453Backend) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hits
}

func xin2453Request(tool, arguments string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + arguments + `}}`
}

// xin2453Serve runs one request through a fresh `mcp serve` and returns the raw
// JSON-RPC response line.
func xin2453Serve(t *testing.T, bin, transport, facade, backendURL, request string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	addr := freeAddr(t)
	args := []string{"mcp", "serve", "--facade", facade}
	if transport == "http" {
		args = append(args, "--http", addr)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backendURL)
	if transport == "stdio" {
		cmd.Stdin = strings.NewReader(request + "\n")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("stdio server: %v", err)
		}
		return string(bytes.TrimSpace(out))
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(request))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bf_smoke")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, _ := io.ReadAll(resp.Body)
	return string(line)
}

// xin2453Text returns the tool result text, or "" for a JSON-RPC error.
func xin2453Text(t *testing.T, line string) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("invalid response: %.400s", line)
	}
	if len(resp.Result.Content) == 0 {
		return ""
	}
	return resp.Result.Content[0].Text
}

func TestBuiltBinary_XIN2453SafetyFieldsAndSingleDelivery(t *testing.T) {
	bin := buildOctoCLI(t)
	backend := newXIN2453Backend(t)

	type refusal struct {
		name, facade, tool, arguments string
		want                          []string
	}
	invalidParams := []string{`"code":-32602`, "invalid call_op arguments"}
	opFieldRefused := []string{"is a tool option, not an operation argument"}
	withField := func(name string) string {
		return `{"operation_id":"message.send","arguments":` + strings.TrimSuffix(xin2453Message, "}") + `,"` + name + `":"x"}}`
	}
	var refusals []refusal
	for _, facade := range []string{"three", "both"} {
		refusals = append(refusals,
			refusal{"F1 call_op dry_run", facade, "call_op", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"dry_run":true}`, append(invalidParams, `unknown argument \"dry_run\"`)},
			refusal{"F1 call_op dry_run false", facade, "call_op", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"dry_run":false}`, invalidParams},
			refusal{"F1 call_op DRY_RUN", facade, "call_op", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"DRY_RUN":true}`, invalidParams},
			refusal{"F1 call_op schema_fingerprint", facade, "call_op", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"schema_fingerprint":"sha256:x"}`, invalidParams},
			refusal{"F1 call_op unknown key", facade, "call_op", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"Arguments":{}}`, invalidParams},
			refusal{"F1 call_op duplicate arguments", facade, "call_op", `{"operation_id":"message.send","arguments":{},"arguments":` + xin2453Message + `}`, append(invalidParams, `duplicate argument \"arguments\"`)},
			refusal{"F1 call_op duplicate operation_id", facade, "call_op", `{"operation_id":"message.get","operation_id":"message.send","arguments":` + xin2453Message + `}`, invalidParams},
			refusal{"F2 call_op long-s fingerprint", facade, "call_op", withField("ſchema_fingerprint"), opFieldRefused},
			refusal{"F2 call_op long-s upper fingerprint", facade, "call_op", withField("ſCHEMA_FINGERPRINT"), opFieldRefused},
		)
	}
	for _, facade := range []string{"two", "both"} {
		refusals = append(refusals,
			refusal{"F1 execute DRY_RUN", facade, "execute", `{"operation_id":"message.send","arguments":` + xin2453Message + `,"DRY_RUN":true}`, []string{`"code":-32602`, "invalid execute arguments"}},
			refusal{"F2 execute long-s fingerprint", facade, "execute", withField("ſchema_fingerprint"), append([]string{`\"status\": \"validation_error\"`}, opFieldRefused...)},
			refusal{"F2 execute long-s upper fingerprint", facade, "execute", withField("ſCHEMA_FINGERPRINT"), opFieldRefused},
			refusal{"F2 execute mixed-case fingerprint", facade, "execute", withField("Schema_FingerPrint"), opFieldRefused},
			refusal{"F2 execute separator dry run", facade, "execute", withField("Dry-Run"), opFieldRefused},
		)
	}

	for _, transport := range []string{"stdio", "http"} {
		for _, r := range refusals {
			t.Run(transport+"/"+r.facade+"/"+r.name, func(t *testing.T) {
				backend.reset("")
				line := xin2453Serve(t, bin, transport, r.facade, backend.srv.URL, xin2453Request(r.tool, r.arguments))
				for _, w := range r.want {
					if !strings.Contains(line, w) {
						t.Errorf("missing %q: %.600s", w, line)
					}
				}
				if n := backend.count(); n != 0 {
					t.Errorf("backend requests = %d, want 0: %.600s", n, line)
				}
			})
		}
	}

	// The legitimate shapes still work: a call_op with params._meta and an
	// execute dry run with the canonical switch.
	t.Run("controls", func(t *testing.T) {
		for _, transport := range []string{"stdio", "http"} {
			backend.reset("")
			meta := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","_meta":{"progressToken":"p1"},"arguments":{"operation_id":"message.send","arguments":` + xin2453Message + `}}}`
			line := xin2453Serve(t, bin, transport, "three", backend.srv.URL, meta)
			if !strings.Contains(xin2453Text(t, line), `"ok": true`) || backend.count() != 1 {
				t.Errorf("%s call_op with _meta: hits=%d %.600s", transport, backend.count(), line)
			}
			backend.reset("")
			line = xin2453Serve(t, bin, transport, "two", backend.srv.URL, xin2453Request("execute", `{"operation_id":"message.send","arguments":`+xin2453Message+`,"dry_run":true}`))
			if !strings.Contains(xin2453Text(t, line), `"dry_run": true`) || backend.count() != 0 {
				t.Errorf("%s execute dry run: hits=%d %.600s", transport, backend.count(), line)
			}
		}
	})

	// F3: one delivery for an ambiguous write, whichever facade sends it.
	for _, transport := range []string{"stdio", "http"} {
		for _, mode := range []string{"drop", "502", "503"} {
			for _, c := range []struct{ facade, tool, want string }{
				{"two", "execute", `"status": "result_unknown"`},
				{"three", "call_op", `"code": "RESULT_UNKNOWN"`},
			} {
				t.Run(transport+"/F3/"+c.tool+"/"+mode, func(t *testing.T) {
					backend.reset(mode)
					line := xin2453Serve(t, bin, transport, c.facade, backend.srv.URL, xin2453Request(c.tool, `{"operation_id":"message.send","arguments":`+xin2453Message+`}`))
					text := xin2453Text(t, line)
					if !strings.Contains(text, c.want) {
						t.Errorf("missing %q: %.600s", c.want, text)
					}
					if strings.Contains(text, `"retryable": true`) {
						t.Errorf("an unknown write outcome must not be retryable: %.600s", text)
					}
					if n := backend.count(); n != 1 {
						t.Errorf("backend deliveries = %d, want exactly 1", n)
					}
				})
			}
		}
	}

	t.Run("cli/F3", func(t *testing.T) {
		for _, mode := range []string{"drop", "502", "503"} {
			backend.reset(mode)
			cmd := exec.Command(bin, "message", "send", "--data", xin2453Message)
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.srv.URL)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Errorf("%s: want a failure: %s", mode, out)
			}
			if !strings.Contains(string(out), `"code": "RESULT_UNKNOWN"`) {
				t.Errorf("%s: want RESULT_UNKNOWN: %s", mode, out)
			}
			if n := backend.count(); n != 1 {
				t.Errorf("%s: backend deliveries = %d, want exactly 1", mode, n)
			}
		}
		// Through the generic passthrough too: a POST is sent once, while a GET
		// keeps the transport retries (one try plus three).
		for _, c := range []struct {
			method string
			want   int
		}{{"POST", 1}, {"GET", 4}} {
			backend.reset("503")
			cmd := exec.Command(bin, "api", c.method, "/v1/bot/xin2453")
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.srv.URL)
			out, _ := cmd.CombinedOutput()
			if n := backend.count(); n != c.want {
				t.Errorf("api %s: backend deliveries = %d, want %d: %s", c.method, n, c.want, out)
			}
		}
	})
}
