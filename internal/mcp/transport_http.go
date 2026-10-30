package mcp

import (
	"context"
	"encoding/json"
	"errors"
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
// (Authorization), while trusted context comes only from operator config.
// Client context headers are refused before dispatch. Credentials are isolated
// per request. reg and mapping are shared read-only across requests; the mutable
// per-connection state lives on the short-lived per-request Server.
type HTTPHandler struct {
	admission       *Admission
	ownsAdmission   bool
	apiOrigin       string
	secretDownloads *SecretDownloads
	reg             *registry.Registry
	mapping         *Mapping
	build           RootBuilder
	baseTrusted     TrustedContext
	facade          facadeMode
	baseGlobals     cmdutil.GlobalOptions
	allowedOrigins  []string // exact-match allowlist; empty => loopback origins only
	uploadRoot      string   // OCTO_MCP_UPLOAD_ROOT; confines multipart file_path
	requestTimeout  time.Duration
}

// Reserved context headers: callers cannot supply trusted provenance. An
// outbound X-Space-Id is derived only from the operator's TrustedContext.
const (
	headerSpaceID     = "X-Space-Id"
	headerChannelID   = "X-Octo-Channel-Id"
	headerChannelType = "X-Octo-Channel-Type"
	headerOnBehalfOf  = "X-Octo-On-Behalf-Of"
)

// NewHTTPHandler builds the HTTP handler. baseTrusted supplies operator-forced
// values (the only source of trusted context); base carries the operator's
// serve-time credential-selector / limit globals into each call. Origin
// validation (anti DNS-rebinding) reads an exact-match allowlist from
// OCTO_MCP_ALLOWED_ORIGINS (comma-separated); when unset, only loopback origins
// are accepted and a request with no Origin header (typical non-browser MCP
// client) is allowed. Multipart uploads are confined to OCTO_MCP_UPLOAD_ROOT.
func NewHTTPHandler(build RootBuilder, baseTrusted TrustedContext, base cmdutil.GlobalOptions, facades ...facadeMode) (*HTTPHandler, error) { //nolint:gocritic // copy operator globals for immutable per-request snapshots
	if baseTrusted.OnBehalfOf != "" {
		return nil, errors.New("OBO_FORBIDDEN")
	}
	if base.DryRun {
		return nil, errors.New("MCP_OFFLINE_POLICY_UNAVAILABLE")
	}
	a, err := NewAdmission(os.Getenv("OCTO_MCP_REQUEST_LOG_DIR"), nil)
	if err != nil {
		return nil, err
	}
	h, err := NewHTTPHandlerWithAdmission(build, baseTrusted, base, a, facades...)
	if err != nil {
		_ = a.Close() //nolint:errcheck // release startup resources while preserving the constructor failure
		return nil, err
	}
	h.ownsAdmission = true
	return h, nil
}

// NewHTTPHandlerWithAdmission binds an explicit operator policy/store before
// constructing a listener-capable handler. The caller owns admission shutdown.
func NewHTTPHandlerWithAdmission(build RootBuilder, baseTrusted TrustedContext, base cmdutil.GlobalOptions, a *Admission, facades ...facadeMode) (*HTTPHandler, error) { //nolint:gocritic // copy operator globals for immutable per-request snapshots
	if baseTrusted.OnBehalfOf != "" {
		return nil, errors.New("OBO_FORBIDDEN")
	}
	if base.DryRun {
		return nil, errors.New("MCP_OFFLINE_POLICY_UNAVAILABLE")
	}
	if a == nil || a.log == nil || a.stopped.Load() {
		return nil, errors.New("REQUEST_LOG_STARTUP_FAILED")
	}
	facade := facadeThree
	if len(facades) > 0 {
		facade = facades[0]
	}
	reg, err := registry.New()
	if err != nil {
		return nil, err
	}
	mapping, err := BuildMapping(reg)
	if err != nil {
		return nil, err
	}
	f := cmdutil.NewDefaultFactory()
	f.Globals.Profile = base.Profile
	cfg, err := f.Config()
	if err != nil {
		return nil, errors.New("MCP_ORIGIN_UNAVAILABLE")
	}
	var downloads *SecretDownloads
	if !base.DryRun {
		downloads = SecretDownloadsFromEnv()
	}
	return &HTTPHandler{
		admission:       a,
		apiOrigin:       cfg.APIBaseURL,
		secretDownloads: downloads,
		reg:             reg, mapping: mapping, build: build, baseTrusted: baseTrusted, baseGlobals: base,
		allowedOrigins: parseAllowedOrigins(os.Getenv("OCTO_MCP_ALLOWED_ORIGINS")),
		uploadRoot:     os.Getenv("OCTO_MCP_UPLOAD_ROOT"),
		requestTimeout: httpRequestTimeoutFromEnv(),
		facade:         facade,
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

// Close releases the optional Redis pool.
func (h *HTTPHandler) Close() {
	h.secretDownloads.Close()
	if h.ownsAdmission {
		_ = h.admission.Close() //nolint:errcheck // compatibility cleanup waits for the owned writer; explicit-admission callers receive shutdown errors from Admission.Close
	}
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { //nolint:gocyclo // preserve ordered HTTP boundary checks before authenticated dispatch
	if strings.HasPrefix(r.URL.Path, secretDownloadPath) {
		h.secretDownloads.serveDownload(w, r)
		return
	}
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
	// A valid bearer authorizes backend requests, not arbitrary trusted scope
	// or principal injection. Refuse even empty or duplicate context headers,
	// including when operator values are fixed, rather than silently accepting
	// an obsolete gateway-header contract. Do not disclose any context values.
	for name := range r.Header {
		if strings.EqualFold(name, headerSpaceID) || strings.EqualFold(name, headerChannelID) ||
			strings.EqualFold(name, headerChannelType) || strings.EqualFold(name, headerOnBehalfOf) {
			http.Error(w, "context headers are not accepted; configure trusted context on the server", http.StatusForbidden)
			return
		}
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
	if err := decodeRPCRequest(body, &req); err != nil {
		writeHTTPError(w, nil, codeParseError, "parse error: "+err.Error())
		return
	}

	srv := h.connectionServer(r)
	// Bound the request: no single JSON-RPC call may outlive the request
	// timeout (and a client disconnect cancels it), so a slow or stuck handler
	// cannot hold a goroutine indefinitely. The deadline flows into call_op's
	// in-process execution and its outbound HTTP call.
	timeout := h.requestTimeout
	if timeout <= 0 || timeout > 120*time.Second {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	resp, has := srv.Dispatch(ctx, req)
	w.Header().Set("Content-Type", "application/json")
	if !has {
		// A notification: acknowledge with 202 and no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck // response write failure means the client disconnected; no response can replace it
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

// connectionServer builds the per-request Server with the request bearer and
// an immutable operator-configured trusted context. Headers never fill unset
// fields, even if an embedding handler calls this helper without ServeHTTP.
func (h *HTTPHandler) connectionServer(r *http.Request) *Server {
	return &Server{
		admission:       h.admission,
		apiOrigin:       h.apiOrigin,
		secretDownloads: h.secretDownloads,
		reg:             h.reg,
		mapping:         h.mapping,
		build:           h.build,
		trusted:         h.baseTrusted,
		credentialToken: bearerToken(r),
		httpMode:        true,
		uploadRoot:      h.uploadRoot,
		baseGlobals:     h.baseGlobals,
		protocolVersion: defaultProtocolVersion,
		clientResources: true, // HTTP clients can read the returned resource URIs
		facade:          h.facade,
	}
}

// bearerToken extracts the Authorization bearer token, parsing the scheme
// case-insensitively per RFC 7235 ("Bearer" / "bearer" / "BEARER").
func bearerToken(r *http.Request) string {
	var values []string
	for key, v := range r.Header {
		if strings.EqualFold(key, "Authorization") {
			values = append(values, v...)
		}
	}
	if len(values) != 1 {
		return ""
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" || strings.ContainsAny(parts[1], ",\t\r\n ") {
		return ""
	}
	return parts[1]
}

func writeHTTPError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(newErrorResponse(id, code, msg)) //nolint:errcheck // response write failure means the client disconnected; no response can replace it
}
