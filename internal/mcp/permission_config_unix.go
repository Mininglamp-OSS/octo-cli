//go:build linux || darwin

package mcp

import (
	"golang.org/x/sys/unix"
	"os"
)

func openPrivatePermissionConfig(dir *os.File, name string) (*os.File, error) {
	return secureFileAt(dir, name, unix.O_RDONLY)
}
