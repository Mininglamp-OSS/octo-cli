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
	"sync/atomic"
	"testing"
	"time"
)

func TestBuiltBinary_ExecuteStrictSafetyDecoding(t *testing.T) {
	bin := buildOctoCLI(t)
	variants := []string{
		`"dry-run":true`, `"dryRun":true`, `"dry_rnu":true`, `"dry_run":null`,
		`"Dry_Run":true`, `"dry_run":"yes"`, `"dry_run":1`, `"dry_run":{}`,
		`"schema-fingerprint":"stale"`, `"schemaFingerprint":"stale"`,
		`"schema_fingerprnit":"stale"`, `"schema_fingerprint":null`,
		`"Schema_Fingerprint":"stale"`, `"schema_fingerprint":true`,
		`"schema_fingerprint":"a","schema_fingerprint":"b"`,
		`"unknown":true`, `"dry_run":true,"dry_run":false`,
	}
	for _, facade := range []string{"two", "both"} {
		for _, transport := range []string{"stdio", "http"} {
			t.Run(facade+"/"+transport, func(t *testing.T) {
				var hits atomic.Int32
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"message_id":"m1"}}`))
				}))
				defer backend.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				env := append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
				args := []string{"mcp", "serve", "--facade", facade}
				url := ""
				if transport == "http" {
					addr := freeAddr(t)
					url = "http://" + addr
					cmd := exec.CommandContext(ctx, bin, append(args, "--http", addr)...)
					cmd.Env = env
					if err := cmd.Start(); err != nil {
						t.Fatal(err)
					}
					defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
					waitPostJSON(t, url, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
				}
				callRequest := func(request string) (int, map[string]any) {
					t.Helper()
					var raw []byte
					if transport == "stdio" {
						cmd := exec.CommandContext(ctx, bin, args...)
						cmd.Env = env
						cmd.Stdin = strings.NewReader(request + "\n")
						var err error
						raw, err = cmd.Output()
						if err != nil {
							t.Fatal(err)
						}
					} else {
						req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(request))
						req.Header.Set("Authorization", "Bearer bf_test")
						resp, err := http.DefaultClient.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						raw, err = io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						if err != nil {
							t.Fatal(err)
						}
					}
					var resp struct {
						Error  *struct{ Code int }                       `json:"error"`
						Result struct{ Content []struct{ Text string } } `json:"result"`
					}
					if err := json.Unmarshal(bytes.TrimSpace(raw), &resp); err != nil {
						t.Fatal(err)
					}
					if resp.Error != nil {
						return resp.Error.Code, nil
					}
					if len(resp.Result.Content) != 1 {
						t.Fatalf("missing result: %s", raw)
					}
					var out map[string]any
					if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &out); err != nil {
						t.Fatal(err)
					}
					return 0, out
				}
				call := func(field string) (int, map[string]any) {
					if field != "" {
						field = "," + field
					}
					return callRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"message.send","arguments":{"channel_id":"c1","channel_type":1,"payload":{"text":"test"}}` + field + `}}}`)
				}
				for _, params := range []string{
					`"name":"execute","schema_fingerprint":"x","arguments":{"operation_id":"message.send"}`,
					`"name":"execute","dry_run":true,"arguments":{"operation_id":"message.send"}`,
					`"name":"execute","arguments":{"operation_id":"message.send","arguments":{"dry_run":true}}`,
					`"name":"execute","arguments":{"operation_id":"message.send","dry_run":true},"arguments":{"operation_id":"message.send"}`,
					`"name":"execute","arguments":{"operation_id":"message.send","arguments":{"Dry_Run":true}}`,
					`"name":"execute","arguments":{"operation_id":"message.send","arguments":{"schema_fingerprint":"stale"}}`,
				} {
					before := hits.Load()
					code, out := callRequest(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{` + params + `}}`)
					if code != -32602 || hits.Load() != before {
						t.Errorf("misplaced safety field: code=%d result=%v hits=%d", code, out, hits.Load()-before)
					}
				}

				for _, variant := range variants {
					t.Run(variant, func(t *testing.T) {
						before := hits.Load()
						code, out := call(variant)
						if code != -32602 || hits.Load() != before {
							t.Errorf("malformed safety field must be rejected before I/O: code=%d result=%v backend hits=%d", code, out, hits.Load()-before)
						}
					})
				}
				before := hits.Load()
				code, out := call(`"dry_run":true`)
				if code != 0 || out["status"] != "ok" || out["dry_run"] != true || hits.Load() != before {
					t.Errorf("canonical dry-run must preview without I/O: code=%d result=%v", code, out)
				}
				for _, field := range []string{"", `"dry_run":false`} {
					before = hits.Load()
					code, out = call(field)
					if code != 0 || out["status"] != "ok" || hits.Load() != before+1 {
						t.Errorf("canonical live call must execute once: code=%d result=%v hits=%d", code, out, hits.Load()-before)
					}
				}
			})
		}
	}
}
