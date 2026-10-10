// Package mcp implements the octo-cli MCP (Model Context Protocol) server: a
// thin JSON-RPC transport layer over the existing metadata-driven engine.
//
// The server exposes three meta-tools — search_ops, describe_op, call_op —
// rather than one MCP tool per operation, so a client's tools/list stays a few
// hundred tokens instead of the ~60k-150k tokens a full 342-operation expansion
// would cost. search_ops and describe_op read the registry directly; call_op
// drives the generated cobra command tree in-process so every request reuses
// the CLI's identity routing, request assembly, validation, transport, secret
// masking, and JSON envelope unchanged (no second copy of parameter truth).
//
// Skill navigation (which business Skill to read for an operation) rides in the
// tool *return values*, never in the constant tool inputSchema, so it adds zero
// resident context. The service->Skill and operation->advice mapping is built
// from each SKILL.md's frontmatter `services:` line plus skills/manifest.json,
// merged and validated fail-fast at startup.
package mcp

import "encoding/json"

// JSON-RPC 2.0 wire types. MCP is JSON-RPC 2.0 over stdio (newline-delimited)
// or HTTP (JSON-RPC over POST).

const jsonrpcVersion = "2.0"

// Request ids are echoed by every response. Bound the encoded id before it
// reaches either response constructor, including direct error paths.
const maxMCPRequestIDBytes = 256

// rpcRequest is an inbound JSON-RPC request or notification. A notification has
// no id; requests carry one that must be echoed on the response.
type rpcRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// decodeRPCRequest applies the same unambiguous envelope policy to HTTP and
// stdio, so an inspecting proxy cannot disagree with the server about method
// or id selection.
func decodeRPCRequest(raw []byte) (rpcRequest, int, error) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		if json.Valid(raw) {
			return rpcRequest{}, codeInvalidRequest, err
		}
		return rpcRequest{}, codeParseError, err
	}
	if err := validateObjectKeysPolicy(raw, true, "jsonrpc", "id", "method", "params"); err != nil {
		return rpcRequest{}, codeInvalidRequest, err
	}
	return req, 0, nil
}

// isNotification reports whether the message omits an id (JSON-RPC
// notifications get no response).
func (r *rpcRequest) isNotification() bool {
	return len(r.ID) == 0
}

// rpcResponse is an outbound JSON-RPC response. Exactly one of Result / Error
// is set.
type rpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Standard JSON-RPC error codes (JSON-RPC 2.0 spec).
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

func newResultResponse(id json.RawMessage, result any) rpcResponse {
	raw, err := json.Marshal(result)
	if err != nil {
		return newErrorResponse(id, codeInternalError, "marshal result: "+err.Error())
	}
	return rpcResponse{Jsonrpc: jsonrpcVersion, ID: boundedResponseID(id), Result: raw}
}

func newErrorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{
		Jsonrpc: jsonrpcVersion,
		ID:      boundedResponseID(id),
		Error:   &rpcError{Code: code, Message: truncateRunes(message, 512)},
	}
}

func boundedResponseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 || !validRequestID(id) {
		return json.RawMessage("null")
	}
	return id
}
