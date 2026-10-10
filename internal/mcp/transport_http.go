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
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// defaultHTTPRequestTimeout bounds how long a single HTTP JSON-RPC request may
// run, so no request outlives the operator's expectations (a slow or stuck
// handler, or a client that disconnects). It is generous enough for a normal
// call_op — including a bounded --page-all walk — and overridable via
// OCTO_MCP_HTTP_REQUEST_TIMEOUT for long-running deployments.
const defaultHTTPRequestTimeout = 120 * time.Second

// maxHTTPBodyBytes caps a single request body (parity with the stdio message cap).
const maxHTTPBodyBytes = maxMCPMessageBytes

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
	reg                 *registry.Registry
	mapping             *Mapping
	build               RootBuilder
	baseTrusted         TrustedContext
	facade              facadeMode
	baseGlobals         cmdutil.GlobalOptions
	allowedOrigins      []string // exact-match allowlist; empty => loopback origins only
	uploadRoot          string   // OCTO_MCP_UPLOAD_ROOT; confines multipart file_path
	trustContextHeaders bool
	version             string
	requestTimeout      time.Duration
	uploadSlots         chan struct{} // shared admission bound for multipart assembly and execution
	readBudget          readBudget    // bounds admitted input bytes through response completion
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
func NewHTTPHandler(build RootBuilder, baseTrusted TrustedContext, base cmdutil.GlobalOptions, facades ...facadeMode) (*HTTPHandler, error) { //nolint:gocritic // copy operator globals for immutable per-request snapshots
	if base.Profile != "" || base.BotID != "" {
		return nil, output.ErrValidation("stored credential selectors --profile and --bot-id are not supported by MCP HTTP", "set OCTO_API_BASE_URL explicitly for routing and send Authorization: Bearer <token> on each request")
	}
	if err := validateHTTPUploadRoot(os.Getenv("OCTO_MCP_UPLOAD_ROOT")); err != nil {
		return nil, output.ErrValidation(err.Error(), "set OCTO_MCP_UPLOAD_ROOT to a dedicated staging directory")
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
	return &HTTPHandler{
		reg: reg, mapping: mapping, build: build, baseTrusted: baseTrusted, baseGlobals: base,
		allowedOrigins: parseAllowedOrigins(os.Getenv("OCTO_MCP_ALLOWED_ORIGINS")),
		uploadRoot:     os.Getenv("OCTO_MCP_UPLOAD_ROOT"),
		requestTimeout: httpRequestTimeoutFromEnv(),
		facade:         facade,
		uploadSlots:    make(chan struct{}, 2),
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
	req, releaseRequest, ok := h.decodeHTTPRPCRequest(w, r)
	if !ok {
		return
	}
	defer releaseRequest()

	srv := h.connectionServer(r)
	// Bound the request: no single JSON-RPC call may outlive the request
	// timeout (and a client disconnect cancels it), so a slow or stuck handler
	// cannot hold a goroutine indefinitely. The deadline flows into call_op's
	// in-process execution and its outbound HTTP call.
	ctx, cancel := context.WithTimeout(r.Context(), h.requestTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	// The context bounds backend execution; the socket deadline also bounds
	// response writes when the peer stops reading. Recorder-only test writers
	// may not support deadlines; net/http production writers do.
	_ = http.NewResponseController(w).SetWriteDeadline(deadline) //nolint:errcheck // unsupported synthetic writers have no socket to block
	resp, has := srv.Dispatch(ctx, req)
	w.Header().Set("Content-Type", "application/json")
	if !has {
		// A notification: acknowledge with 202 and no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck // response write failure means the client disconnected; no response can replace it
}

// decodeHTTPRPCRequest admits, reads, and parses a single bounded request.
// On success the caller owns the reservation until dispatch and response
// writing complete. Errors release here, including chunked promotion failures.
func (h *HTTPHandler) decodeHTTPRPCRequest(w http.ResponseWriter, r *http.Request) (rpcRequest, func(), bool) {
	if r.ContentLength > maxHTTPBodyBytes {
		http.Error(w, "request body exceeds 8 MiB limit", http.StatusRequestEntityTooLarge)
		return rpcRequest{}, nil, false
	}
	// Charge bytes before reading, with a separate reserve for small requests.
	// A pair of slow body readers must not deny discovery or health requests.
	var releaseRead func()
	var admitted bool
	if r.ContentLength < 0 {
		releaseRead, admitted = h.readBudget.reserveUnknownPrefix()
	} else {
		releaseRead, admitted = h.readBudget.reserve(r.ContentLength)
	}
	if !admitted {
		w.Header().Set("Retry-After", "1")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		writeHTTPError(w, nil, -32000, "request body read budget exhausted; retry later")
		return rpcRequest{}, nil, false
	}
	defer func() {
		if releaseRead != nil {
			releaseRead()
		}
	}()
	reader := http.MaxBytesReader(w, r.Body, maxHTTPBodyBytes)
	var body []byte
	var err error
	if r.ContentLength < 0 {
		// Unknown-size requests start in the small pool. After 64 KiB they
		// transfer to the large pool before reading the remainder. This keeps
		// a chunked ping available while large uploads occupy their budget.
		body, err = io.ReadAll(io.LimitReader(reader, smallReadThreshold+1))
		if err == nil && len(body) > smallReadThreshold {
			largeRelease, ok := h.readBudget.reserve(-1)
			if !ok {
				w.Header().Set("Retry-After", "1")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				writeHTTPError(w, nil, -32000, "request body read budget exhausted; retry later")
				return rpcRequest{}, nil, false
			}
			releaseRead()
			releaseRead = largeRelease
			body, err = readBoundedBody(reader, body, maxHTTPBodyBytes)
		}
	} else {
		body, err = readBoundedBody(reader, nil, int(r.ContentLength))
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body exceeds 8 MiB limit", http.StatusRequestEntityTooLarge)
			return rpcRequest{}, nil, false
		}
		writeHTTPError(w, nil, codeParseError, "read body: "+err.Error())
		return rpcRequest{}, nil, false
	}
	if r.ContentLength >= 0 && int64(len(body)) != r.ContentLength {
		writeHTTPError(w, nil, codeParseError, "request body shorter than Content-Length")
		return rpcRequest{}, nil, false
	}
	req, code, err := decodeRPCRequest(body)
	if err != nil {
		prefix := "invalid JSON-RPC request: "
		if code == codeParseError {
			prefix = "parse error: "
		}
		writeHTTPError(w, nil, code, prefix+err.Error())
		return rpcRequest{}, nil, false
	}

	releaseRequest := releaseRead
	releaseRead = nil // transfer ownership to ServeHTTP
	return req, releaseRequest, true
}

// readBoundedBody keeps one fixed-capacity body for declared lengths and for
// chunked bodies after they move to the large pool. io.ReadAll can temporarily
// retain both growing chunks and a final copy, defeating the byte budget.
func readBoundedBody(reader io.Reader, prefix []byte, capacity int) ([]byte, error) {
	body := make([]byte, capacity)
	n := copy(body, prefix)
	var probe [1]byte
	emptyReads := 0
	for {
		target := body[n:]
		if n == len(body) {
			target = probe[:]
		}
		read, err := reader.Read(target)
		if n == len(body) && read > 0 {
			return nil, errors.New("request body exceeds declared Content-Length")
		}
		n += read
		if err == io.EOF {
			return body[:n], nil
		}
		if err != nil {
			return nil, err
		}
		if read == 0 {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		} else {
			emptyReads = 0
		}
	}
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
	if h.trustContextHeaders {
		// A caller must not complete one half of an operator-pinned channel
		// binding. Otherwise it can select which derived group/thread field is
		// forced on a write. Headers can supply a channel only when the operator
		// has not pinned any channel component.
		channelFromHeaders := tc.ChannelID == "" && tc.ChannelType == "" && tc.ThreadID == ""
		if tc.SpaceID == "" {
			tc.SpaceID = r.Header.Get(headerSpaceID)
		}
		if channelFromHeaders {
			tc.ChannelID = r.Header.Get(headerChannelID)
			tc.ChannelType = r.Header.Get(headerChannelType)
		}
		if tc.OnBehalfOf == "" {
			tc.OnBehalfOf = r.Header.Get(headerOnBehalfOf)
		}
		// A type-5 channel binding already fixes the thread short ID. A
		// request header must not redirect HTML writes to another thread.
		_, pinnedThread := tc.threadChannelParts()
		if channelFromHeaders && tc.ThreadID == "" && pinnedThread == "" {
			tc.ThreadID = r.Header.Get("X-Octo-Thread-Id")
		}
		if tc.WorkspaceID == "" {
			tc.WorkspaceID = r.Header.Get("X-Workspace-ID")
		}
		if tc.PrincipalSpaceID == "" {
			tc.PrincipalSpaceID = r.Header.Get("X-Octo-Principal-Space-Id")
		}
	}
	return &Server{
		reg:             h.reg,
		mapping:         h.mapping,
		build:           h.build,
		version:         h.version,
		trusted:         tc,
		credentialToken: bearerToken(r),
		httpMode:        true,
		uploadRoot:      h.uploadRoot,
		uploadSlots:     h.uploadSlots,
		baseGlobals:     h.baseGlobals,
		protocolVersion: defaultProtocolVersion,
		clientResources: true, // stateless HTTP exposes resources/read on every POST; no negotiation persists
		facade:          h.facade,
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
	_ = json.NewEncoder(w).Encode(newErrorResponse(id, code, msg)) //nolint:errcheck // response write failure means the client disconnected; no response can replace it
}

// WithContextHeaders enables headers only behind an operator-approved proxy.
func (h *HTTPHandler) WithContextHeaders(trust bool) *HTTPHandler {
	h.trustContextHeaders = trust
	return h
}

// WithVersion sets the artifact version returned by initialize.
func (h *HTTPHandler) WithVersion(version string) *HTTPHandler { h.version = version; return h }
