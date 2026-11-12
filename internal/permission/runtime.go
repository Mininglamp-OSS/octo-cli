package permission

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
)

type callContextKey struct{}
type responseBudgetsContextKey struct{}

var (
	relayEndpointBudgetsMu sync.RWMutex
	relayEndpointBudgets   = map[string]ResponseBudgets{}
)

// WithResponseBudgets binds explicit ResponseBudgets to ctx so in-process
// callers and turn-relay bridges without a direct Call handle can enforce the
// same fail-closed acquisition and serialized RPC ceilings.
func WithResponseBudgets(ctx context.Context, budgets ResponseBudgets) context.Context {
	return context.WithValue(ctx, responseBudgetsContextKey{}, budgets)
}

// ResponseBudgetsFromContext returns any explicit ResponseBudgets bound to ctx.
func ResponseBudgetsFromContext(ctx context.Context) (ResponseBudgets, bool) {
	if ctx == nil {
		return ResponseBudgets{}, false
	}
	b, ok := ctx.Value(responseBudgetsContextKey{}).(ResponseBudgets)
	return b, ok
}

// RegisterRelayEndpointBudgets associates a private turn-relay endpoint URL
// with the Coordinator's configured ResponseBudgets for its lifetime.
func RegisterRelayEndpointBudgets(endpoint string, budgets ResponseBudgets) {
	if endpoint == "" || !budgets.Valid() {
		return
	}
	relayEndpointBudgetsMu.Lock()
	relayEndpointBudgets[endpoint] = budgets
	relayEndpointBudgetsMu.Unlock()
}

// UnregisterRelayEndpointBudgets removes any ResponseBudgets association for endpoint.
func UnregisterRelayEndpointBudgets(endpoint string) {
	if endpoint == "" {
		return
	}
	relayEndpointBudgetsMu.Lock()
	delete(relayEndpointBudgets, endpoint)
	relayEndpointBudgetsMu.Unlock()
}

// Call is an immutable logical MCP call. Child contexts retain the same root
// refusal/uncertainty state, but never borrow the root's capability grant.
type Call struct {
	coordinator     *Coordinator
	invocation      *Invocation
	capability      string
	callID          string
	argumentsDigest string
	children        map[string]bool
	state           *callState
	targets         map[string][]RequestTarget
}

// RequestTarget is a source-owned operation method/path binding.
type RequestTarget struct{ Capability, Method, Path string }
type callState struct {
	sync.Mutex
	refusal   *Refusal
	end       sync.Once
	cancel    context.CancelFunc
	stop      func() bool
	pages     map[string]int
	pageBytes map[string]int64
}

