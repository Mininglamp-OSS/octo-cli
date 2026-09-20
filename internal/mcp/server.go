package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// defaultProtocolVersion is echoed when the client sends none. The server
// otherwise mirrors the client's requested protocolVersion.
const defaultProtocolVersion = "2025-06-18"

const (
	serverName    = "octo-cli"
	serverVersion = "0.1.0-mcp"
)

// Server handles MCP JSON-RPC requests for one connection. search_ops and
// describe_op read the registry directly; call_op drives the generated cobra
// tree via build. trusted carries the connection-scoped over-privilege防护
// values; credentialToken, when set, pins call_op to that bearer (HTTP, one
// credential per connection) instead of the process env (stdio).
type Server struct {
	reg     *registry.Registry
	mapping *Mapping
	build   RootBuilder

	trusted         TrustedContext
	credentialToken string // "" → resolve from env (stdio) unless httpMode
	httpMode        bool   // HTTP connection: never inherit the server env credential
	uploadRoot      string // OCTO_MCP_UPLOAD_ROOT; confines multipart file_path over HTTP

	protocolVersion string
	clientResources bool

	// factoryFn builds the per-call factory; nil means makeFactory. Tests set it
	// to point call_op at a fake backend.
	factoryFn func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer)

	mu sync.Mutex // serialize call_op assembly per connection
}

// NewServer builds a connection server. It fails if the embedded skill
// navigation mapping is invalid (fail-fast, design §3.6(b)).
func NewServer(build RootBuilder) (*Server, error) {
	reg, err := registry.New()
	if err != nil {
		return nil, err
	}
	mapping, err := BuildMapping(reg)
	if err != nil {
		return nil, err
	}
	return &Server{
		reg:             reg,
		mapping:         mapping,
		build:           build,
		protocolVersion: defaultProtocolVersion,
	}, nil
}

// WithTrustedContext pins the connection's forced values.
func (s *Server) WithTrustedContext(tc TrustedContext) *Server { s.trusted = tc; return s }

// WithCredentialToken pins call_op to a specific bearer (HTTP per-connection).
func (s *Server) WithCredentialToken(token string) *Server { s.credentialToken = token; return s }

// makeFactory builds a fresh factory with buffered IO for one call_op, applying
// the connection credential and trusted space. A fresh factory per call keeps
// credentials and cached clients isolated between connections.
//
// HTTP connections fail closed (B1): the credential is pinned to the
// connection's bearer, and a missing/malformed bearer yields a CredentialFunc
// that returns an auth error — an HTTP call can NEVER inherit the server
// process env / auth-store credential. Only stdio (the local/trusted transport)
// resolves from the environment.
func (s *Server) makeFactory(tc TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	streams, _, outBuf, errBuf := cmdutil.NewTestIOStreams()
	f := cmdutil.NewDefaultFactory()
	f.IOStreams = streams
	if s.httpMode {
		tok := s.credentialToken
		space := tc.SpaceID
		f.CredentialFunc = func() (*credential.BotCredential, error) {
			if strings.TrimSpace(tok) == "" {
				return nil, output.ErrAuth(
					"missing or malformed Authorization bearer on the MCP HTTP connection",
					"send Authorization: Bearer <token>; the HTTP transport never inherits the server's own credential")
			}
			return &credential.BotCredential{
				Token:   tok,
				SpaceID: space,
				BotKind: credential.TokenKind(tok),
				Source:  "mcp:connection",
			}, nil
		}
	} else if tc.SpaceID != "" {
		// stdio: force the space onto the env-resolved credential via --space.
		f.Globals.Space = tc.SpaceID
	}
	return f, outBuf, errBuf
}

// Dispatch handles one JSON-RPC message and returns the response plus whether
// there is one. A JSON-RPC notification (no id) NEVER receives a response, for
// any method (B6), so the id check comes first.
func (s *Server) Dispatch(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if req.isNotification() {
		return rpcResponse{}, false
	}
	if req.Jsonrpc != jsonrpcVersion && req.Jsonrpc != "" {
		return newErrorResponse(req.ID, codeInvalidRequest, "unsupported jsonrpc version"), true
	}
	switch req.Method {
	case "initialize":
		return newResultResponse(req.ID, s.handleInitialize(req.Params)), true
	case "ping":
		return newResultResponse(req.ID, map[string]any{}), true
	case "tools/list":
		return newResultResponse(req.ID, map[string]any{"tools": toolDefinitions()}), true
	case "tools/call":
		return s.handleToolCall(ctx, req), true
	case "resources/list":
		return newResultResponse(req.ID, s.listResources()), true
	case "resources/read":
		return s.handleResourceRead(req), true
	default:
		return newErrorResponse(req.ID, codeMethodNotFound, "unknown method: "+req.Method), true
	}
}

