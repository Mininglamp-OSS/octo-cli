package mcp_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBuiltBinary_XIN2544HTTPContextProvenance(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, forced := range []bool{false, true} {
		t.Run(map[bool]string{false: "unforced", true: "operator-forced"}[forced], func(t *testing.T) {
			var mu sync.Mutex
			var deliveries []string
			backend := binaryPolicyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				deliveries = append(deliveries, r.Header.Get("Authorization")+" space="+r.Header.Get("X-Space-Id")+" "+string(body))
				mu.Unlock()
				_, _ = w.Write([]byte(`{"message_id":123}`))
			}))
			defer backend.Close()
			count := func() int { mu.Lock(); defer mu.Unlock(); return len(deliveries) }
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			addr := freeAddr(t)
			args := []string{"mcp", "serve", "--http", addr, "--facade", "both"}
			if forced {
				args = append(args, "--space", "operator-space", "--force-channel-id", "operator-channel", "--force-channel-type", "2")
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
			client := &http.Client{Timeout: 5 * time.Second}
			defer client.CloseIdleConnections()
			for _, tool := range []string{"execute", "call_op"} {
				call := xin2453Request(tool, `{"operation_id":"message.send","arguments":`+xin2453Message+`}`)
				for _, key := range []string{"X-Space-Id", "X-Octo-Channel-Id", "X-Octo-Channel-Type", "X-Octo-On-Behalf-Of", "x-sPaCe-iD", "x-oCtO-cHaNnEl-Id", "X-OCTO-CHANNEL-TYPE", "x-octo-on-behalf-of"} {
					for _, vals := range [][]string{{"arbitrary-scope"}, {""}, {"first", "second"}, {"first, second"}} {
						before := count()
						req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(call))
						req.Header.Set("Authorization", "Bearer bf_same_caller")
						req.Header[key] = vals
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, _ := io.ReadAll(resp.Body)
						_ = resp.Body.Close()
						if resp.StatusCode != 403 || count() != before {
							t.Errorf("%s key=%s vals=%v: status=%d deliveries=%d, want 403 and zero new deliveries", tool, key, vals, resp.StatusCode, count()-before)
						}
						if strings.Contains(string(body), "operator-") || strings.Contains(string(body), "arbitrary-scope") {
							t.Errorf("refusal disclosed context: %s", body)
						}
					}
				}
				// Same bearer, no context headers: legitimate business arguments
				// remain ordinary arguments; only operator values force them.
				for _, token := range []string{"bf_same_caller", "bf_other_caller", ""} {
					before := count()
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(call))
					if token != "" {
						req.Header.Set("Authorization", "Bearer "+token)
					}
					resp, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					if token == "" {
						if count() != before || !strings.Contains(string(body), "BOT_AUTH_REQUIRED") {
							t.Fatal("missing bearer inherited host credential")
						}
						continue
					}
					text := xin2453Text(t, string(body))
					if token == "" {
						if count() != before || !strings.Contains(text, "auth_error") {
							t.Errorf("missing bearer inherited ambient credential: %s", text)
						}
						continue
					}
					if count() != before+1 || strings.Contains(text, `"ok": false`) {
						t.Fatalf("no-header control failed: %s", text)
					}
					mu.Lock()
					wire := deliveries[len(deliveries)-1]
					mu.Unlock()
					want := []string{"Bearer " + token}
					if forced {
						want = append(want, "space=operator-space", `"channel_id":"operator-channel"`, `"channel_type":2`)
					} else {
						want = append(want, `"channel_id":"c1"`, `"channel_type":1`)
						if strings.Contains(wire, "on_behalf_of") || strings.Contains(wire, "operator-") {
							t.Errorf("unforced request acquired provenance: %s", wire)
						}
					}
					for _, s := range want {
						if !strings.Contains(wire, s) {
							t.Errorf("wire missing %s: %s", s, wire)
						}
					}
				}
			}
			// HTTP field names are ASCII tokens. Unicode lookalikes cannot be
			// smuggled through the wire parser, including the long-s fold.
			for _, name := range []string{"X-ſpace-Id", "X-Octo-Khannel-Id"} {
				before := count()
				conn, err := net.DialTimeout("tcp", addr, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = io.WriteString(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer bf_same_caller\r\n"+name+": evil\r\nContent-Length: 2\r\n\r\n{}")
				resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
				if err != nil {
					_ = conn.Close()
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				_ = conn.Close()
				if resp.StatusCode != 400 || count() != before {
					t.Errorf("Unicode header status=%d deliveries=%d", resp.StatusCode, count()-before)
				}
			}
		})
	}
}
