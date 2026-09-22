package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// defaultHTTPRequestTimeout bounds how long a single HTTP JSON-RPC request may
// run, so no request outlives the operator's expectations (a slow or stuck
// handler, or a client that disconnects). It is generous enough for a normal
// call_op — including a bounded --page-all walk — and overridable via
// OCTO_MCP_HTTP_REQUEST_TIMEOUT for long-running deployments.
const defaultHTTPRequestTimeout = 120 * time.Second

// maxHTTPBodyBytes caps a single request body (parity with the stdio message cap).
const maxHTTPBodyBytes = 8 << 20

// HTTPHandler is the HTTP transport: JSON-RPC request/response over POST. It is
// NOT (yet) full MCP streamable HTTP — there is no SSE stream (GET) and no
// server-managed session; each POST is one request and one response. It is the
// intended production shape because each request carries its own bearer
// (Authorization) and its own trusted headers, so the server injects a
// per-connection credential and per-connection over-privilege-protection values — a
// client on connection A can never act in connection B's space / channel /
// identity. reg and mapping are shared read-only across requests; the mutable
// per-connection state lives on the short-lived per-request Server.
type HTTPHandler struct {
	reg            *registry.Registry
	mapping        *Mapping
	build          RootBuilder
	baseTrusted    TrustedContext
	baseGlobals    cmdutil.GlobalOptions
	allowedOrigins []string // exact-match allowlist; empty => loopback origins only
	uploadRoot     string   // OCTO_MCP_UPLOAD_ROOT; confines multipart file_path
	requestTimeout time.Duration
}

// Trusted-context request headers. Space also flows onto the credential so
// X-Space-Id is set exactly as the CLI already does it.
const (
	headerSpaceID     = "X-Space-Id"
	headerChannelID   = "X-Octo-Channel-Id"
	headerChannelType = "X-Octo-Channel-Type"
	headerOnBehalfOf  = "X-Octo-On-Behalf-Of"
)

// NewHTTPHandler builds the HTTP handler. baseTrusted supplies operator-forced
// values (which win over request headers); base carries the operator's
// serve-time credential-selector / limit globals into each call. Origin
// validation (anti DNS-rebinding) reads an exact-match allowlist from
// OCTO_MCP_ALLOWED_ORIGINS (comma-separated); when unset, only loopback origins
// are accepted and a request with no Origin header (typical non-browser MCP
// client) is allowed. Multipart uploads are confined to OCTO_MCP_UPLOAD_ROOT.
func NewHTTPHandler(build RootBuilder, baseTrusted TrustedContext, base cmdutil.GlobalOptions) (*HTTPHandler, error) {
	reg, err := registry.New()
	if err != nil {
		return nil, err
	}
	mapping, err := BuildMapping(reg)
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{
		reg: reg, mapping: mapping, build: build, baseTrusted: baseTrusted, baseGlobals: base,
		allowedOrigins: parseAllowedOrigins(os.Getenv("OCTO_MCP_ALLOWED_ORIGINS")),
		uploadRoot:     os.Getenv("OCTO_MCP_UPLOAD_ROOT"),
		requestTimeout: httpRequestTimeoutFromEnv(),
	}, nil
}

// httpRequestTimeoutFromEnv reads OCTO_MCP_HTTP_REQUEST_TIMEOUT (a Go duration)
// or falls back to the default. A non-positive or malformed value uses the
// default rather than disabling the bound.
func httpRequestTimeoutFromEnv() time.Duration {
	if v := strings.TrimSpace(os.Getenv("OCTO_MCP_HTTP_REQUEST_TIMEOUT")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultHTTPRequestTimeout
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
	// Read one byte past the cap so an over-limit body is rejected outright
	// rather than silently truncated to a valid-JSON prefix.
	body, err := io.ReadAll(io.LimitReader(r.Body, maxHTTPBodyBytes+1))
	if err != nil {
		writeHTTPError(w, nil, codeParseError, "read body: "+err.Error())
		return
	}
	if len(body) > maxHTTPBodyBytes {
		http.Error(w, "request body exceeds 8 MiB limit", http.StatusRequestEntityTooLarge)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeHTTPError(w, nil, codeParseError, "parse error: "+err.Error())
		return
	}

	srv := h.connectionServer(r)
	// Bound the request: no single JSON-RPC call may outlive the request
	// timeout (and a client disconnect cancels it), so a slow or stuck handler
	// cannot hold a goroutine indefinitely. The deadline flows into call_op's
	// in-process execution and its outbound HTTP call.
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()
	resp, has := srv.Dispatch(ctx, req)
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
//
// Precedence: operator-configured (baseTrusted) values WIN. A request
// X-Octo-* header may only fill a field the operator did NOT force. Header
// trust is a gateway-mode convenience, not authentication — the bearer
// credential and Origin validation are the guards; deploy behind a
// loopback bind or a TLS-terminating reverse proxy that sets these headers.
func (h *HTTPHandler) connectionServer(r *http.Request) *Server {
	tc := h.baseTrusted
	if tc.SpaceID == "" {
		tc.SpaceID = r.Header.Get(headerSpaceID)
	}
	if tc.ChannelID == "" {
		tc.ChannelID = r.Header.Get(headerChannelID)
	}
	if tc.ChannelType == "" {
		tc.ChannelType = r.Header.Get(headerChannelType)
	}
	if tc.OnBehalfOf == "" {
		tc.OnBehalfOf = r.Header.Get(headerOnBehalfOf)
	}
	return &Server{
		reg:             h.reg,
		mapping:         h.mapping,
		build:           h.build,
		trusted:         tc,
		credentialToken: bearerToken(r),
		httpMode:        true,
		uploadRoot:      h.uploadRoot,
		baseGlobals:     h.baseGlobals,
		protocolVersion: defaultProtocolVersion,
		clientResources: true, // HTTP clients can read the returned resource URIs
	}
}

// bearerToken extracts the Authorization bearer token, parsing the scheme
// case-insensitively per RFC 7235 ("Bearer" / "bearer" / "BEARER").
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const scheme = "bearer "
	if len(auth) >= len(scheme) && strings.EqualFold(auth[:len(scheme)], scheme) {
		return strings.TrimSpace(auth[len(scheme):])
	}
	return ""
}

func writeHTTPError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(newErrorResponse(id, code, msg))
}
