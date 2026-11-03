package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestBuiltBinary_MisplacedSafetyFieldsNeverRunLive covers the levels next to
// the execute object that TestBuiltBinary_ExecuteStrictSafetyDecoding does
// not: a tool switch beside params.arguments, a duplicate arguments object,
// and a switch inside the operation arguments. Each must be refused with no
// backend request, over both transports and both facades that carry execute.
func TestBuiltBinary_MisplacedSafetyFieldsNeverRunLive(t *testing.T) {
	bin := buildOctoCLI(t)
	const opArgs = `{"channel_id":"c1","channel_type":1,"payload":{"text":"test"}}`
	const exec1 = `{"operation_id":"message.send","arguments":` + opArgs + `}`
	nestedArgs := func(field string) string {
		return `{"operation_id":"message.send","arguments":{"channel_id":"c1","channel_type":1,"payload":{"text":"test"},` + field + `}}`
	}
	rpcRefused := []string{
		`{"name":"execute","arguments":` + exec1 + `,"dry_run":true}`,
		`{"name":"execute","dry_run":true,"arguments":` + exec1 + `}`,
		`{"name":"execute","arguments":` + exec1 + `,"schema_fingerprint":"sha256:stale"}`,
		`{"name":"execute","arguments":{"operation_id":"message.send","dry_run":true},"arguments":` + exec1 + `}`,
		`{"name":"execute","Arguments":` + exec1 + `}`,
		`{"name":"execute","name":"execute","arguments":` + exec1 + `}`,
	}
	toolRefused := []string{
		`{"name":"execute","arguments":` + nestedArgs(`"dry_run":true`) + `}`,
		`{"name":"execute","arguments":` + nestedArgs(`"Dry-Run":true`) + `}`,
		`{"name":"execute","arguments":` + nestedArgs(`"schema_fingerprint":"sha256:stale"`) + `}`,
	}
	for _, facade := range []string{"two", "both"} {
		for _, transport := range []string{"stdio", "http"} {
			t.Run(facade+"/"+transport, func(t *testing.T) {
				var hits atomic.Int32
				backend := binaryPolicyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"message_id":"m1"}}`))
				}))
				defer backend.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
				call := func(params string) (int, map[string]any) {
					t.Helper()
					request := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
					var raw []byte
					if transport == "stdio" {
						cmd := exec.CommandContext(ctx, bin, args...)
						cmd.Env = env
						cmd.Stdin = strings.NewReader(request + "\n")
						var err error
						if raw, err = cmd.Output(); err != nil {
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
						t.Fatalf("%v: %s", err, raw)
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
				for _, params := range rpcRefused {
					before := hits.Load()
					if code, out := call(params); code != -32602 || hits.Load() != before {
						t.Errorf("%s: want -32602 and no backend request, got code=%d result=%v hits=%d", params, code, out, hits.Load()-before)
					}
				}
				refusedTool := toolRefused
				if facade == "both" {
					refusedTool = append(refusedTool, `{"name":"call_op","arguments":`+nestedArgs(`"dry_run":true`)+`}`)
				}
				for _, params := range refusedTool {
					before := hits.Load()
					code, out := call(params)
					if code != 0 || out["status"] == "ok" || out["ok"] == true || hits.Load() != before {
						t.Errorf("%s: want a refused tool result and no backend request, got code=%d result=%v hits=%d", params, code, out, hits.Load()-before)
					}
				}

				// The legitimate shapes still work: _meta is accepted, a
				// canonical dry run sends nothing, a live call sends once.
				before := hits.Load()
				code, out := call(`{"name":"execute","_meta":{"progressToken":1},"arguments":{"operation_id":"message.send","dry_run":true,"arguments":` + opArgs + `}}`)
				if code != 0 || out["status"] != "ok" || out["dry_run"] != true || hits.Load() != before {
					t.Errorf("canonical dry run must preview without I/O: code=%d result=%v", code, out)
				}
				code, out = call(`{"name":"execute","arguments":` + exec1 + `}`)
				if code != 0 || out["status"] != "ok" || hits.Load() != before+1 {
					t.Errorf("canonical live call must execute once: code=%d result=%v hits=%d", code, out, hits.Load()-before)
				}
			})
		}
	}
}
