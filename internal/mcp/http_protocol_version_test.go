package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHTTPProtocolVersionRejectsBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name       string
		headers    []string
		wantStatus int
		wantCalls  int32
	}{
		{"legacy-headerless", nil, 200, 1},
		{"supported", []string{"2025-06-18"}, 200, 1},
		{"unknown", []string{"unsupported-version"}, 400, 0},
		{"old-unimplemented", []string{"2025-03-26"}, 400, 0},
		{"invalid-date", []string{"1900-01-01"}, 400, 0},
		{"empty", []string{""}, 400, 0},
		{"conflicting", []string{"2025-06-18", "1900-01-01"}, 400, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message_id":"1"}`))
			}))
			defer backend.Close()
			t.Setenv("OCTO_API_BASE_URL", backend.URL)
			h := lifecycleHTTPHandler(t)
			raw, err := json.Marshal(notificationToolRequest())
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			payload["id"] = 1
			raw, err = json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
			r.Header.Set("Authorization", "Bearer bf_fixture")
			for _, v := range test.headers {
				r.Header.Add("MCP-Protocol-Version", v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.wantStatus || calls.Load() != test.wantCalls {
				t.Fatalf("status=%d backend calls=%d; want %d/%d; body=%s", w.Code, calls.Load(), test.wantStatus, test.wantCalls, w.Body.String())
			}
			if h.readBudget.small.Load() != 0 || h.readBudget.large.Load() != 0 {
				t.Fatal("input reservation leaked")
			}
		})
	}
}

func TestHTTPProtocolVersionLifecycle(t *testing.T) {
	h := lifecycleHTTPHandler(t)
	for _, test := range []struct {
		name, method, body, version string
		want                        int
	}{
		{"initialization-without-header", "POST", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`, "", 200},
		{"initialized-notification", "POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "2025-06-18", 202},
		{"unsupported-notification", "POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "1900-01-01", 400},
		{"get-no-sse", "GET", "", "2025-06-18", 405},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, "/", bytes.NewBufferString(test.body))
			if test.version != "" {
				r.Header.Set("MCP-Protocol-Version", test.version)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
			if test.want == 202 && w.Body.Len() != 0 {
				t.Fatal("notification emitted a body")
			}
		})
	}
}
