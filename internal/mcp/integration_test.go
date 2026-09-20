package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// backend captures what the engine actually sent so tests can assert the wire.
type backend struct {
	path   string
	auth   string
	space  string
	body   map[string]any
	status int
	reply  string
}

func (b *backend) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.path = r.URL.Path
		b.auth = r.Header.Get("Authorization")
		b.space = r.Header.Get("X-Space-Id")
		_ = json.NewDecoder(r.Body).Decode(&b.body)
		w.Header().Set("Content-Type", "application/json")
		status := b.status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		reply := b.reply
		if reply == "" {
			reply = `{"message_id":"m-1"}`
		}
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCallOp_SuccessReusesEngineTransport(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "hi"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	env := toolText(t, res)
	if env["ok"] != true {
		t.Errorf("envelope not ok: %v", env)
	}
	if be.path != "/v1/bot/sendMessage" {
		t.Errorf("backend path = %q, want /v1/bot/sendMessage", be.path)
	}
	if !strings.HasPrefix(be.auth, "Bearer bf_test") {
		t.Errorf("Authorization = %q, want the connection bearer", be.auth)
	}
	if be.body["channel_id"] != "c1" {
		t.Errorf("body channel_id = %v, want c1", be.body["channel_id"])
	}
	// message.send has no idempotency key — none must be invented.
	if _, ok := be.body["idempotency_key"]; ok {
		t.Errorf("message.send must not carry an injected idempotency key")
	}
}

// TestCallOp_TrustedContextOverridesModelChannel is the over-privilege防护
// end-to-end proof: the model writes a wrong channel_id, the connection forces
// the trusted one, and the trusted value is what reaches the backend.
func TestCallOp_TrustedContextOverridesModelChannel(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "trusted-chan", SpaceID: "space-forced"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "attacker-chan", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	if be.body["channel_id"] != "trusted-chan" {
		t.Errorf("backend channel_id = %v, want the forced trusted-chan (model value must be overridden)", be.body["channel_id"])
	}
	if be.space != "space-forced" {
		t.Errorf("X-Space-Id = %q, want space-forced (space injected via credential)", be.space)
	}
}

func TestCallOp_MissingRequiredParamIsValidationError(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_type": 1, "payload": map[string]any{"text": "x"}}, // channel_id missing
	})
	if !res.IsError {
		t.Fatalf("missing required field must be an error result")
	}
	env := toolText(t, res)
	errObj, _ := env["error"].(map[string]any)
	if errObj == nil || errObj["type"] != "validation" {
		t.Errorf("expected a validation error envelope, got %v", env)
	}
	if be.path != "" {
		t.Errorf("no request should reach the backend on a pre-flight validation failure, path=%q", be.path)
	}
}

func TestCallOp_ServerErrorSurfacesEnvelope(t *testing.T) {
	be := &backend{status: http.StatusInternalServerError, reply: `{"code":"BOOM","message":"backend exploded"}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("a 5xx backend response must be an error result")
	}
	env := toolText(t, res)
	if env["ok"] != false {
		t.Errorf("server error envelope must have ok:false, got %v", env)
	}
}

func TestCallOp_UnknownOperationSuggestsCandidates(t *testing.T) {
	s := newTestServer(t)
	res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.snd", "arguments": map[string]any{}})
	if !res.IsError {
		t.Fatalf("unknown op must be an error result")
	}
	env := toolText(t, res)
	if _, ok := env["candidates"].([]any); !ok {
		t.Errorf("unknown op should return candidates, got %v", env)
	}
}

// TestCallOp_AutoIdempotencyKeyInjected proves the client-idempotency class
// (design §5.3(d) sub-form two) is inherited: html.publish declares
// x-octo-auto-idempotency-key, so run.go fills it when absent — call_op reuses
// that path unchanged.
func TestCallOp_AutoIdempotencyKeyInjected(t *testing.T) {
	be := &backend{reply: `{"data":{"doc_id":"d1","slug":"d1"}}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "html.publish",
		"arguments":    map[string]any{"html": "<p>hi</p>"},
	})
	if res.IsError {
		t.Fatalf("call_op html.publish errored: %s", res.Content[0].Text)
	}
	key, _ := be.body["idempotency_key"].(string)
	if !strings.HasPrefix(key, "octo-cli-") {
		t.Errorf("html.publish should carry an auto-generated idempotency_key, got %q", key)
	}
}

func TestResources_ListAndRead(t *testing.T) {
	s := newTestServer(t)
	list := s.listResources()
	resources, _ := list["resources"].([]resourceEntry)
	if len(resources) == 0 {
		t.Fatalf("resources/list returned nothing")
	}
	var sawMessaging, sawDisabled bool
	for _, r := range resources {
		if r.URI == "octo://skills/octo-messaging/SKILL.md" {
			sawMessaging = true
			if r.Annotations.Kind != "SKILL.md" || len(r.Annotations.Services) == 0 {
				t.Errorf("messaging resource missing annotations: %+v", r.Annotations)
			}
		}
		if strings.Contains(r.URI, "octo-matter") || strings.Contains(r.URI, "octo-summary") {
			sawDisabled = true
		}
	}
	if !sawMessaging {
		t.Errorf("octo-messaging SKILL.md must be listed")
	}
	if sawDisabled {
		t.Errorf("disabled skills must not be listed as resources")
	}

	content, err := s.readResource("octo://skills/octo-messaging/SKILL.md")
	if err != nil {
		t.Fatalf("readResource: %v", err)
	}
	contents := content["contents"].([]resourceContent)
	if len(contents) == 0 || !strings.Contains(contents[0].Text, "name: octo-messaging") {
		t.Errorf("resource content should be the SKILL.md body")
	}

	if _, err := s.readResource("octo://skills/octo-matter/SKILL.md"); err == nil {
		t.Errorf("reading a disabled skill resource must fail")
	}
	if _, err := s.readResource("file:///etc/passwd"); err == nil {
		t.Errorf("a non-skill URI must be refused")
	}
}

func TestInitialize_NegotiatesResourcesCapability(t *testing.T) {
	s := newTestServer(t)
	params := json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"resources":{}}}`)
	resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: params})
	if !has || resp.Error != nil {
		t.Fatalf("initialize failed: %+v", resp)
	}
	if !s.clientResources {
		t.Errorf("server should record that the client supports resources")
	}
	var result map[string]any
	_ = json.Unmarshal(resp.Result, &result)
	caps, _ := result["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("server must advertise tools capability")
	}
	if _, ok := caps["resources"]; !ok {
		t.Errorf("server must advertise resources capability")
	}
}
