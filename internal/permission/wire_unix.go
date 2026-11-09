//go:build linux || darwin

package permission

import (
	"errors"

	"golang.org/x/sys/unix"
)

func writeNonblocking(fd uintptr, b []byte) (int, error) { return unix.Write(int(fd), b) }
func wouldBlock(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)
}