func (s *Server) handleInitialize(params json.RawMessage) map[string]any {
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Resources json.RawMessage `json:"resources"`
		} `json:"capabilities"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}
	if p.ProtocolVersion != "" {
		s.protocolVersion = p.ProtocolVersion
	}
	s.clientResources = len(p.Capabilities.Resources) > 0
	return map[string]any{
		"protocolVersion": s.protocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"serverInfo": map[string]any{"name": serverName, "version": serverVersion},
	}
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func (s *Server) handleToolCall(ctx context.Context, req rpcRequest) rpcResponse {
	var p toolCallParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		return newErrorResponse(req.ID, codeInvalidParams, "invalid tools/call params: "+err.Error())
	}
	// An arguments value that is present but not a JSON object is an invalid
	// shape: return -32602 rather than silently degrading to empty args (B6).
	if len(p.Arguments) > 0 && !isJSONObject(p.Arguments) {
		return newErrorResponse(req.ID, codeInvalidParams, "tool arguments must be a JSON object")
	}
	switch p.Name {
	case "search_ops":
		var a struct {
			Domain string `json:"domain"`
			Query  string `json:"query"`
		}
		if err := unmarshalArgs(p.Arguments, &a); err != nil {
			return newErrorResponse(req.ID, codeInvalidParams, "invalid search_ops arguments: "+err.Error())
		}
		return newResultResponse(req.ID, s.searchOps(a.Domain, a.Query))
	case "describe_op":
		var a struct {
			OperationID string `json:"operation_id"`
		}
		if err := unmarshalArgs(p.Arguments, &a); err != nil {
			return newErrorResponse(req.ID, codeInvalidParams, "invalid describe_op arguments: "+err.Error())
		}
		if a.OperationID == "" {
			return newResultResponse(req.ID, jsonToolResult(missingArg("operation_id"), true))
		}
		return newResultResponse(req.ID, s.describeOp(a.OperationID))
	case "call_op":
		var a struct {
			OperationID string          `json:"operation_id"`
			Arguments   json.RawMessage `json:"arguments"`
		}
		if err := unmarshalArgs(p.Arguments, &a); err != nil {
			return newErrorResponse(req.ID, codeInvalidParams, "invalid call_op arguments: "+err.Error())
		}
		if a.OperationID == "" {
			return newResultResponse(req.ID, jsonToolResult(missingArg("operation_id"), true))
		}
		if len(a.Arguments) > 0 && !isJSONObject(a.Arguments) {
			return newErrorResponse(req.ID, codeInvalidParams, "call_op arguments must be a JSON object")
		}
		args, err := decodeArgs(a.Arguments)
		if err != nil {
			return newErrorResponse(req.ID, codeInvalidParams, "call_op arguments must be a JSON object: "+err.Error())
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		return newResultResponse(req.ID, s.callOp(ctx, a.OperationID, args))
	default:
		return newErrorResponse(req.ID, codeInvalidParams, "unknown tool: "+p.Name)
	}
}

// unmarshalArgs decodes the tool arguments object, treating an empty/omitted
// value as an empty object rather than an error.
func unmarshalArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// isJSONObject reports whether raw is a JSON object (first non-space byte '{').
func isJSONObject(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return b == '{'
	}
	return false
}

func (s *Server) handleResourceRead(req rpcRequest) rpcResponse {
	var a struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &a); err != nil {
		return newErrorResponse(req.ID, codeInvalidParams, "invalid resources/read params: "+err.Error())
	}
	res, err := s.readResource(a.URI)
	if err != nil {
		return newErrorResponse(req.ID, codeInvalidParams, err.Error())
	}
	return newResultResponse(req.ID, res)
}

// decodeArgs decodes the call_op arguments object with UseNumber so a uint64 id
// keeps its exact decimal text rather than rounding through float64.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func missingArg(name string) map[string]any {
	return map[string]any{"ok": false, "error": map[string]any{
		"type": "validation", "code": "VALIDATION_ERROR",
		"message": "missing required argument " + name,
	}}
}
