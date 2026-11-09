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
// Managed business calls use this sink; ordinary CLI calls retain their client.
type PreparedHTTP struct {
	key     Key
	conn    net.Conn
	request []byte
	method  string
	wire    *wireCapture
	used    atomic.Bool
}

// PrepareHTTP finishes connection/TLS and request serialization before F/B.
// The only accepted target authority is the configured exact policy origin.
// Helper URL requests use the separate owned transfer sink with their existing
// SSRF/credential-forwarding contracts; this method grants none.
func PrepareHTTP(ctx context.Context, key Key, method, target string, headers http.Header, body []byte, tlsConfig *tls.Config, deadline time.Time) (*PreparedHTTP, error) { //nolint:gocyclo // all target, immutable spool and TLS checks must precede the first application write
	if !key.Valid() || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || deadline.IsZero() || !deadline.After(time.Now()) {
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
	if err = req.Write(&sealed); err != nil {
		return nil, ErrInvalid
	}
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if tlsConfig != nil {
		conf = tlsConfig.Clone()
	}
	if conf.InsecureSkipVerify || conf.ServerName != "" && conf.ServerName != u.Hostname() || conf.MaxVersion != 0 && conf.MaxVersion < tls.VersionTLS12 {
		return nil, ErrInvalid
	}
	if conf.MinVersion < tls.VersionTLS12 {
		conf.MinVersion = tls.VersionTLS12
	}
	conf.ServerName = u.Hostname()
	conf.NextProtos = []string{"http/1.1"}
	addr := u.Host
	if u.Port() == "" {
		addr = net.JoinHostPort(u.Hostname(), "443")
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	rawConn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, ErrUnavailable
	}
	wire := &wireCapture{Conn: rawConn}
	conn := tls.Client(wire, conf)
	if err = rawConn.SetDeadline(deadline); err != nil {
		closeOwned(rawConn)
		return nil, ErrUnavailable
	}
	if err = conn.HandshakeContext(dialCtx); err != nil {
		closeOwned(conn)
		return nil, ErrUnavailable
	}
	ciphertext, err := sealWire(conn, wire, sealed.Bytes())
	if err != nil {
		closeOwned(conn)
		return nil, err
	}
	return &PreparedHTTP{key: key, conn: conn, request: ciphertext, method: method, wire: wire}, nil
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
	err := c.firstWrite(ctx, i, capability, p.wire, p.request)
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
	// Match the ordinary client's zero MaxResponseHeaderBytes setting in
	// net/http.Transport (10MiB). This is a header parsing budget, not a new
	// business response-body limit. Buffered body bytes remain readable after
	// the parser completes; the caller retains its source-specific body contract.
	headerReader := &responseHeaderReader{Reader: p.conn, remaining: 10 << 20}
	resp, err := http.ReadResponse(bufio.NewReader(headerReader), &http.Request{Method: p.method}) //nolint:bodyclose // caller closes the returned connectionBody and its socket
	if err != nil {
		closeOwned(p.conn)
		return AttemptResult{Status: "result_unknown"}, ErrAttemptUnknown
	}
	headerReader.remaining = -1
	resp.Body = &connectionBody{ReadCloser: resp.Body, conn: p.conn}
	return AttemptResult{Status: "sent", Response: resp}, nil
}

type responseHeaderReader struct {
	io.Reader
	remaining int64
}

func (r *responseHeaderReader) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return r.Reader.Read(p)
	}
	if r.remaining == 0 {
		return 0, ErrUnavailable
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.Reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

type connectionBody struct {
	io.ReadCloser
	conn net.Conn
}

// Close releases both the response body and its single-attempt connection.
func (b *connectionBody) Close() error { err := b.ReadCloser.Close(); closeOwned(b.conn); return err }

func closeOwned(c io.Closer) { _ = c.Close() } //nolint:errcheck // best-effort cleanup cannot change permission or erase uncertainty

func prepareRequest(ctx context.Context, key Key, req *http.Request, conf *tls.Config) (*PreparedHTTP, error) {
	if req.URL == nil || req.URL.Scheme+"://"+req.URL.Host != key.Origin {
		return nil, refuse("context_untrusted")
	}
	var body []byte
	if req.Body != nil {
		defer closeOwned(req.Body)
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, ErrInvalid
		}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, ErrUnavailable
	}
	return PrepareHTTP(ctx, key, req.Method, req.URL.RequestURI(), req.Header, body, conf, deadline)
}
