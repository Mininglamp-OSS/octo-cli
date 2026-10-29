//go:build linux || darwin

package mcp

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func openUploadComponent(parent *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), filepath.Join(parent.Name(), name)), nil
}

func openUploadRoot(path string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	root := os.NewFile(uintptr(fd), "/")
	if path == "/" {
		return root, nil
	}
	defer root.Close() //nolint:errcheck // release the anchor after the walk
	return walkUpload(root, strings.TrimPrefix(path, "/"), func(parent *os.File, name string, _ bool) (*os.File, error) {
		return openUploadComponent(parent, name, true)
	})
}

func openLocalUpload(path string) (*os.File, error) {
	// The stdio opt-in permits symlinks/unconfined regular files, but a FIFO
	// must not block before fstat has had a chance to reject it.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
