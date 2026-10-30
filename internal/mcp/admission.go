package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// Admission is shared only for bounded capacity, the immutable operator policy
// source, and the single request writer. It stores no per-Bot identity cache.
type Admission struct {
	stopped              atomic.Bool
	log                  *RequestLog
	debug                *DebugPolicy
	tools, verifications chan struct{}
	mu                   sync.Mutex
	tokens               float64
	updated              time.Time
}

// NewAdmission fixes the operator policy source and starts the request writer.
func NewAdmission(logDirectory string, debug *DebugPolicy) (*Admission, error) {
	l, err := OpenRequestLog(logDirectory)
	if err != nil {
		return nil, err
	}
	return &Admission{log: l, debug: debug, tools: make(chan struct{}, 32), verifications: make(chan struct{}, 16), tokens: 16, updated: time.Now()}, nil
}

// Close stops new verification and drains the bounded request writer.
func (a *Admission) Close() error {
	if a == nil {
		return nil
	}
	a.stopped.Store(true)
	return a.log.Close()
}

// WithAdmission binds operator-owned admission before serving requests.
func (s *Server) WithAdmission(a *Admission) *Server { s.admission = a; return s }
func (a *Admission) verificationSlot() (func(), error) {
	if a.stopped.Load() {
		return nil, logUnavailable()
	}
	a.mu.Lock()
	now := time.Now()
	a.tokens += now.Sub(a.updated).Seconds() * 8
	if a.tokens > 16 {
		a.tokens = 16
	}
	a.updated = now
	if a.tokens < 1 {
		a.mu.Unlock()
		return nil, admissionFailure(-32006, "MCP_BUSY")
	}
	select {
	case a.verifications <- struct{}{}:
		a.tokens--
		a.mu.Unlock()
		return func() { <-a.verifications }, nil
	default:
		a.mu.Unlock()
		return nil, admissionFailure(-32006, "MCP_BUSY")
	}
}

func admissionResponse(id json.RawMessage, err error) rpcResponse {
	var ce *contextError
	if errors.As(err, &ce) {
		r := newErrorResponse(id, codeInvalidParams, "invalid Octo context")
		r.Error.Data = map[string]string{"field_path": ce.path, "reason": ce.reason}
		return r
	}
	var ae *admissionError
	if errors.As(err, &ae) {
		return newErrorResponse(id, ae.code, ae.reason)
	}
	return newErrorResponse(id, codeInvalidParams, "INVALID_ARGUMENTS")
}

func (s *Server) verify(ctx context.Context, policy bool) (call *Server, uid, origin, mode string, err error) {
	a := s.admission
	if a == nil {
		return nil, "", "", "", logUnavailable()
	}
	release, err := a.verificationSlot()
	if err != nil {
		return nil, "", "", "", err
	}
	defer release()
	fn := s.factoryFn
	if fn == nil {
		fn = s.makeFactory
	}
	f, out, errout := fn(s.trusted)
	uid, origin, mode, err = verifyMCPBot(ctx, f, policy)
	if err != nil {
		return nil, "", "", "", err
	}
	bot, err := f.Credential()
	if err != nil {
		return nil, "", "", "", admissionFailure(-32001, "BOT_AUTH_INVALID")
	}
	// Build a new server explicitly: mutexes and protocol negotiation state
	// must never be copied or used as request-scoped provenance storage.
	s.mu.Lock()
	clientResources := s.clientResources
	s.mu.Unlock()
	call = &Server{reg: s.reg, mapping: s.mapping, build: s.build, trusted: s.trusted, credentialToken: bot.Token, httpMode: s.httpMode, uploadRoot: s.uploadRoot, allowLocalUpload: s.allowLocalUpload, uploadOpener: s.uploadOpener, baseGlobals: s.baseGlobals, facade: s.facade, secretDownloads: s.secretDownloads, admission: a, protocolVersion: defaultProtocolVersion, clientResources: clientResources}
	call.factoryFn = func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) { return f, out, errout }
	return call, uid, origin, mode, nil
}