// BeginCall establishes the root check before any protected metadata/input.
// Its positive result is not a lease: all concrete sinks recheck independently.
func (c *Coordinator) BeginCall(ctx context.Context, i *Invocation, capability string, arguments []byte, children []string, targets ...RequestTarget) (context.Context, error) {
	if i == nil || i.owner != c {
		return ctx, refuse("context_untrusted")
	}
	select {
	case i.calls <- struct{}{}:
	default:
		return ctx, PolicyUnavailable()
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(i.context, cancel)
	sum := sha256.Sum256(arguments)
	call := &Call{coordinator: c, invocation: i, capability: capability, callID: randomID(), argumentsDigest: hex.EncodeToString(sum[:]), children: map[string]bool{}, state: &callState{cancel: cancel, stop: stop, pages: map[string]int{}, pageBytes: map[string]int64{}}}
	call.targets = map[string][]RequestTarget{}
	for _, child := range children {
		call.children[child] = true
	}
	for _, target := range targets {
		if target.Capability == capability || call.children[target.Capability] {
			call.targets[target.Capability] = append(call.targets[target.Capability], target)
		}
	}
	ctx = context.WithValue(ctx, callContextKey{}, call)
	if err := call.check(ctx, func() error { return nil }); err != nil {
		call.Close()
		return ctx, err
	}
	return ctx, nil
}

// MaxAcquisitionBytes returns the configured per-response acquisition byte
// budget for this managed call, or 0 when unconfigured.
func (c *Call) MaxAcquisitionBytes() int64 {
	if c == nil || c.coordinator == nil || c.coordinator.responseBudgets == nil {
		return 0
	}
	return c.coordinator.responseBudgets.MaxAcquisitionBytes
}

// MaxSerializedRPCBytes returns the configured serialized MCP RPC response byte
// budget for this managed call, or 0 when unconfigured.
func (c *Call) MaxSerializedRPCBytes() int64 {
	if c == nil || c.coordinator == nil || c.coordinator.responseBudgets == nil {
		return 0
	}
	return c.coordinator.responseBudgets.MaxSerializedRPCBytes
}

// PageStopLimit returns the configured page-count stop bound for clean
// page-budget-prefix pagination when ResponseBudgets is active, or 0 otherwise.
func (c *Call) PageStopLimit() int {
	if c == nil || c.coordinator == nil || StaticProvenanceRequired(c.capability) {
		return 0
	}
	if bound, ok := c.coordinator.pageBounds[c.capability]; ok {
		if c.coordinator.responseBudgets != nil && c.coordinator.responseBudgets.MaxPages < bound.Pages {
			return c.coordinator.responseBudgets.MaxPages
		}
		return bound.Pages
	}
	if c.coordinator.responseBudgets != nil {
		return c.coordinator.responseBudgets.MaxPages
	}
	return 0
}

// AdmitCumulativeBytes checks the merged pagination payload against the
// configured cumulative pagination byte budget without silent truncation.
func (c *Call) AdmitCumulativeBytes(totalBytes int64) error {
	if c == nil || c.coordinator == nil || c.coordinator.responseBudgets == nil {
		return nil
	}
	limit := c.coordinator.responseBudgets.MaxCumulativeBytes
	if limit > 0 && totalBytes > limit {
		return &ResponseBudgetError{Kind: "cumulative_bytes", Limit: limit, Observed: totalBytes}
	}
	return nil
}

// ConsumeBytes owns insertion of one completed read into helper/derived state.
func (c *Call) ConsumeBytes(ctx context.Context, source []byte) ([]byte, error) {
	if limit := c.MaxAcquisitionBytes(); limit > 0 && int64(len(source)) > limit {
		return nil, &ResponseBudgetError{Kind: "acquisition_bytes", Limit: limit, Observed: int64(len(source))}
	}
	var sealed []byte
	err := c.check(ctx, func() error { sealed = append([]byte(nil), source...); return nil })
	return sealed, err
}

// AdmitPage counts every returned item before aggregation. Missing compatible
// source bounds are unavailable, never zero/unlimited or silent truncation.
func AdmitPage(ctx context.Context, count int) error {
	return AdmitPageBytes(ctx, count, 0)
}

// AdmitPageBytes enforces per-page item count, page count, and cumulative page
// byte budgets across each pagination page boundary with a fresh F -> B check.
func AdmitPageBytes(ctx context.Context, count int, pageBytes int) error {
	c := BoundCall(ctx)
	if c == nil {
		return nil
	}
	if StaticProvenanceRequired(c.capability) {
		err := PolicyUnavailable()
		c.record(err)
		return err
	}
	bound, hasStatic := c.coordinator.pageBounds[c.capability]
	budgets := c.coordinator.responseBudgets
	if !hasStatic && budgets == nil {
		err := PolicyUnavailable()
		c.record(err)
		return err
	}
	if count < 0 || (hasStatic && count > bound.Items) {
		err := PolicyUnavailable()
		c.record(err)
		return err
	}
	if budgets != nil {
		if count > budgets.MaxPageItems {
			return &ResponseBudgetError{Kind: "page_items", Limit: int64(budgets.MaxPageItems), Observed: int64(count)}
		}
		c.state.Lock()
		nextBytes := c.state.pageBytes[c.capability] + int64(pageBytes)
		c.state.Unlock()
		if pageBytes > 0 && nextBytes > budgets.MaxCumulativeBytes {
			return &ResponseBudgetError{Kind: "cumulative_bytes", Limit: budgets.MaxCumulativeBytes, Observed: nextBytes}
		}
	}
	return c.check(ctx, func() error {
		c.state.Lock()
		defer c.state.Unlock()
		c.state.pages[c.capability]++
		c.state.pageBytes[c.capability] += int64(pageBytes)
		if hasStatic && c.state.pages[c.capability] > bound.Pages {
			return PolicyUnavailable()
		}
		if budgets != nil && c.state.pages[c.capability] > budgets.MaxPages {
			return &ResponseBudgetError{Kind: "page_count", Limit: int64(budgets.MaxPages), Observed: int64(c.state.pages[c.capability])}
		}
		return nil
	})
}

// RequestContext resolves only source-declared physical targets. Manual engine
// helpers without an OperationID can match an exact root/declared-child path;
// an unrelated hidden request cannot borrow the root capability.
func RequestContext(ctx context.Context, service, id, method, path string) (context.Context, error) {
	c := BoundCall(ctx)
	if c == nil {
		return ctx, nil
	}
	capability := ""
	if id != "" {
		capability = "octo-cli/" + service + "/op/" + id
	}
	for cap, targets := range c.targets {
		if capability != "" && cap != capability {
			continue
		}
		for _, target := range targets {
			if target.Method == method && targetPath(target.Path, path) {
				return Child(ctx, cap)
			}
		}
	}
	return ctx, DenyCapability(ctx)
}
func targetPath(template, path string) bool {
	a, b := strings.Split(template, "/"), strings.Split(path, "/")
	if len(a) != len(b) {
		return false
	}
	for j := range a {
		if strings.HasPrefix(a[j], "{") && strings.HasSuffix(a[j], "}") {
			if b[j] == "" {
				return false
			}
		} else if a[j] != b[j] {
			return false
		}
	}
	return true
}

// Close releases the bounded logical-call slot after publication or shutdown.
func (c *Call) Close() {
	if c != nil {
		c.state.end.Do(func() { c.state.stop(); c.state.cancel(); <-c.invocation.calls })
	}
}

// BoundCall returns only the private in-process context handle, never a bearer.
func BoundCall(ctx context.Context) *Call { c, _ := ctx.Value(callContextKey{}).(*Call); return c }

// Child selects a declared fixed-program child, preserving the original turn.
func Child(ctx context.Context, capability string) (context.Context, error) {
	c := BoundCall(ctx)
	if c == nil {
		return ctx, nil
	}
	if capability != c.capability && !c.children[capability] {
		err := refuse("capability_denied")
		c.record(err)
		return ctx, err
	}
	clone := *c
	clone.capability = capability
	return context.WithValue(ctx, callContextKey{}, &clone), nil
}

// DenyCapability refuses an unclassified physical path and records its root.
func DenyCapability(ctx context.Context) error {
	err := refuse("capability_denied")
	if c := BoundCall(ctx); c != nil {
		c.record(err)
	}
	return err
}

func (c *Call) record(err error) {
	if r, ok := err.(*Refusal); ok {
		c.state.Lock()
		if c.state.refusal == nil {
			c.state.refusal = r
		}
		c.state.Unlock()
	}
}

// Refused returns the first typed refusal, including a helper's refusal.
func (c *Call) Refused() *Refusal {
	if c == nil {
		return nil
	}
	c.state.Lock()
	defer c.state.Unlock()
	return c.state.refusal
}
func (c *Call) check(ctx context.Context, start func() error) error {
	if r := c.Refused(); r != nil {
		return r
	}
	err := c.coordinator.atBoundary(ctx, c.invocation, c.capability, start)
	c.record(err)
	return err
}

// Publish performs one synchronous release of sealed bytes to the turn's
// owned output. Every publication after a wait is a separate fresh boundary.
func (c *Call) Publish(ctx context.Context, out io.Writer, payload []byte) error {
	sealed := append([]byte(nil), payload...)
	defer clear(sealed)
	return c.check(ctx, func() error {
		n, err := out.Write(sealed)
		if err != nil || n != len(sealed) {
			return ErrAttemptUnknown
		}
		return nil
	})
}

// PublishConnection releases one sealed response on a host-owned raw socket.
// Socket preparation and HTTP framing happen before this final F/B boundary.
func (c *Call) PublishConnection(ctx context.Context, conn net.Conn, payload []byte) error {
	sealed := append([]byte(nil), payload...)
	defer clear(sealed)
	deadline, ok := ctx.Deadline()
	if !ok {
		return PolicyUnavailable()
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return PolicyUnavailable()
	}
	stop := context.AfterFunc(ctx, func() { closeOwned(conn) })
	defer stop()
	stopTurn := context.AfterFunc(c.invocation.context, func() { closeOwned(conn) })
	defer stopTurn()
	if refusal := c.Refused(); refusal != nil {
		return refusal
	}
	var err error
	if secured, ok := conn.(*tls.Conn); ok {
		wire, owned := secured.NetConn().(*wireCapture)
		if !owned {
			return PolicyUnavailable()
		}
		err = c.coordinator.firstWriteEncoding(ctx, c.invocation, c.capability, wire, func() ([]byte, error) { return sealWire(secured, wire, sealed) }, true)
	} else {
		err = c.coordinator.firstWrite(ctx, c.invocation, c.capability, &wireCapture{Conn: conn}, sealed)
	}
	c.record(err)
	return err
}

// ReadLocal gates an actual confined descriptor open and bounded materialization.
// The opener is the existing descriptor-based upload implementation, selected
// by the host, never by a wire callback or model-provided command.
func (c *Call) ReadLocal(ctx context.Context, open func() (*os.File, error)) (*os.File, func(), error) {
	spool, err := os.CreateTemp("", "octo-mcp-input-")
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	cleanup := func() { closeOwned(spool); removeSpool(spool.Name()) }
	err = c.check(ctx, func() error {
		file, err := open()
		if err != nil {
			return err
		}
		defer closeOwned(file)
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return ErrInvalid
		}
		b, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
		if err != nil || len(b) > 16<<20 {
			return ErrInvalid
		}
		defer clear(b)
		if _, err = spool.Write(b); err != nil {
			return err
		}
		_, err = spool.Seek(0, io.SeekStart)
		return err
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return spool, cleanup, nil
}

func removeSpool(path string) { _ = os.Remove(path) } //nolint:errcheck // private temporary spool cleanup is best effort

// RoundTrip executes the exact request through the coordinator-owned sink.
// It has no net/http transport resend/redirect path. Preparation precedes F/B.
func (c *Call) RoundTrip(ctx context.Context, req *http.Request) (*http.Response, error) {
	select {
	case c.coordinator.attempts <- struct{}{}:
	case <-ctx.Done():
		return nil, PolicyUnavailable()
	}
	release := func() { <-c.coordinator.attempts }
	prepared, err := prepareRequest(ctx, c.invocation.binding.Key, req, c.coordinator.tlsConfig)
	if err != nil {
		release()
		return nil, err
	}
	result, err := c.coordinator.Execute(ctx, c.invocation, prepared, c.capability)
	c.record(err)
	if err != nil {
		release()
	} else {
		result.Response.Body = &releasedBody{ReadCloser: result.Response.Body, release: release}
	}
	return result.Response, err
}

type releasedBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

// Close releases the response and its coordinator attempt slot exactly once.
func (b *releasedBody) Close() error { err := b.ReadCloser.Close(); b.once.Do(b.release); return err }

// AuthorityContext is used only for fixed authenticated identity prerequisites.
// The caller must not use it for business or content requests.
func AuthorityContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, callContextKey{}, (*Call)(nil))
}

