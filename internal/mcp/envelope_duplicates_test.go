package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestXIN2549EnvelopeDuplicates(t *testing.T) {
	var hits atomic.Int32
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"data":{"message_id":"fixture"}}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, tool := range []string{"execute", "call_op"} {
			params := xin2549Params(tool, false)
			for _, field := range []string{"method", "params", "id", "jsonrpc"} {
				for _, escaped := range []bool{false, true} {
					name := fmt.Sprintf("%s/%s/%s/escaped=%t", transport, tool, field, escaped)
					t.Run(name, func(t *testing.T) {
						key := field
						if escaped {
							key = fmt.Sprintf(`\u%04x%s`, field[0], field[1:])
						}
						values := map[string]string{"method": `"ping"`, "params": `{"name":"missing"}`, "id": `1`, "jsonrpc": `"1.0"`}
						if tool == "execute" {
							values["params"] = xin2549Params(tool, true)
						}
						canonical := map[string]string{"method": `"tools/call"`, "params": params, "id": `2`, "jsonrpc": `"2.0"`}
						members := []string{}
						for _, k := range []string{"jsonrpc", "id", "method", "params"} {
							if k == field {
								members = append(members, `"`+k+`":`+values[k]+`,"`+key+`":`+canonical[k])
							} else {
								members = append(members, fmt.Sprintf("%q:%s", k, canonical[k]))
							}
						}
						wire := "{" + strings.Join(members, ",") + "}"
						before := hits.Load()
						raw := xin2549Dispatch(t, transport, wire, backend.URL)
						var response rpcResponse
						if err := json.Unmarshal(raw, &response); err != nil {
							t.Fatal(err)
						}
						t.Logf("deliveries=%d response_id=%s rpc_error=%t", hits.Load()-before, response.ID, response.Error != nil)
						if hits.Load() != before || response.Error == nil || string(response.ID) != "null" {
							t.Error("duplicate envelope member must fail before dispatch")
						}
					})
				}
			}
		}
	}
}

func xin2549Dispatch(t *testing.T, transport, wire, url string, facades ...facadeMode) []byte {
	t.Helper()
	facade := facadeBoth
	if len(facades) > 0 {
		facade = facades[0]
	}
	if transport == "http" {
		h, err := testHTTPHandler(t, testRoot, TrustedContext{}, cmdutil.GlobalOptions{NoRetry: true}, facade)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/", strings.NewReader(wire))
		r.Header.Set("Authorization", "Bearer bf_fixture")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Body.Bytes()
	}
	s := newTestServer(t).WithFacade(facade)
	s.factoryFn = fakeBackendFactory(url, "bf_fixture")
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(wire+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestXIN2549EnvelopeControls(t *testing.T) {
	var hits atomic.Int32
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"data":{"message_id":"fixture"}}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, control := range []string{"live", "dry-run", "nested-duplicate", "invalid-version", "unknown-method", "malformed"} {
			t.Run(transport+"/"+control, func(t *testing.T) {
				wire := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + xin2549Params("execute", control == "dry-run") + `}`
				switch control {
				case "nested-duplicate":
					wire = strings.Replace(wire, `"operation_id":"message.send"`, `"operation_id":"message.send","operation_id":"message.send"`, 1)
				case "invalid-version":
					wire = strings.Replace(wire, `"jsonrpc":"2.0"`, `"jsonrpc":"1.0"`, 1)
				case "unknown-method":
					wire = strings.Replace(wire, `"method":"tools/call"`, `"method":"missing"`, 1)
				case "malformed":
					wire = "{"
				}
				before := hits.Load()
				raw := xin2549Dispatch(t, transport, wire, backend.URL)
				var response rpcResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				want := int32(0)
				if control == "live" {
					want = 1
				}
				if hits.Load()-before != want {
					t.Errorf("deliveries=%d want=%d", hits.Load()-before, want)
				}
				if control != "live" && control != "dry-run" && response.Error == nil {
					t.Error("invalid control was accepted")
				}
				t.Logf("deliveries=%d rpc_error=%t", hits.Load()-before, response.Error != nil)
			})
		}
	}
}
