package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// maxHTTPBodyBytes caps a single request body (parity with the stdio message cap).
const maxHTTPBodyBytes = 8 << 20

// HTTPHandler is the HTTP transport: JSON-RPC request/response over POST. It is
// NOT (yet) full MCP streamable HTTP — there is no SSE stream (GET) and no
// server-managed session; each POST is one request and one response. It is the
// intended production shape because each request carries its own bearer
// (Authorization) and its own trusted headers, so the server injects a
// per-connection credential and per-connection over-privilege防护 values — a
// client on connection A can never act in connection B's space / channel /
// identity. reg and mapping are shared read-only across requests; the mutable
// per-connection state lives on the short-lived per-request Server.
type HTTPHandler struct {
	reg            *registry.Registry
	mapping        *Mapping
	build          RootBuilder
	baseTrusted    TrustedContext
	allowedOrigins []string // exact-match allowlist; empty => loopback origins only
}

// Trusted-context request headers. Space also flows onto the credential so
// X-Space-Id is set exactly as the CLI already does it.
const (
	headerSpaceID     = "X-Space-Id"
	headerChannelID   = "X-Octo-Channel-Id"
	headerChannelType = "X-Octo-Channel-Type"
	headerOnBehalfOf  = "X-Octo-On-Behalf-Of"
)

// NewHTTPHandler builds the HTTP handler. baseTrusted supplies defaults a
// request header may override. Origin validation (anti DNS-rebinding, MCP
// guidance) reads an exact-match allowlist from OCTO_MCP_ALLOWED_ORIGINS
// (comma-separated); when unset, only loopback origins are accepted and a
// request with no Origin header (typical non-browser MCP client) is allowed.
func NewHTTPHandler(build RootBuilder, baseTrusted TrustedContext) (*HTTPHandler, error) {
	reg, err := registry.New()
	if err != nil {
		return nil, err
	}
	mapping, err := BuildMapping(reg)
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{
		reg: reg, mapping: mapping, build: build, baseTrusted: baseTrusted,
		allowedOrigins: parseAllowedOrigins(os.Getenv("OCTO_MCP_ALLOWED_ORIGINS")),
	}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Origin validation first: reject a cross-origin browser request before any
	// work, so a page on another origin cannot drive this server via the user's
	// loopback binding (DNS-rebinding / CSRF class).
	if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, h.allowedOrigins) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		// No SSE stream is served yet; this transport is POST request/response
		// only. A GET would be the SSE channel in a fuller implementation.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes))
	if err != nil {
		writeHTTPError(w, nil, codeParseError, "read body: "+err.Error())
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, nil, codeParseError, "parse error: "+err.Error())
		return
	}

	srv := h.connectionServer(r)
	resp, has := srv.Dispatch(r.Context(), req)
	w.Header().Set("Content-Type", "application/json")
	if !has {
		// A notification: acknowledge with 202 and no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = json.NewEncoder(w).Encode(resp)
}

// parseAllowedOrigins splits the comma-separated env allowlist, trimming blanks.
func parseAllowedOrigins(raw string) []string {
	var out []string
	for _, o := range strings.Split(raw, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// originAllowed reports whether an Origin header value is permitted: it matches
// the exact-match allowlist, or (when the allowlist is empty) it is a loopback
// origin. A malformed Origin is rejected.
func originAllowed(origin string, allowed []string) bool {
	for _, a := range allowed {
		if origin == a {
			return true
		}
	}
	if len(allowed) > 0 {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// connectionServer builds the per-request Server with connection-scoped
// credential and trusted context from the request headers.
func (h *HTTPHandler) connectionServer(r *http.Request) *Server {
	tc := h.baseTrusted
	if v := r.Header.Get(headerSpaceID); v != "" {
		tc.SpaceID = v
	}
	if v := r.Header.Get(headerChannelID); v != "" {
		tc.ChannelID = v
	}
	if v := r.Header.Get(headerChannelType); v != "" {
		tc.ChannelType = v
	}
	if v := r.Header.Get(headerOnBehalfOf); v != "" {
		tc.OnBehalfOf = v
	}
	return &Server{
		reg:             h.reg,
		mapping:         h.mapping,
		build:           h.build,
		trusted:         tc,
		credentialToken: bearerToken(r),
		protocolVersion: defaultProtocolVersion,
		clientResources: true, // HTTP clients can read the returned resource URIs
	}
}

func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const p = "Bearer "
	if strings.HasPrefix(auth, p) {
		return strings.TrimSpace(auth[len(p):])
	}
	return ""
}

func writeHTTPError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(newErrorResponse(id, code, msg))
}