// RelayResponseBudgetBytes resolves the effective response byte budget for a
// turn-relay HTTP call from the bound Call, context ResponseBudgets, registered
// PermissionHost endpoint, ticket max_response_bytes, or response header, with
// MaxResponseBudgetBytes (64 MiB) as the hard fail-closed upper bound.
func RelayResponseBudgetBytes(ctx context.Context, endpoint string, ticketMaxBytes int64, headerBudget string) (int64, error) {
	limit := MaxResponseBudgetBytes
	if call := BoundCall(ctx); call != nil {
		if b := call.MaxAcquisitionBytes(); b > 0 && b < limit {
			limit = b
		}
	}
	if b, ok := ResponseBudgetsFromContext(ctx); ok && b.MaxAcquisitionBytes > 0 && b.MaxAcquisitionBytes < limit {
		limit = b.MaxAcquisitionBytes
	}
	if endpoint != "" {
		relayEndpointBudgetsMu.RLock()
		b, ok := relayEndpointBudgets[endpoint]
		relayEndpointBudgetsMu.RUnlock()
		if ok && b.MaxAcquisitionBytes > 0 && b.MaxAcquisitionBytes < limit {
			limit = b.MaxAcquisitionBytes
		}
	}
	if ticketMaxBytes != 0 {
		if ticketMaxBytes < 1 || ticketMaxBytes > MaxResponseBudgetBytes {
			return 0, errors.New("RELAY_TICKET_INVALID")
		}
		if ticketMaxBytes < limit {
			limit = ticketMaxBytes
		}
	}
	if trimmed := strings.TrimSpace(headerBudget); trimmed != "" {
		hb, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil || hb < 1 || hb > MaxResponseBudgetBytes {
			return 0, errors.New("RELAY_RESPONSE_INVALID")
		}
		if hb < limit {
			limit = hb
		}
	}
	return limit, nil
}

