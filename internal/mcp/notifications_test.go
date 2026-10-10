package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func notificationToolRequest() rpcRequest {
	return rpcRequest{
		Jsonrpc: jsonrpcVersion, Method: "tools/call",
		Params: json.RawMessage(`{"name":"call_op","arguments":{"operation_id":"message.send","arguments":{"channel_id":"fixture-channel","channel_type":2,"payload":{"type":1,"content":"notification fixture"}}}}`),
	}
}

func TestNotificationTransportsExecuteWithoutResponding(t *testing.T) {
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			for _, test := range []struct {
				name        string
				version     string
				withID      bool
				backendCode int
				wantCalls   int32
			}{
				{"valid-notification", jsonrpcVersion, false, http.StatusOK, 1},
				{"lost-write-outcome-is-not-retried", jsonrpcVersion, false, http.StatusServiceUnavailable, 1},
				{"bad-version-notification", "1.0", false, http.StatusOK, 0},
				{"missing-version-notification", "", false, http.StatusOK, 0},
				{"ordinary-request", jsonrpcVersion, true, http.StatusOK, 1},
			} {
				t.Run(test.name, func(t *testing.T) {
					var calls atomic.Int32
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.Method != http.MethodPost || r.URL.Path != "/v1/bot/sendMessage" || r.Header.Get("Authorization") != "Bearer bf_fixture" {
							t.Errorf("unexpected backend request: %s %s auth=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
						}
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(test.backendCode)
						_, _ = w.Write([]byte(`{"message_id":"123"}`))
					}))
					defer backend.Close()
					t.Setenv("OCTO_API_BASE_URL", backend.URL)
					t.Setenv("OCTO_TOKEN", "bf_fixture")
					req := notificationToolRequest()
					req.Jsonrpc = test.version
					if test.withID {
						req.ID = json.RawMessage(`9`)
					}
					raw, err := json.Marshal(req)
					if err != nil {
						t.Fatal(err)
					}
					var body []byte
					if transport == "stdio" {
						s := newTestServer(t)
						var out bytes.Buffer
						if err := s.ServeStdio(context.Background(), strings.NewReader(string(raw)+"\n"), &out); err != nil {
							t.Fatal(err)
						}
						body = out.Bytes()
					} else {
						h := lifecycleHTTPHandler(t)
						r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
						r.Header.Set("Authorization", "Bearer bf_fixture")
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						wantStatus := http.StatusAccepted
						if test.withID {
							wantStatus = http.StatusOK
						}
						if w.Code != wantStatus {
							t.Errorf("HTTP status = %d, want %d: %s", w.Code, wantStatus, w.Body.String())
						}
						body = w.Body.Bytes()
						if h.readBudget.small.Load() != 0 || h.readBudget.large.Load() != 0 {
							t.Error("notification retained the HTTP input reservation")
						}
					}
					if calls.Load() != test.wantCalls {
						t.Errorf("backend calls = %d, want %d", calls.Load(), test.wantCalls)
					}
					if !test.withID && len(body) != 0 {
						t.Errorf("notification emitted a response: %s", body)
					}
					if test.withID && !bytes.Contains(body, []byte(`"id":9`)) {
						t.Errorf("ordinary request did not receive its response: %s", body)
					}
				})
			}
		})
	}
}

func TestNotificationInitializeUpdatesConnection(t *testing.T) {
	s := newTestServer(t)
	s.protocolVersion = "not-initialized"
	_, has := s.Dispatch(context.Background(), rpcRequest{
		Jsonrpc: jsonrpcVersion, Method: "initialize",
		Params: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}}}`),
	})
	if has || s.protocolVersion != defaultProtocolVersion || !s.clientResources {
		t.Fatalf("initialize notification: response=%v version=%q resources=%v", has, s.protocolVersion, s.clientResources)
	}
	for _, req := range []rpcRequest{
		{Method: "notifications/initialized"},
		{Jsonrpc: jsonrpcVersion, Method: "notifications/progress", Params: json.RawMessage(`{"progress":1}`)},
		{Jsonrpc: jsonrpcVersion, Method: "unknown-method"},
	} {
		if _, has := s.Dispatch(context.Background(), req); has {
			t.Fatalf("id-less message emitted a response: %+v", req)
		}
	}
}

func TestHTTPNotificationRequiresItsOwnBearer(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	t.Setenv("OCTO_TOKEN", "bf_server_must_not_be_used")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(notificationToolRequest())
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)))
	if w.Code != http.StatusAccepted || w.Body.Len() != 0 || calls.Load() != 0 {
		t.Fatalf("unauthenticated notification: status=%d body=%s backend calls=%d", w.Code, w.Body.String(), calls.Load())
	}
}

func TestHTTPNotificationBindingRejectionIsSilent(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, test := range []struct {
		name     string
		operator TrustedContext
		channel  string
	}{
		{"invalid-thread-channel", TrustedContext{}, "invalid"},
		{"conflicting-thread", TrustedContext{ThreadID: "OPERATOR"}, "G____OTHER"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, err := NewHTTPHandler(testRoot, test.operator, cmdutil.GlobalOptions{})
			if err != nil {
				t.Fatal(err)
			}
			h.WithContextHeaders(true)
			for _, withID := range []bool{false, true} {
				req := notificationToolRequest()
				if withID {
					req.ID = json.RawMessage(`9`)
				}
				raw, err := json.Marshal(req)
				if err != nil {
					t.Fatal(err)
				}
				r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
				r.Header.Set("Authorization", "Bearer bf_fixture")
				r.Header.Set(headerChannelID, test.channel)
				r.Header.Set(headerChannelType, "5")
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if withID {
					var response rpcResponse
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Error == nil || response.Error.Code != codeInvalidParams || string(response.ID) != "9" {
						t.Errorf("binding rejection lost ordinary request error: %s", w.Body.String())
					}
				} else if w.Code != http.StatusAccepted || w.Body.Len() != 0 {
					t.Errorf("rejected notification emitted a response: status=%d body=%s", w.Code, w.Body.String())
				}
				if calls.Load() != 0 || h.readBudget.small.Load() != 0 || h.readBudget.large.Load() != 0 {
					t.Errorf("binding rejection executed or leaked its reservation: calls=%d", calls.Load())
				}
			}
		})
	}
}
