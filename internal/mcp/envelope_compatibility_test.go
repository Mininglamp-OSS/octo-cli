package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestEnvelopeReverseAndEqualDuplicates(t *testing.T) {
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, c := range []struct {
			facade facadeMode
			tool   string
		}{{facadeTwo, "execute"}, {facadeThree, "call_op"}, {facadeBoth, "execute"}, {facadeBoth, "call_op"}} {
			canonical := map[string]string{"jsonrpc": `"2.0"`, "id": `1`, "method": `"tools/call"`, "params": xin2549Params(c.tool, false)}
			other := map[string]string{"jsonrpc": `"1.0"`, "id": `2`, "method": `"ping"`, "params": `{"name":"missing"}`}
			if c.tool == "execute" {
				other["params"] = xin2549Params(c.tool, true)
			}
			for _, field := range []string{"method", "params", "id", "jsonrpc"} {
				for _, equal := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/%s/equal=%t", transport, c.facade, c.tool, field, equal), func(t *testing.T) {
						members := []string{}
						for _, k := range []string{"jsonrpc", "id", "method", "params"} {
							members = append(members, fmt.Sprintf("%q:%s", k, canonical[k]))
							if k == field {
								last := other[k]
								if equal {
									last = canonical[k]
								}
								members = append(members, fmt.Sprintf("%q:%s", k, last))
							}
						}
						before := hits.Load()
						raw := xin2549Dispatch(t, transport, "{"+strings.Join(members, ",")+"}", backend.URL, c.facade)
						var response rpcResponse
						if err := json.Unmarshal(raw, &response); err != nil {
							t.Fatal(err)
						}
						if hits.Load() != before || response.Error == nil || response.Error.Code != codeParseError {
							t.Fatalf("duplicate must fail before dispatch: deliveries=%d error=%+v", hits.Load()-before, response.Error)
						}
					})
				}
			}
		}
	}
}

func TestEnvelopeCompatibility(t *testing.T) {
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, tc := range []struct {
			name, wire string
			code       int
			noResponse bool
		}{
			{"notification", `{"jsonrpc":"2.0","method":"tools/call","params":` + xin2549Params("execute", false) + `}`, 0, true},
			{"duplicate notification", `{"method":"ping","method":"tools/call","params":` + xin2549Params("execute", false) + `}`, codeParseError, false},
			{"escaped notification", `{"method":"ping","\u006dethod":"ping"}`, codeParseError, false},
			{"nested extension", `{"id":1,"method":"ping","extra":{"a":1,"a":2}}`, 0, false},
			{"nested params", `{"id":1,"method":"ping","params":{"a":1,"a":2}}`, 0, false},
			{"extension duplicate", `{"id":1,"method":"ping","extra":1,"extra":2}`, codeParseError, false},
			{"case collision", `{"id":1,"method":"missing","Method":"ping"}`, codeParseError, false},
			{"single case alias", `{"ID":1,"Method":"ping"}`, 0, false},
			{"missing version", `{"id":1,"method":"ping"}`, 0, false},
			{"null", `null`, 0, true},
			{"invalid version", `{"id":1,"jsonrpc":"1.0","method":"ping"}`, codeInvalidRequest, false},
			{"unknown method", `{"id":1,"method":"missing"}`, codeMethodNotFound, false},
			{"array", `[]`, codeParseError, false},
			{"trailing value", `{"id":1,"method":"ping"}{}`, codeParseError, false},
			{"truncated", `{"id":1,"method":`, codeParseError, false},
			{"trailing comma", `{"id":1,"method":"ping",}`, codeParseError, false},
		} {
			t.Run(transport+"/"+tc.name, func(t *testing.T) {
				before := hits.Load()
				raw := xin2549Dispatch(t, transport, tc.wire, backend.URL)
				if hits.Load() != before {
					t.Fatal("control dispatched a backend operation")
				}
				if tc.noResponse {
					if strings.TrimSpace(string(raw)) != "" {
						t.Fatal("notification produced a response")
					}
					return
				}
				var response rpcResponse
				if err := json.Unmarshal(raw, &response); err != nil {
					t.Fatal(err)
				}
				if tc.code == 0 {
					if response.Error != nil {
						t.Fatalf("compatibility control refused: %+v", response.Error)
					}
				} else if response.Error == nil || response.Error.Code != tc.code {
					t.Fatalf("error=%+v, want code %d", response.Error, tc.code)
				}
			})
		}
	}
}
