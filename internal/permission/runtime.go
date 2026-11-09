package permission

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
)

type callContextKey struct{}

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
	refusal *Refusal
	end     sync.Once
	cancel  context.CancelFunc
	stop    func() bool
	pages   map[string]int
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
	call := &Call{coordinator: c, invocation: i, capability: capability, callID: randomID(), argumentsDigest: hex.EncodeToString(sum[:]), children: map[string]bool{}, state: &callState{cancel: cancel, stop: stop, pages: map[string]int{}}}
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

// ConsumeBytes owns insertion of one completed read into helper/derived state.
func (c *Call) ConsumeBytes(ctx context.Context, source []byte) ([]byte, error) {
	var sealed []byte
	err := c.check(ctx, func() error { sealed = append([]byte(nil), source...); return nil })
	return sealed, err
}

// AdmitPage counts every returned item before aggregation. Missing compatible
// source bounds are unavailable, never zero/unlimited or silent truncation.
func AdmitPage(ctx context.Context, count int) error {
	c := BoundCall(ctx)
	if c == nil {
		return nil
	}
	bound, ok := c.coordinator.pageBounds[c.capability]
	if !ok || count < 0 || count > bound.Items {
		err := PolicyUnavailable()
		c.record(err)
		return err
	}
	return c.check(ctx, func() error {
		c.state.Lock()
		defer c.state.Unlock()
		c.state.pages[c.capability]++
		if c.state.pages[c.capability] > bound.Pages {
			return PolicyUnavailable()
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
