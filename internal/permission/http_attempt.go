package permission

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// PreparedHTTP owns one fresh, already-handshaken HTTP/1.1 connection and one
// immutable serialized request. There is no http.Transport, pool, redirect,
// proxy, authentication replay, GetBody callback or retry loop in this sink.
// It is a component seam, not the business client's production transport.
type PreparedHTTP struct {
	key     Key
	conn    net.Conn
	request []byte
	method  string
	used    atomic.Bool
}

// PrepareHTTP finishes connection/TLS and request serialization before F/B.
// The only accepted target authority is the configured exact policy origin.
// Redirects and helper URL requests need separate, as-yet-unwired sinks with
// their existing SSRF/credential-forwarding contracts; this method grants none.
func PrepareHTTP(ctx context.Context, key Key, method, target string, headers http.Header, body []byte, tlsConfig *tls.Config, deadline time.Time) (*PreparedHTTP, error) { //nolint:gocyclo // all target, immutable spool and TLS checks must precede the first application write
	if !key.Valid() || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || len(body) > 16<<20 || deadline.IsZero() || !deadline.After(time.Now()) {
		return nil, ErrInvalid
	}
	u, err := url.Parse(key.Origin + target)
	if err != nil || u.Scheme+"://"+u.Host != key.Origin || u.Fragment != "" || u.User != nil {
		return nil, ErrInvalid
	}
	req, err := http.NewRequest(method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header = headers.Clone()
	req.Close = true
	var sealed bytes.Buffer
	if err = req.Write(&sealed); err != nil || sealed.Len() > 16<<20 {
		return nil, ErrInvalid
	}
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		conf = tlsConfig.Clone()
	}
	if conf.InsecureSkipVerify || conf.ServerName != "" && conf.ServerName != u.Hostname() {
		return nil, ErrInvalid
	}
	conf.ServerName = u.Hostname()
	conf.NextProtos = []string{"http/1.1"}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "443")
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	d := tls.Dialer{NetDialer: &net.Dialer{}, Config: conf}
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, ErrUnavailable
	}
	if err = conn.SetDeadline(deadline); err != nil {
		closeOwned(conn)
		return nil, ErrUnavailable
	}
	return &PreparedHTTP{key: key, conn: conn, request: sealed.Bytes(), method: method}, nil
}

// Abort consumes an unused action and closes its connection. No application
// request bytes were sent. It cannot roll back an already-started attempt.
func (p *PreparedHTTP) Abort() {
	if p != nil && p.used.CompareAndSwap(false, true) {
		closeOwned(p.conn)
		clear(p.request)
		p.request = nil
	}
}

// AttemptResult reports only this physical attempt, independently of refusal data.
type AttemptResult struct {
	// sent means the sealed request was written and a response was parsed,
	// not that the downstream business operation succeeded. No error output
	// includes the URL, headers, request bytes or a transport error string.
	Status   string
	Response *http.Response
}

// ErrAttemptUnknown preserves uncertainty after an owned write or response loss.
var ErrAttemptUnknown = errors.New("permission: physical attempt result unknown")

// Execute consumes p even on refusal. The exact-key watermark remains locked
// across the first synchronous application Write, so accepting a newer bundle
// cannot interleave between the check and this write. Remaining request bytes
// are part of that same sealed in-flight request. A write failure is never
// retried, including a reported zero-byte write. Response loss stays unknown.
// Response consumption and publication still require caller-owned bounds and
// a separate checked publication integration; this method is not a full MCP
// permission implementation or an OS/nonblocking/TLS I-SEND acceptance proof.
func (c *Coordinator) Execute(ctx context.Context, i *Invocation, p *PreparedHTTP, capability string) (AttemptResult, error) {
	if p == nil || !p.used.CompareAndSwap(false, true) {
		return AttemptResult{Status: "refused"}, refuse("context_untrusted")
	}
	defer func() { clear(p.request); p.request = nil }()
	if i == nil || p.key != i.binding.Key {
		closeOwned(p.conn)
		return AttemptResult{Status: "refused"}, refuse("context_untrusted")
	}
	stop := context.AfterFunc(ctx, func() { closeOwned(p.conn) })
	defer stop()
	err := c.atBoundary(ctx, i, capability, func() error {
		// Deliberately call the connection exactly once. A short write is an
		// unknown partial attempt, never an instruction to resend the suffix.
		n, err := p.conn.Write(p.request)
		if err != nil || n != len(p.request) {
			return ErrAttemptUnknown
		}
		return nil
	})
	if err != nil {
		closeOwned(p.conn)
		status := "unavailable"
		if IsRefusal(err) {
			status = "refused"
		}
		if errors.Is(err, ErrAttemptUnknown) {
			status = "result_unknown"
		}
		return AttemptResult{Status: status}, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(p.conn), &http.Request{Method: p.method}) //nolint:bodyclose // caller closes the returned connectionBody and its socket
	if err != nil {
		closeOwned(p.conn)
		return AttemptResult{Status: "result_unknown"}, ErrAttemptUnknown
	}
	resp.Body = &connectionBody{ReadCloser: resp.Body, conn: p.conn}
	return AttemptResult{Status: "sent", Response: resp}, nil
}

type connectionBody struct {
	io.ReadCloser
	conn net.Conn
}

// Close releases both the response body and its single-attempt connection.
func (b *connectionBody) Close() error { err := b.ReadCloser.Close(); closeOwned(b.conn); return err }

func closeOwned(c io.Closer) { _ = c.Close() } //nolint:errcheck // best-effort cleanup cannot change permission or erase uncertainty
