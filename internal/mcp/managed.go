package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

// ManagedTurn is private host state. Public Bot bearers, declarations and MCP
// initialize capabilities cannot construct or replace its invocation handle.
type ManagedTurn struct {
	resources   bool
	coordinator *permission.Coordinator
	invocation  *permission.Invocation
	source      permission.DeclarationV2
	mu          sync.Mutex
	negotiated  bool
	closed      bool
}

// NewManagedTurn binds a host-admitted invocation to its exact native source.
func NewManagedTurn(c *permission.Coordinator, i *permission.Invocation, source permission.DeclarationV2) *ManagedTurn {
	return &ManagedTurn{coordinator: c, invocation: i, source: source}
}

// WithManagedTurn enables enforcement on this connection with no downgrade.
func (s *Server) WithManagedTurn(turn *ManagedTurn) *Server { s.managed = turn; return s }

// Close invalidates the turn and joins its coordinator-owned write boundary.
func (m *ManagedTurn) Close() { m.mu.Lock(); m.closed = true; m.mu.Unlock(); m.invocation.Close() }

func permissionResponse(id json.RawMessage, r *permission.Refusal) rpcResponse {
	resp := newErrorResponse(id, -32007, "PERMISSION_REFUSED")
	resp.Error.Data = r
	return resp
}

func (s *Server) declaredSource(meta json.RawMessage) (*DeclaredSource, error) {
	if s.managed == nil {
		return parseDeclaredSource(meta)
	}
	obj, err := strictObject(meta)
	if err != nil {
		return nil, contextFailure("params._meta.octo", "type")
	}
	raw, ok := obj["octo"]
	if !ok {
		return nil, contextFailure("params._meta.octo", "missing")
	}
	v, err := permission.ParseDeclarationV2(raw)
	if err != nil {
		return nil, contextFailure("params._meta.octo", "type")
	}
	if v != s.managed.source {
		return nil, permission.ContextUntrusted()
	}
	return &DeclaredSource{Version: 2, Sender: v.Sender, Channel: v.Channel, ChannelType: v.ChannelType, SpaceID: v.SpaceID, MessageID: v.MessageID}, nil
}

// Dispatch uses the same coordinator for both facades, resources and transports.
// The returned response is still host-private; writeManaged owns publication.
func (s *Server) Dispatch(ctx context.Context, req rpcRequest) (rpcResponse, bool) { //nolint:gocritic // immutable protocol/config snapshots must be detached by value
	resp, has, call := s.dispatchManaged(ctx, req)
	defer call.Close()
	return resp, has
}

