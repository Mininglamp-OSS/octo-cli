package permission

import (
	"bytes"
	"context"
	"net"
	"syscall"
)

// wireCapture completes TLS record construction before F/B, retaining the
// exact encrypted request. Handshake writes pass through before capture starts.
type wireCapture struct {
	net.Conn
	buffer    bytes.Buffer
	capturing bool
}

func (c *wireCapture) Write(b []byte) (int, error) {
	if c.capturing {
		if c.buffer.Len()+len(b) > 17<<20 {
			return 0, ErrInvalid
		}
		return c.buffer.Write(b)
	}
	return c.Conn.Write(b)
}

func sealWire(conn net.Conn, wire *wireCapture, plain []byte) ([]byte, error) {
	wire.capturing = true
	n, err := conn.Write(plain)
	if err != nil || n != len(plain) {
		clear(wire.buffer.Bytes())
		return nil, ErrUnavailable
	}
	return append([]byte(nil), wire.buffer.Bytes()...), nil
}

// firstWrite obtains the owned nonblocking descriptor before acquiring F/B.
// Its callback performs the final policy checks and exactly one OS write. The
// RawConn callback always returns true: EAGAIN cannot enter an implicit poll /
// resend cycle. Remaining bytes of an already-started sealed request are in flight.
func (c *Coordinator) firstWrite(ctx context.Context, i *Invocation, capability string, wire *wireCapture, sealed []byte) error {
	socket, ok := wire.Conn.(syscall.Conn)
	if !ok {
		return PolicyUnavailable()
	}
	raw, err := socket.SyscallConn()
	if err != nil {
		return PolicyUnavailable()
	}
	var boundaryErr error
	sent := 0
	err = raw.Write(func(fd uintptr) bool {
		boundaryErr = c.atBoundary(ctx, i, capability, func() error {
			var err error
			sent, err = writeNonblocking(fd, sealed)
			if wouldBlock(err) && sent == 0 {
				return PolicyUnavailable()
			}
			if err != nil || sent == 0 {
				return ErrAttemptUnknown
			}
			return nil
		})
		return true
	})
	if err != nil && boundaryErr == nil {
		return PolicyUnavailable()
	}
	if boundaryErr != nil {
		return boundaryErr
	}
	// This is the suffix of the same sealed request, never another physical
	// attempt. Failure preserves possible effects and is not retried here.
	if sent < len(sealed) {
		n, err := wire.Conn.Write(sealed[sent:])
		if err != nil || n != len(sealed)-sent {
			return ErrAttemptUnknown
		}
	}
	wire.capturing = false
	clear(wire.buffer.Bytes())
	wire.buffer.Reset()
	return nil
}
