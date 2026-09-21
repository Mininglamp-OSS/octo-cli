package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStdioTransport_InitializeToolsListRoundTrip is the stdio smoke test: feed
// newline-delimited JSON-RPC in, read newline-delimited responses out.
func TestStdioTransport_InitializeToolsListRoundTrip(t *testing.T) {
	s := newTestServer(t)
	in := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"resources":{}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_ops","arguments":{"domain":"message"}}}`,
	}, "\n") + "\n")
	var out bytes.Buffer

	if err := s.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	lines := nonEmptyLines(out.String())
	// initialize + tools/list + tools/call = 3 responses; the notification gets none.
	if len(lines) != 3 {
		t.Fatalf("got %d response lines, want 3:\n%s", len(lines), out.String())
	}
	var initResp rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil || initResp.Error != nil {
		t.Fatalf("initialize response bad: %v (%s)", err, lines[0])
	}
	// tools/call result must carry search results as text content.
	var callResp rpcResponse
	_ = json.Unmarshal([]byte(lines[2]), &callResp)
	var res toolResult
	_ = json.Unmarshal(callResp.Result, &res)
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "message.send") {
		t.Errorf("search_ops over stdio should list message.send, got %s", res.Content[0].Text)
	}
}

func TestStdioTransport_ParseErrorDoesNotKillLoop(t *testing.T) {
	s := newTestServer(t)
	in := strings.NewReader("not json\n" + `{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("expected a parse-error response and a ping response, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "parse error") {
		t.Errorf("first response should be a parse error, got %s", lines[0])
	}
	if !strings.Contains(lines[1], `"id":9`) {
		t.Errorf("loop must continue after a parse error, got %s", lines[1])
	}
}

// TestHTTPTransport_PerConnectionCredentialAndTrustedHeaders is the HTTP
// (JSON-RPC over POST) smoke test plus a multi-tenant isolation proof: two POSTs with different
// bearers and different X-Space-Id headers each reach the backend with their
// own credential and space.
func TestHTTPTransport_PerConnectionCredentialAndTrustedHeaders(t *testing.T) {
	be := &backend{}
	backendSrv := be.server(t)

	h, err := NewHTTPHandler(testRoot, TrustedContext{}, facadeThree)
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	// Point call_op's factory at the fake backend regardless of connection.
	// The handler builds a per-request server; wrap it so each request's
	// connection server uses the fake backend but keeps its own trusted context.
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reuse the handler's routing but override factory to the fake backend.
		// We do this by delegating to a per-request server we build here.
		srv := h.connectionServer(r)
		srv.factoryFn = fakeBackendFactory(backendSrv.URL, srv.credentialToken)
		body, _ := readAll(r)
		var req rpcRequest
		_ = json.Unmarshal(body, &req)
		resp, has := srv.Dispatch(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		if !has {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(mcpSrv.Close)

	// tools/list works with no auth.
	if code, _ := postJSON(t, mcpSrv.URL, "", "", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); code != http.StatusOK {
		t.Fatalf("tools/list over HTTP returned %d", code)
	}

	// call_op carries the connection bearer + trusted space to the backend.
	_, body := postJSON(t, mcpSrv.URL, "bf_alice", "space-alice",
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"message.send","arguments":{"channel_id":"c1","channel_type":1,"payload":{"text":"hi"}}}}}`)
	if !envelopeOKFromRPC(t, body) {
		t.Fatalf("call_op over HTTP did not succeed: %s", body)
	}
	if !strings.HasPrefix(be.auth, "Bearer bf_alice") {
		t.Errorf("backend saw Authorization %q, want the per-connection bearer", be.auth)
	}
	if be.space != "space-alice" {
		t.Errorf("backend saw X-Space-Id %q, want space-alice", be.space)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		origin  string
		allowed []string
		want    bool
	}{
		{"http://localhost:3000", nil, true},
		{"http://127.0.0.1", nil, true},
		{"http://[::1]:9000", nil, true},
		{"http://evil.example", nil, false},                                    // remote origin, no allowlist → rejected
		{"https://app.example.com", []string{"https://app.example.com"}, true}, // exact allowlist match
		{"https://evil.example", []string{"https://app.example.com"}, false},   // not in allowlist
		{"::not a url", nil, false},                                            // malformed → rejected
	}
	for _, c := range cases {
		if got := originAllowed(c.origin, c.allowed); got != c.want {
			t.Errorf("originAllowed(%q, %v) = %v, want %v", c.origin, c.allowed, got, c.want)
		}
	}
}

func TestHTTPTransport_RejectsCrossOrigin(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, facadeThree)
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	h.allowedOrigins = nil // default: loopback-only
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Cross-origin browser request is refused before any dispatch.
	code, _ := postJSONWithOrigin(t, srv.URL, "http://evil.example", `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if code != http.StatusForbidden {
		t.Errorf("cross-origin request = %d, want 403", code)
	}
	// A non-browser client (no Origin header) is allowed.
	if code, _ := postJSON(t, srv.URL, "", "", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`); code != http.StatusOK {
		t.Errorf("no-Origin request = %d, want 200", code)
	}
	// A loopback Origin is allowed.
	if code, _ := postJSONWithOrigin(t, srv.URL, "http://localhost:1234", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`); code != http.StatusOK {
		t.Errorf("loopback-origin request = %d, want 200", code)
	}
}

func TestStdioTransport_OversizedMessageRejectedLoopSurvives(t *testing.T) {
	s := newTestServer(t)
	big := strings.Repeat("a", maxStdioMessageBytes+16)
	in := strings.NewReader(big + "\n" + `{"jsonrpc":"2.0","id":7,"method":"ping"}` + "\n")
	var out bytes.Buffer
	if err := s.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}
	lines := nonEmptyLines(out.String())
	if len(lines) != 2 {
		t.Fatalf("want an oversized-error response and a ping response, got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[0], "exceeds 8 MiB") {
		t.Errorf("first response should report the size limit, got %s", lines[0])
	}
	if !strings.Contains(lines[1], `"id":7`) {
		t.Errorf("loop must continue after an oversized message, got %s", lines[1])
	}
}

// envelopeOKFromRPC decodes a JSON-RPC response body, extracts the tool
// result's text content, and reports whether that envelope's ok is true.
func envelopeOKFromRPC(t *testing.T, body string) bool {
	t.Helper()
	var resp rpcResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Error != nil {
		return false
	}
	var res toolResult
	if err := json.Unmarshal(resp.Result, &res); err != nil || len(res.Content) == 0 {
		return false
	}
	return envelopeOK([]byte(res.Content[0].Text))
}

func readAll(r *http.Request) ([]byte, error) {
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

func postJSON(t *testing.T, url, bearer, space, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if space != "" {
		req.Header.Set(headerSpaceID, space)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}

func postJSONWithOrigin(t *testing.T, url, origin, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	out := new(bytes.Buffer)
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.String()
}
