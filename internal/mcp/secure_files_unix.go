//go:build linux || darwin

package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func secureStat(f *os.File, directory, exclusive bool) error {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		return errors.New("unsafe file")
	}
	uid := uint32(os.Geteuid())
	if directory {
		if st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Uid != 0 && st.Uid != uid || st.Mode&0o022 != 0 {
			return errors.New("unsafe directory")
		}
		if exclusive && (st.Uid != uid || st.Mode&0o777 != 0o700) {
			return errors.New("unsafe directory")
		}
	} else if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uid || st.Mode&0o777 != 0o600 || st.Nlink != 1 {
		return errors.New("unsafe file")
	}
	return nil
}

func secureDirectory(path string, exclusive bool) (*os.File, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return nil, errors.New("unsafe directory")
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return nil, errors.New("unsafe directory")
		}
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	dir := os.NewFile(uintptr(fd), "/")
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/") {
		if part == "" {
			continue
		}
		next, e := openUploadComponent(dir, part, true)
		closePrivateFile(dir)
		if e != nil {
			return nil, e
		}
		dir = next
		if e = secureStat(dir, true, false); e != nil {
			closePrivateFile(dir)
			return nil, e
		}
	}
	if err = secureStat(dir, true, exclusive); err != nil {
		closePrivateFile(dir)
		return nil, err
	}
	return dir, nil
}

func secureFileAt(dir *os.File, name string, flags int) (*os.File, error) {
	if name == "" || filepath.Base(name) != name {
		return nil, errors.New("unsafe file")
	}
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if err = secureStat(f, false, false); err != nil {
		closePrivateFile(f)
		return nil, err
	}
	return f, nil
}
func logOpenAt(dir *os.File, name string, create bool) (*os.File, error) {
	flags := unix.O_RDWR
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	return secureFileAt(dir, name, flags)
}
func lockLogDirectory(dir *os.File) (*os.File, error) {
	f, e := secureFileAt(dir, ".lock", unix.O_RDWR|unix.O_CREAT)
	if e != nil {
		return nil, e
	}
	if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		closePrivateFile(f)
		return nil, e
	}
	return f, nil
}
func renameLogFile(dir *os.File, from, to string) error {
	return unix.Renameat(int(dir.Fd()), from, int(dir.Fd()), to)
}
func removeLogFile(dir *os.File, name string) error { return unix.Unlinkat(int(dir.Fd()), name, 0) }
func sameDebugFile(dir, f *os.File, name string, before os.FileInfo) bool {
	end, err := f.Stat()
	if err != nil || !os.SameFile(before, end) || end.Size() != before.Size() || !end.ModTime().Equal(before.ModTime()) || secureStat(f, false, false) != nil {
		return false
	}
	current, err := secureFileAt(dir, name, unix.O_RDONLY)
	if err != nil {
		return false
	}
	defer closePrivateFile(current)
	st, err := current.Stat()
	return err == nil && os.SameFile(end, st) && st.Size() == end.Size() && st.ModTime().Equal(end.ModTime())
}
func openDebugAt(dir *os.File, name string) (*os.File, error) {
	return secureFileAt(dir, name, unix.O_RDONLY)
}
