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

import (
	"encoding/json"
	"errors"
)

// JSON-RPC 2.0 wire types. MCP is JSON-RPC 2.0 over stdio (newline-delimited)
// or HTTP (JSON-RPC over POST).

const jsonrpcVersion = "2.0"

// rpcRequest is an inbound JSON-RPC request or notification. A notification has
// no id; requests carry one that must be echoed on the response.
type rpcRequest struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message omits an id (JSON-RPC
// notifications get no response).
func (r *rpcRequest) isNotification() bool {
	return len(r.ID) == 0
}

// rpcResponse is an outbound JSON-RPC response. Exactly one of Result / Error
// is set. id is always present: JSON-RPC 2.0 requires "id": null when the
// request id could not be determined (parse error / invalid request), so a nil
// ID must marshal as null rather than be omitted.
type rpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
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
	return rpcResponse{Jsonrpc: jsonrpcVersion, ID: responseID(id), Result: raw}
}

func newErrorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{
		Jsonrpc: jsonrpcVersion,
		ID:      responseID(id),
		Error:   &rpcError{Code: code, Message: message},
	}
}

// responseID normalises an undetermined id to nil, which json.RawMessage
// marshals as null (an empty non-nil RawMessage would fail to marshal).
func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return nil
	}
	return id
}

// validRequestID reports whether a present id has a JSON-RPC 2.0 legal type:
// string, number, or null. An absent id (notification) is valid.
func validRequestID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	switch c := id[0]; {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return true
	default:
		return string(id) == "null"
	}
}

// decodeRequest parses one inbound message for either transport. Malformed JSON
// is a parse error (-32700). Well-formed JSON that is not a valid request
// object — not an object, wrongly typed members, an id that is not a
// string/number/null, or a request without a method — is an invalid request
// (-32600). Both echo the id when it can be determined and "id": null
// otherwise. A nil *rpcResponse means req is ready for Dispatch.
func decodeRequest(raw []byte) (rpcRequest, *rpcResponse) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			resp := newErrorResponse(nil, codeParseError, "parse error: "+err.Error())
			return rpcRequest{}, &resp
		}
		// A type mismatch still decodes the remaining members, so a
		// well-typed id is recoverable.
		id := req.ID
		if !validRequestID(id) {
			id = nil
		}
		resp := newErrorResponse(id, codeInvalidRequest, "invalid request: "+err.Error())
		return rpcRequest{}, &resp
	}
	if !validRequestID(req.ID) {
		resp := newErrorResponse(nil, codeInvalidRequest, "invalid request: id must be a string, number, or null")
		return rpcRequest{}, &resp
	}
	if !req.isNotification() && req.Method == "" {
		resp := newErrorResponse(req.ID, codeInvalidRequest, "invalid request: method is required")
		return rpcRequest{}, &resp
	}
	return req, nil
}