func (s *Server) dispatchManaged(ctx context.Context, req rpcRequest) (rpcResponse, bool, *permission.Call) { //nolint:gocritic,gocyclo // closed fixed-program branches retain explicit checks in security order
	if s.managed == nil {
		r, h := s.dispatchRaw(ctx, req)
		return r, h, nil
	}
	m := s.managed
	if !validRPCID(req.ID) || req.isNotification() || req.Jsonrpc != "" && req.Jsonrpc != jsonrpcVersion {
		r, h := s.dispatchRaw(ctx, req)
		return r, h, nil
	}
	m.mu.Lock()
	closed, selected := m.closed, m.negotiated
	resources := m.resources
	m.mu.Unlock()
	s.mu.Lock()
	s.clientResources = resources
	s.mu.Unlock()
	if closed {
		return permissionResponse(req.ID, permission.ContextUntrusted()), true, nil
	}
	if req.Method == "initialize" {
		var obj map[string]json.RawMessage
		if selected || json.Unmarshal(req.Params, &obj) != nil {
			return permissionResponse(req.ID, permission.ContextUntrusted()), true, nil
		}
		var capabilities struct {
			Experimental map[string]json.RawMessage `json:"experimental"`
			Resources    json.RawMessage            `json:"resources"`
		}
		if json.Unmarshal(obj["capabilities"], &capabilities) != nil || !managedSelection(capabilities.Experimental["octo_permission_binding"]) {
			return permissionResponse(req.ID, permission.ContextUntrusted()), true, nil
		}
		result := s.handleInitialize(req.Params)
		result["capabilities"].(map[string]any)["experimental"] = map[string]any{"octo_permission_binding": map[string]int{"selected_binding": 2, "selected_metadata": 2, "selected_identity": 2}}
		m.mu.Lock()
		if m.closed || m.negotiated {
			m.mu.Unlock()
			return permissionResponse(req.ID, permission.ContextUntrusted()), true, nil
		}
		m.negotiated = true
		m.resources = len(capabilities.Resources) > 0
		m.mu.Unlock()
		return newResultResponse(req.ID, result), true, nil
	}
	if !selected {
		return permissionResponse(req.ID, permission.ContextUntrusted()), true, nil
	}
	if req.Method == "ping" {
		return newResultResponse(req.ID, map[string]any{}), true, nil
	}
	if req.Method == "tools/list" {
		return newResultResponse(req.ID, map[string]any{"tools": s.toolList()}), true, nil
	}
	meta, err := paramsMeta(req.Params)
	if err == nil {
		_, err = s.declaredSource(meta)
	}
	if err != nil {
		if r, ok := err.(*permission.Refusal); ok {
			return permissionResponse(req.ID, r), true, nil
		}
		return admissionResponse(req.ID, err), true, nil
	}
	capability, children, err := s.requestCapability(req)
	if err != nil {
		return permissionResponse(req.ID, permission.CapabilityDenied()), true, nil
	}
	// Native-delivery replay is fenced at ingress. RPC IDs correlate replies;
	// equal arguments or reused RPC IDs may be legitimate new logical calls.
	// BeginCall mints a fresh private call ID, and each prepared attempt is one-use.
	ctx, err = m.coordinator.BeginCall(ctx, m.invocation, capability, req.Params, children, s.requestTargets(capability, children)...)
	call := permission.BoundCall(ctx)
	if err != nil {
		if r := call.Refused(); r != nil {
			return permissionResponse(req.ID, r), true, call
		}
		return permissionResponse(req.ID, permission.PolicyUnavailable()), true, call
	}
	resp, has := s.dispatchRaw(ctx, req)
	if r := call.Refused(); r != nil {
		return permissionResponse(req.ID, r), true, call
	}
	if has {
		resp = enforceSerializedRPCBudget(req, resp, call)
	}
	return resp, has, call
}

func serializedRPCResponseLen(resp rpcResponse) (int64, bool) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(resp); err != nil {
		return 0, false
	}
	return int64(b.Len()), true
}