func (s *Server) admitToolCall(ctx context.Context, req rpcRequest, p toolCallParams, received time.Time) (*Server, func(), error) { //nolint:gocritic,gocyclo // immutable request snapshots are copied at this boundary; ordered authentication, policy, context, exposure and durability gates must all precede execution
	a := s.admission
	if a == nil || a.stopped.Load() {
		return nil, nil, logUnavailable()
	}
	select {
	case a.tools <- struct{}{}:
	default:
		return nil, nil, admissionFailure(-32006, "MCP_BUSY")
	}
	release := func() { <-a.tools }
	call, uid, origin, mode, err := s.verify(ctx, a.debug == nil)
	if err != nil {
		release()
		return nil, nil, err
	}
	if a.debug != nil {
		mode, err = a.debug.resolve(uid, origin)
		if err != nil {
			release()
			return nil, nil, err
		}
	}
	source, err := parseDeclaredSource(p.Meta)
	if err != nil {
		release()
		return nil, nil, err
	}
	if mode == "strict" && source == nil {
		release()
		return nil, nil, admissionFailure(-32602, "CONTEXT_REQUIRED")
	}
	if s.trusted.OnBehalfOf != "" {
		release()
		return nil, nil, admissionFailure(-32005, "OBO_FORBIDDEN")
	}
	var controls struct {
		OperationID string `json:"operation_id"`
	}
	if len(p.Arguments) > 0 {
		if err := json.Unmarshal(p.Arguments, &controls); err != nil {
			release()
			return nil, nil, admissionFailure(-32602, "INVALID_ARGUMENTS")
		}
	}
	var ordinary map[string]bool
	if controls.OperationID == "docs.comment-task.get" || controls.OperationID == "docs.comment-task.events" {
		ordinary = map[string]bool{"botUid": true}
	}
	if err = guardBotOnlyArguments(ctx, p.Arguments, ordinary); err != nil {
		release()
		return nil, nil, err
	}
	if oboOperation(controls.OperationID) {
		release()
		return nil, nil, admissionFailure(-32005, "OBO_FORBIDDEN")
	}
	if detail, ok := s.reg.GetOperation(controls.OperationID); ok {
		if s.reg.ServiceDisabled(detail.Service) || !mcpExposed(controls.OperationID) {
			// The original handlers return the established safe error envelope
			// at these gates, before any execution or request-store access.
			return call, release, nil
		}
		if _, div := divergentMCPOps[controls.OperationID]; div {
			return call, release, nil
		}
		// Header declarations can carry auth controls even when their body
		// spelling would be ordinary business data. Deny such operations.
		for i := range detail.Parameters {
			param := &detail.Parameters[i]
			if param.In == "header" && identityControl(param.Name) {
				release()
				return nil, nil, admissionFailure(-32005, "IDENTITY_OVERRIDE_FORBIDDEN")
			}
		}
	}
	record, err := projectRequest(call, req, p, uid, call.credentialToken, source, received)
	if err == nil {
		err = a.log.AppendAndSync(ctx, record)
	}
	if err == nil && ctx.Err() != nil {
		err = logUnavailable()
	}
	if err != nil {
		release()
		return nil, nil, err
	}
	return call, release, nil
}

func oboOperation(id string) bool { return id == "bot.obo-grant" || id == "bot.obo-grants-self" }

func guardOperationArguments(ctx context.Context, id string, args map[string]any) error {
	if oboOperation(id) {
		return admissionFailure(-32005, "OBO_FORBIDDEN")
	}
	raw, err := json.Marshal(map[string]any{"arguments": args})
	if err != nil {
		return admissionFailure(-32602, "INVALID_ARGUMENTS")
	}
	var ordinary map[string]bool
	if id == "docs.comment-task.get" || id == "docs.comment-task.events" {
		ordinary = map[string]bool{"botUid": true}
	}
	return guardBotOnlyArguments(ctx, raw, ordinary)
}
func controlKey(k string) string {
	return strings.ToLower(safetyNameKey(k))
}
func identityControl(k string) bool {
	switch controlKey(k) {
	case "token", "authorization", "profile", "botid", "credential", "actoruid", "subjectuid", "identitymode", "policydebug", "debugpolicyfile", "policydebugfile", "contextmode", "botuid", "forceonbehalfof":
		return true
	}
	return false
}

func guardBotOnlyArguments(ctx context.Context, raw json.RawMessage, ordinary ...map[string]bool) error { //nolint:gocyclo // bounded recursive scanner checks each reserved-control class before map decoding
	keys := 0
	var walk func(json.RawMessage, int) error
	walk = func(v json.RawMessage, depth int) error {
		if depth > 64 {
			return admissionFailure(-32602, "INVALID_ARGUMENTS")
		}
		v = bytes.TrimSpace(v)
		if len(v) == 0 {
			return nil
		}
		if v[0] == '{' {
			seen := make(map[string]bool)
			return visitObjectMembers(v, func(k string, value json.RawMessage) error {
				if seen[k] {
					return admissionFailure(-32602, "INVALID_ARGUMENTS")
				}
				seen[k] = true
				keys++
				if keys > 8192 {
					return admissionFailure(-32602, "INVALID_ARGUMENTS")
				}
				if keys%64 == 0 && ctx.Err() != nil {
					return admissionFailure(-32602, "INVALID_ARGUMENTS")
				}
				folded := controlKey(k)
				if folded == "onbehalfof" || folded == "actualsenderuid" || strings.HasPrefix(folded, "obo") {
					return admissionFailure(-32005, "OBO_FORBIDDEN")
				}
				// Outer tool controls and operation-level identity selectors are
				// reserved; ordinary nested business fields are not identities.
				businessFilter := depth == 1 && len(ordinary) > 0 && ordinary[0][k]
				if depth <= 1 && identityControl(k) && !businessFilter {
					return admissionFailure(-32005, "IDENTITY_OVERRIDE_FORBIDDEN")
				}
				return walk(value, depth+1)
			})
		}
		if v[0] == '[' {
			var a []json.RawMessage
			if json.Unmarshal(v, &a) != nil {
				return admissionFailure(-32602, "INVALID_ARGUMENTS")
			}
			for _, child := range a {
				if err := walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(raw, 0)
}
