//go:build linux || darwin

package mcp

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openPrivatePermissionConfig(dir *os.File, name string) (*os.File, error) {
	return secureFileAt(dir, name, unix.O_RDONLY)
}

func writePermissionReadiness(path, incarnation string) (func(), error) {
	dir, err := secureDirectory(filepath.Dir(path), false)
	if err != nil {
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	name := filepath.Base(path)
	f, err := secureFileAt(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		closePrivateFile(dir)
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	cleanup := func() {
		closePrivateFile(f)
		if err := removeLogFile(dir, name); err != nil {
			closePrivateFile(dir)
			return
		}
		closePrivateFile(dir)
	}
	b, err := permissionReadiness(incarnation)
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = dir.Sync()
	}
	if err != nil {
		cleanup()
		return nil, errors.New("PERMISSION_READINESS_UNAVAILABLE")
	}
	return cleanup, nil
}