func enforceSerializedRPCBudget(req rpcRequest, resp rpcResponse, call *permission.Call) rpcResponse {
	if call == nil || (resp.Error != nil && resp.Error.Code == -32007) {
		return resp
	}
	limit := call.MaxSerializedRPCBytes()
	if limit <= 0 {
		return resp
	}
	observed, ok := serializedRPCResponseLen(resp)
	if !ok || observed <= limit {
		return resp
	}
	var p toolCallParams
	_ = decodeToolCallParams(req.Params, &p)
	var a struct {
		OperationID string `json:"operation_id"`
	}
	_ = json.Unmarshal(p.Arguments, &a)
	detail, _ := json.Marshal(map[string]any{
		"budget_kind":    "serialized_rpc_bytes",
		"limit_bytes":    limit,
		"observed_bytes": observed,
	})
	ee := output.ErrWithHint(
		"api",
		"RESPONSE_BUDGET_EXCEEDED",
		fmt.Sprintf("serialized MCP RPC response (%d bytes) exceeds managed serialized_rpc_bytes budget of %d bytes", observed, limit),
		"narrow the request scope or page size",
	)
	if len(detail) > 0 {
		ee.Detail = detail
	}
	env := synthErrorEnvelope(ee)
	var candidate rpcResponse
	if p.Name == "execute" {
		candidate = newResultResponse(req.ID, jsonToolResult(map[string]any{
			"status":       "execution_error",
			"operation_id": a.OperationID,
			"envelope":     decodeEnvelope(env),
			"error":        ee,
		}, true))
	} else {
		candidate = newResultResponse(req.ID, rawToolResult(env, true))
	}
	if n, fitOK := serializedRPCResponseLen(candidate); fitOK && n <= limit {
		return candidate
	}
	compactEE := output.ErrWithHint(
		"api",
		"RESPONSE_BUDGET_EXCEEDED",
		"exceeds serialized_rpc_bytes budget",
		"",
	)
	if len(detail) > 0 {
		compactEE.Detail = detail
	}
	compactEnv := synthErrorEnvelope(compactEE)
	compactID := req.ID
	if len(compactID) > 64 {
		compactID = json.RawMessage("null")
	}
	if p.Name == "execute" {
		opID := a.OperationID
		if len(opID) > 64 {
			opID = ""
		}
		candidate = newResultResponse(compactID, jsonToolResult(map[string]any{
			"status":       "execution_error",
			"operation_id": opID,
			"envelope":     decodeEnvelope(compactEnv),
			"error":        compactEE,
		}, true))
	} else {
		candidate = newResultResponse(compactID, rawToolResult(compactEnv, true))
	}
	if n, fitOK := serializedRPCResponseLen(candidate); fitOK && n <= limit {
		return candidate
	}
	return rpcResponse{
		Jsonrpc: jsonrpcVersion,
		ID:      compactID,
		Error: &rpcError{
			Code:    -32000,
			Message: "RESPONSE_BUDGET_EXCEEDED",
			Data: map[string]any{
				"code":           "RESPONSE_BUDGET_EXCEEDED",
				"budget_kind":    "serialized_rpc_bytes",
				"limit_bytes":    limit,
				"observed_bytes": observed,
			},
		},
	}
}

func managedSelection(raw json.RawMessage) bool {
	m, err := strictObject(raw, "selected_binding", "selected_metadata", "selected_identity")
	if err != nil {
		return false
	}
	for _, v := range m {
		if string(bytes.TrimSpace(v)) != "2" {
			return false
		}
	}
	return true
}

func (s *Server) requestCapability(req rpcRequest) (string, []string, error) { //nolint:gocritic,gocyclo // closed fixed-program branches retain explicit checks in security order
	if req.Method == "resources/list" {
		return "octo-cli/catalog/resource-list/skills", nil, nil
	}
	if req.Method == "resources/read" {
		var p struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(req.Params, &p) != nil {
			return "", nil, io.ErrUnexpectedEOF
		}
		uri, _, _ := strings.Cut(p.URI, "#")
		if !strings.HasPrefix(uri, skillURIScheme) {
			return "", nil, io.ErrUnexpectedEOF
		}
		return "octo-cli/skills/resource/" + strings.TrimPrefix(uri, skillURIScheme), nil, nil
	}
	if req.Method != "tools/call" {
		return "", nil, io.ErrUnexpectedEOF
	}
	var p toolCallParams
	if decodeToolCallParams(req.Params, &p) != nil {
		return "", nil, io.ErrUnexpectedEOF
	}
	var a struct {
		Intent, Domain, Query string
		OperationID           string `json:"operation_id"`
		Arguments             map[string]any
	}
	if json.Unmarshal(p.Arguments, &a) != nil {
		return "", nil, io.ErrUnexpectedEOF
	}
	switch p.Name {
	case "get_secret_download_url":
		return "octo-cli/bot/secret/get_secret_download_url", nil, nil
	case "search_ops", "get_skill":
		if p.Name == "get_skill" && a.Intent == "describe" {
			break
		}
		if a.Domain != "" {
			return "octo-cli/" + a.Domain + "/discovery/search", nil, nil
		}
		if a.Query == "" {
			return "octo-cli/discovery/module-map", nil, nil
		}
		return "octo-cli/discovery/search-all", nil, nil
	case "execute", "call_op", "describe_op":
	default:
		return "", nil, io.ErrUnexpectedEOF
	}
	d, ok := s.reg.GetOperation(a.OperationID)
	if !ok {
		return "", nil, io.ErrUnexpectedEOF
	}
	kind := "op"
	if p.Name == "describe_op" || p.Name == "get_skill" {
		kind = "schema"
	}
	capability := "octo-cli/" + d.Service + "/" + kind + "/" + d.ID
	var children []string
	if d.ID == "docs.scene.export" && a.Arguments["format"] == "excalidraw" {
		children = []string{"octo-cli/docs/op/docs.scene.get", "octo-cli/docs/op/docs.attachments.get", "octo-cli/docs/derived/portable-attachment-bytes"}
	}
	return capability, children, nil
}

