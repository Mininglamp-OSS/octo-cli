package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// HTTPHandler is the streamable-HTTP transport: the production form (design
// §5.5). Each request carries its own bearer (Authorization) and its own
// trusted headers, so the server injects a per-connection credential and
// per-connection over-privilege防护 values — a client on connection A can never
// act in connection B's space / channel / identity. reg and mapping are shared
// read-only across requests; the mutable per-connection state lives on the
// short-lived per-request Server.
type HTTPHandler struct {
	reg         *registry.Registry
	mapping     *Mapping
	build       RootBuilder
	baseTrusted TrustedContext
}

// Trusted-context request headers. Space also flows onto the credential so
// X-Space-Id is set exactly as the CLI already does it.
const (
	headerSpaceID     = "X-Space-Id"
	headerChannelID   = "X-Octo-Channel-Id"
	headerChannelType = "X-Octo-Channel-Type"
	headerOnBehalfOf  = "X-Octo-On-Behalf-Of"
)

// NewHTTPHandler builds the streamable-HTTP handler. baseTrusted supplies
// defaults a request header may override.
func NewHTTPHandler(build RootBuilder, baseTrusted TrustedContext) (*HTTPHandler, error) {
	reg, err := registry.New()
	if err != nil {
		return nil, err
	}
	mapping, err := BuildMapping(reg)
	if err != nil {
		return nil, err
	}
	return &HTTPHandler{reg: reg, mapping: mapping, build: build, baseTrusted: baseTrusted}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// A GET here would be the SSE stream in a fuller implementation; this
		// first version answers request/response over POST only.
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
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
		clientResources: true, // HTTP clients that speak streamable-HTTP can read resource URIs
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
