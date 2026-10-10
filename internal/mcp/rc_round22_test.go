package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRound22DroppedPostCommitResponseIsUnknown(t *testing.T) {
	for _, tool := range []string{"call_op", "execute"} {
		t.Run(tool, func(t *testing.T) {
			var attempts atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1) // backend accepted the write, response connection failed
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			defer backend.Close()
			s := newTestServer(t).WithFacade(facadeBoth)
			s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
			var args map[string]any
			if err := json.Unmarshal([]byte(rcMessageArgs), &args); err != nil {
				t.Fatal(err)
			}
			result := callTool(t, s, tool, map[string]any{"operation_id": "message.send", "arguments": args})
			if !result.IsError || attempts.Load() != 1 {
				t.Fatalf("write must make one attempt: attempts=%d result=%s", attempts.Load(), result.Content[0].Text)
			}
			if tool == "execute" {
				if got := toolText(t, result)["status"]; got != "result_unknown" {
					t.Fatalf("status = %v, want result_unknown", got)
				}
			} else if got := toolText(t, result)["error"].(map[string]any)["code"]; got != "RESULT_UNKNOWN" {
				t.Fatalf("code = %v, want RESULT_UNKNOWN", got)
			}
		})
	}
}
