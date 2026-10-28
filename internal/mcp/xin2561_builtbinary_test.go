package mcp_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestBuiltBinary_XIN2561EnvelopeDuplicates(t *testing.T) {
	bin := buildOctoCLI(t)
	backend := newXIN2453Backend(t)
	for _, transport := range []string{"http", "stdio"} {
		for _, c := range []struct{ facade, tool string }{{"two", "execute"}, {"three", "call_op"}, {"both", "execute"}, {"both", "call_op"}} {
			params := `{"name":"` + c.tool + `","arguments":{"operation_id":"message.send","arguments":` + xin2453Message + `}}`
			canonical := map[string]string{"jsonrpc": `"2.0"`, "id": `2`, "method": `"tools/call"`, "params": params}
			for _, field := range []string{"method", "params", "id", "jsonrpc"} {
				for _, escaped := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/%s/%s/escaped=%t", transport, c.facade, c.tool, field, escaped), func(t *testing.T) {
						key := field
						if escaped {
							key = fmt.Sprintf(`\u%04x%s`, field[0], field[1:])
						}
						first := map[string]string{"jsonrpc": `"1.0"`, "id": `1`, "method": `"ping"`, "params": `{"name":"missing"}`}[field]
						if field == "params" && c.tool == "execute" {
							first = strings.Replace(params, `"operation_id":`, `"dry_run":true,"operation_id":`, 1)
						}
						members := []string{}
						for _, k := range []string{"jsonrpc", "id", "method", "params"} {
							outKey := k
							if k == field {
								members = append(members, fmt.Sprintf("%q:%s", k, first))
								outKey = key
							}
							members = append(members, `"`+outKey+`":`+canonical[k])
						}
						backend.reset("")
						line := xin2453Serve(t, bin, transport, c.facade, backend.srv.URL, "{"+strings.Join(members, ",")+"}")
						var response struct {
							Error *struct{ Code int } `json:"error"`
						}
						if err := json.Unmarshal([]byte(line), &response); err != nil {
							t.Fatal(err)
						}
						if backend.count() != 0 || response.Error == nil || response.Error.Code != -32700 {
							t.Fatalf("duplicate accepted: deliveries=%d rpc_error=%+v", backend.count(), response.Error)
						}
					})
				}
			}
		}
	}
}
