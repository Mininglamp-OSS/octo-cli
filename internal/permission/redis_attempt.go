package permission

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// RedisEndpoint is trusted secret-issuer configuration, never model metadata.
type RedisEndpoint struct {
	Address, Username, Password string
	Database                    int
	TLS                         *tls.Config
}

func redisCommand(parts ...string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, part := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(part), part)
	}
	return b.Bytes()
}

// IssueSecret owns one fresh Redis command connection and one EVAL write.
// Authentication/database selection are completed before final facts/policy.
// There is no library reconnect, retry or script-cache fallback after that gate.
func (c *Call) IssueSecret(ctx context.Context, endpoint RedisEndpoint, script, key, payload string, ttl int64) (int64, error) { //nolint:gocyclo // closed fixed-program branches retain explicit checks in security order
	if c.capability != "octo-cli/bot/secret/get_secret_download_url" || endpoint.Database < 0 || ttl < 1 {
		return 0, DenyCapability(ctx)
	}
	sealed := redisCommand("EVAL", script, "1", key, payload, strconv.FormatInt(ttl, 10))
	if len(sealed) > 16<<20 {
		return 0, ErrInvalid
	}
	defer clear(sealed)
	select {
	case c.coordinator.attempts <- struct{}{}:
	case <-ctx.Done():
		return 0, PolicyUnavailable()
	}
	defer func() { <-c.coordinator.attempts }()
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0, ErrUnavailable
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", endpoint.Address)
	if err != nil {
		return 0, ErrUnavailable
	}
	rawWire := &wireCapture{Conn: conn}
	conn = rawWire
	defer closeOwned(conn)
	if err = conn.SetDeadline(deadline); err != nil {
		return 0, ErrUnavailable
	}
	if endpoint.TLS != nil {
		host, _, err := net.SplitHostPort(endpoint.Address)
		if err != nil {
			return 0, ErrUnavailable
		}
		conf := endpoint.TLS.Clone()
		if conf.InsecureSkipVerify || conf.ServerName != "" && conf.ServerName != host {
			return 0, ErrInvalid
		}
		if conf.MinVersion < tls.VersionTLS12 {
			conf.MinVersion = tls.VersionTLS12
		}
		conf.ServerName = host
		secured := tls.Client(conn, conf)
		if err = secured.HandshakeContext(ctx); err != nil {
			return 0, ErrUnavailable
		}
		conn = secured
	}
	reader := bufio.NewReaderSize(conn, 1024)
	prerequisite := func(command []byte) error {
		defer clear(command)
		if _, err := conn.Write(command); err != nil {
			return ErrUnavailable
		}
		line, err := reader.ReadSlice('\n')
		if err != nil || string(line) != "+OK\r\n" {
			return ErrUnavailable
		}
		return nil
	}
	if endpoint.Password != "" {
		parts := []string{"AUTH", endpoint.Password}
		if endpoint.Username != "" {
			parts = []string{"AUTH", endpoint.Username, endpoint.Password}
		}
		if err := prerequisite(redisCommand(parts...)); err != nil {
			return 0, err
		}
	}
	if endpoint.Database != 0 {
		if err := prerequisite(redisCommand("SELECT", strconv.Itoa(endpoint.Database))); err != nil {
			return 0, err
		}
	}
	ciphertext, err := sealWire(conn, rawWire, sealed)
	if err != nil {
		return 0, err
	}
	defer clear(ciphertext)
	err = c.coordinator.firstWrite(ctx, c.invocation, c.capability, rawWire, ciphertext)
	c.record(err)
	if err != nil {
		return 0, err
	}
	lineBytes, err := reader.ReadSlice('\n')
	line := string(lineBytes)
	if err != nil || len(line) > 128 || !strings.HasPrefix(line, ":") || !strings.HasSuffix(line, "\r\n") {
		return 0, ErrAttemptUnknown
	}
	n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(line, ":"), "\r\n"), 10, 64)
	if err != nil {
		return 0, ErrAttemptUnknown
	}
	return n, nil
}
