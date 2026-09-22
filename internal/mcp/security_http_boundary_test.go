package mcp

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// --- HTTP must fail closed on credentials (production seam, factoryFn == nil) ---

// httpModeServer builds a Server on the real makeFactory path (no factoryFn)
// with httpMode set and the given connection bearer, mirroring what
// connectionServer produces for an HTTP request.
func httpModeServer(t *testing.T, token string) *Server {
	t.Helper()
	s := newTestServer(t)
	s.httpMode = true
	s.credentialToken = token
	return s
}

func TestHTTP_MissingBearerCannotExecute_RealMakeFactory(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	// A server credential IS present in the env; the HTTP path must NOT inherit it.
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	t.Setenv("OCTO_TOKEN", "bf_server_ambient")
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())

	s := httpModeServer(t, "") // no/empty bearer

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("HTTP call with no bearer must fail closed, got success: %s", res.Content[0].Text)
	}
	env := toolText(t, res)
	errObj, _ := env["error"].(map[string]any)
	if errObj == nil || errObj["type"] != "auth_error" {
		t.Errorf("expected an auth_error envelope, got %v", env)
	}
	if be.path != "" {
		t.Errorf("no request may reach the backend without a connection bearer, path=%q", be.path)
	}
}

func TestHTTP_MalformedAuthCannotExecute_RealMakeFactory(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	t.Setenv("OCTO_TOKEN", "bf_server_ambient")
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())

	// Malformed Authorization (no Bearer scheme) yields an empty connection
	// token via bearerToken; the connection therefore fails closed.
	s := httpModeServer(t, "")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError || be.path != "" {
		t.Errorf("malformed auth must fail closed with no backend hit; isError=%v path=%q", res.IsError, be.path)
	}
}

func TestHTTP_ValidBearerUsesConnectionCredential_RealMakeFactory(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	t.Setenv("OCTO_TOKEN", "bf_server_ambient") // must be ignored in HTTP mode
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())

	s := httpModeServer(t, "bf_alice")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if res.IsError {
		t.Fatalf("valid bearer should execute: %s", res.Content[0].Text)
	}
	if !strings.HasPrefix(be.auth, "Bearer bf_alice") {
		t.Errorf("backend must see the connection bearer, not the server env token; got %q", be.auth)
	}
}

// --- trusted-context precedence + case-insensitive Bearer ---

func TestConnectionServer_OperatorForcedWinsOverHeader(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{SpaceID: "op-space", OnBehalfOf: "op-obo"}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set(headerSpaceID, "hdr-space")
	req.Header.Set(headerOnBehalfOf, "hdr-obo")
	req.Header.Set(headerChannelID, "hdr-chan") // this field is NOT operator-forced

	srv := h.connectionServer(req)
	if !srv.httpMode {
		t.Error("connectionServer must set httpMode so call_op fails closed on the HTTP transport")
	}
	if srv.trusted.SpaceID != "op-space" {
		t.Errorf("operator-forced SpaceID must win, got %q", srv.trusted.SpaceID)
	}
	if srv.trusted.OnBehalfOf != "op-obo" {
		t.Errorf("operator-forced OnBehalfOf must win, got %q", srv.trusted.OnBehalfOf)
	}
	if srv.trusted.ChannelID != "hdr-chan" {
		t.Errorf("unforced field may be filled by header, got %q", srv.trusted.ChannelID)
	}
}

func TestBearerToken_CaseInsensitiveScheme(t *testing.T) {
	cases := map[string]string{
		"Bearer tok1": "tok1",
		"bearer tok2": "tok2",
		"BEARER tok3": "tok3",
		"Basic tok4":  "",
		"tok5":        "",
		"":            "",
	}
	for auth, want := range cases {
		req := httptest.NewRequest("POST", "/", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		if got := bearerToken(req); got != want {
			t.Errorf("bearerToken(%q) = %q, want %q", auth, got, want)
		}
	}
}