// CheckHTTPResponseContentLength inspects Content-Length headers and
// resp.ContentLength before reading resp.Body. When limit > 0 and the declared
// length exceeds limit, it returns overflow=true without reading any body bytes.
func CheckHTTPResponseContentLength(resp *http.Response, limit int64) (observed int64, overflow bool, err error) {
	if resp == nil {
		return 0, false, errors.New("nil HTTP response")
	}
	if limit <= 0 {
		return 0, false, nil
	}
	var seen bool
	var declared int64
	for _, rawHeader := range resp.Header.Values("Content-Length") {
		for _, part := range strings.Split(rawHeader, ",") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			parsed, parseErr := strconv.ParseInt(trimmed, 10, 64)
			if parseErr != nil || parsed < 0 {
				return 0, false, errors.New("invalid Content-Length header")
			}
			if seen && parsed != declared {
				return 0, false, errors.New("conflicting Content-Length headers")
			}
			seen = true
			declared = parsed
		}
	}
	if seen && declared > limit {
		return declared, true, nil
	}
	if resp.ContentLength > limit {
		return resp.ContentLength, true, nil
	}
	return 0, false, nil
}

// ReadBoundedHTTPResponseBody enforces CheckHTTPResponseContentLength and
// io.LimitReader(resp.Body, limit+1) so neither oversized Content-Length nor
// chunked/streaming bodies can allocate or buffer beyond limit+1 bytes.
func ReadBoundedHTTPResponseBody(resp *http.Response, limit int64) (data []byte, observed int64, overflow bool, err error) {
	observed, overflow, err = CheckHTTPResponseContentLength(resp, limit)
	if err != nil || overflow {
		return nil, observed, overflow, err
	}
	if resp.Body == nil {
		return nil, 0, false, nil
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit+1)
	}
	data, err = io.ReadAll(reader)
	observed = int64(len(data))
	if err != nil {
		return nil, observed, false, err
	}
	if limit > 0 && observed > limit {
		return nil, observed, true, nil
	}
	return data, observed, false, nil
}