func writeManaged(ctx context.Context, out io.Writer, resp rpcResponse, call *permission.Call) error {
	defer call.Close()
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(resp); err != nil {
		return err
	}
	if call == nil || resp.Error != nil && resp.Error.Code == -32007 {
		_, err := out.Write(b.Bytes())
		return err
	}
	if w, ok := out.(http.ResponseWriter); ok {
		return writeManagedHTTP(ctx, w, resp.ID, b.Bytes(), call)
	}
	if err := call.Publish(ctx, out, b.Bytes()); err != nil {
		if r := call.Refused(); r != nil {
			b.Reset()
			if err := json.NewEncoder(&b).Encode(permissionResponse(resp.ID, r)); err != nil {
				return err
			}
			_, err = out.Write(b.Bytes())
			return err
		}
		return err
	}
	return nil
}

// The private relay is HTTP/1.1 on a host-local raw socket. Hijacking before
// F/B avoids net/http's delayed buffering/flush becoming an unchecked send.
func writeManagedHTTP(ctx context.Context, w http.ResponseWriter, id json.RawMessage, body []byte, call *permission.Call) error {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return permission.PolicyUnavailable()
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return permission.PolicyUnavailable()
	}
	defer closePermissionIO(conn)
	if buffered.Writer.Buffered() != 0 {
		return permission.PolicyUnavailable()
	}
	frame := func(payload []byte) []byte {
		var budgetHeader string
		if maxBytes := call.MaxAcquisitionBytes(); maxBytes > 0 {
			budgetHeader = fmt.Sprintf("X-Octo-Acquisition-Budget-Bytes: %d\r\n", maxBytes)
		} else if hb := strings.TrimSpace(w.Header().Get("X-Octo-Acquisition-Budget-Bytes")); hb != "" {
			budgetHeader = "X-Octo-Acquisition-Budget-Bytes: " + hb + "\r\n"
		}
		header := fmt.Sprintf("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n%sContent-Length: %d\r\nConnection: close\r\n\r\n", budgetHeader, len(payload))
		return append([]byte(header), payload...)
	}
	if err = call.PublishConnection(ctx, conn, frame(body)); err == nil {
		return nil
	}
	if refusal := call.Refused(); refusal != nil {
		refused, encodeErr := json.Marshal(permissionResponse(id, refusal))
		if encodeErr != nil {
			return encodeErr
		}
		_, err = conn.Write(frame(append(refused, '\n')))
	}
	return err
}

func (s *Server) requestTargets(root string, children []string) []permission.RequestTarget {
	var targets []permission.RequestTarget
	for _, capability := range append([]string{root}, children...) {
		parts := strings.SplitN(capability, "/", 4)
		if len(parts) != 4 || parts[2] != "op" {
			continue
		}
		detail, ok := s.reg.GetOperation(parts[3])
		if !ok || detail.Service != parts[1] {
			continue
		}
		targets = append(targets, permission.RequestTarget{Capability: capability, Method: detail.Method, Path: detail.Path})
		switch detail.ID {
		case "message.search", "message.search.all":
			targets = append(targets, permission.RequestTarget{Capability: capability, Method: detail.Method, Path: "/v1/bot/messages/_search_global_messages"})
		case "message.search.files":
			targets = append(targets, permission.RequestTarget{Capability: capability, Method: detail.Method, Path: "/v1/bot/messages/_search_global_files"})
		}
	}
	return targets
}
