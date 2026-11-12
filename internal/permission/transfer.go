package permission

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
)

// OwnedTransferClient retains the existing URL/DNS/proxy/redirect guard but
// replaces its physical transport with one fresh checked socket per hop.
func OwnedTransferClient(ctx context.Context, client *http.Client) (*http.Client, error) {
	if BoundCall(ctx) == nil {
		return client, nil
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.DialContext == nil || transport.Proxy == nil || transport.DialTLSContext != nil {
		return nil, ErrUnavailable
	}
	clone := *client
	clone.Transport = &ownedTransfer{base: transport}
	return &clone, nil
}

type ownedTransfer struct{ base *http.Transport }

// RoundTrip owns one independently checked transfer hop without transport replay.
func (t *ownedTransfer) RoundTrip(req *http.Request) (*http.Response, error) {
	c := BoundCall(req.Context())
	if c == nil || c.capability != "octo-cli/docs/derived/portable-attachment-bytes" || req.Method != "GET" || req.Header.Get("Authorization") != "" {
		return nil, DenyCapability(req.Context())
	}
	ctx := req.Context()
	select {
	case c.coordinator.attempts <- struct{}{}:
	case <-ctx.Done():
		return nil, PolicyUnavailable()
	}
	release := func() { <-c.coordinator.attempts }
	prepared, err := prepareTransfer(ctx, c.invocation.binding.Key, req, t.base)
	if err != nil {
		release()
		return nil, err
	}
	result, err := c.coordinator.Execute(ctx, c.invocation, prepared, c.capability)
	c.record(err)
	if err != nil {
		release()
		return nil, err
	}
	result.Response.Request = req
	result.Response.Body = &releasedBody{ReadCloser: result.Response.Body, release: release}
	return result.Response, nil
}

func socketAddress(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	port := "80"
	if u.Scheme == "https" {
		port = "443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}
func handshake(ctx context.Context, conn net.Conn, u *url.URL, config *tls.Config) (net.Conn, error) {
	conf := &tls.Config{MinVersion: tls.VersionTLS12}
	if config != nil {
		conf = config.Clone()
	}
	if conf.InsecureSkipVerify || conf.ServerName != "" && conf.ServerName != u.Hostname() || conf.MaxVersion != 0 && conf.MaxVersion < tls.VersionTLS12 {
		return conn, ErrInvalid
	}
	if conf.MinVersion < tls.VersionTLS12 {
		conf.MinVersion = tls.VersionTLS12
	}
	conf.ServerName = u.Hostname()
	conf.NextProtos = []string{"http/1.1"}
	tlsConn := tls.Client(conn, conf)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return conn, ErrUnavailable
	}
	return tlsConn, nil
}

func prepareTransfer(ctx context.Context, key Key, req *http.Request, tr *http.Transport) (result *PreparedHTTP, err error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	deadline, ok := ctx.Deadline()
	if !ok || req.URL == nil {
		return nil, ErrUnavailable
	}
	// Proxy performs the source-present target SSRF validation; DialContext
	// connects only its validated pinned addresses (including proxy rebinding).
	proxy, err := tr.Proxy(req)
	if err != nil {
		return nil, err
	}
	target := req.URL
	if proxy != nil {
		target = proxy
	}
	conn, err := tr.DialContext(ctx, "tcp", socketAddress(target))
	if err != nil {
		return nil, ErrUnavailable
	}
	rawWire := &wireCapture{Conn: conn}
	conn = rawWire
	defer func() {
		if result == nil {
			closeOwned(conn)
		}
	}()
	if err = conn.SetDeadline(deadline); err != nil {
		return nil, ErrUnavailable
	}
	if proxy != nil && proxy.Scheme == "https" {
		conn, err = handshake(ctx, conn, proxy, tr.TLSClientConfig)
		if err != nil {
			return nil, err
		}
	}
	proxyRequest := proxy != nil && req.URL.Scheme == "http"
	if proxy != nil && req.URL.Scheme == "https" {
		connect := &http.Request{Method: "CONNECT", URL: &url.URL{Opaque: socketAddress(req.URL)}, Host: socketAddress(req.URL), Header: make(http.Header)}
		if proxy.User != nil {
			password, _ := proxy.User.Password()
			connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
		}
		// This is connection preparation, never a business request or Bot
		// credential-bearing application write to the destination.
		if err = connect.Write(conn); err != nil {
			return nil, ErrUnavailable
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), connect) //nolint:bodyclose // successful CONNECT becomes the owned tunnel; closing its body would close that tunnel
		if err != nil {
			return nil, ErrUnavailable
		}
		if response.StatusCode != 200 {
			closeOwned(response.Body)
			return nil, ErrUnavailable
		}
	}
	if req.URL.Scheme == "https" {
		conn, err = handshake(ctx, conn, req.URL, tr.TLSClientConfig)
		if err != nil {
			return nil, err
		}
	} else if req.URL.Scheme != "http" {
		return nil, ErrInvalid
	}
	copyReq := req.Clone(ctx)
	copyReq.Close = true
	copyReq.Body = http.NoBody
	copyReq.GetBody = nil
	copyReq.ContentLength = 0
	if proxyRequest && proxy.User != nil {
		password, _ := proxy.User.Password()
		copyReq.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxy.User.Username()+":"+password)))
	}
	var sealed bytes.Buffer
	if proxyRequest {
		err = copyReq.WriteProxy(&sealed)
	} else {
		err = copyReq.Write(&sealed)
	}
	if err != nil || sealed.Len() > 16<<20 {
		return nil, ErrInvalid
	}
	ciphertext, err := sealWire(conn, rawWire, sealed.Bytes())
	if err != nil {
		return nil, err
	}
	return &PreparedHTTP{key: key, conn: conn, request: ciphertext, method: req.Method, wire: rawWire}, nil
}

// TransferFailure preserves a root typed refusal through net/http's url.Error.
func TransferFailure(err error) error {
	var r *Refusal
	if errors.As(err, &r) {
		return r
	}
	return err
}