type boundedPrefixWriter struct {
	buf *bytes.Buffer
	max int64
}

func (w *boundedPrefixWriter) Write(p []byte) (int, error) {
	if room := w.max - int64(w.buf.Len()); room > 0 {
		if int64(len(p)) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

// StreamBoundedHTTPResponseBody enforces CheckHTTPResponseContentLength and
// io.LimitReader(resp.Body, acquisitionLimit+1) on inline binary responses so
// streaming binary downloads cannot bypass MaxAcquisitionBytes while keeping at
// most inlineLimit+1 bytes in memory when within the acquisition budget.
func StreamBoundedHTTPResponseBody(resp *http.Response, inlineLimit, acquisitionLimit int64) (head []byte, size int64, sum []byte, overflow bool, err error) {
	observed, overflow, err := CheckHTTPResponseContentLength(resp, acquisitionLimit)
	if err != nil || overflow {
		return nil, observed, nil, overflow, err
	}
	if resp.Body == nil {
		empty := sha256.Sum256(nil)
		return nil, 0, empty[:], false, nil
	}
	var reader io.Reader = resp.Body
	if acquisitionLimit > 0 {
		reader = io.LimitReader(resp.Body, acquisitionLimit+1)
	}
	h := sha256.New()
	var buf bytes.Buffer
	keep := inlineLimit + 1
	if keep < 1 {
		keep = 1
	}
	n, err := io.Copy(io.MultiWriter(h, &boundedPrefixWriter{buf: &buf, max: keep}), reader)
	if err != nil {
		return nil, n, nil, false, err
	}
	if acquisitionLimit > 0 && n > acquisitionLimit {
		return nil, n, nil, true, nil
	}
	return buf.Bytes(), n, h.Sum(nil), false, nil
}
