//go:build !linux && !darwin

package mcp

import (
	"errors"
	"os"
)

func secureDirectory(string, bool) (*os.File, error) {
	return nil, errors.New("POSIX storage required")
}
func openDebugAt(*os.File, string) (*os.File, error) {
	return nil, errors.New("POSIX storage required")
}
func sameDebugFile(*os.File, *os.File, string, os.FileInfo) bool { return false }
func logOpenAt(*os.File, string, bool) (*os.File, error) {
	return nil, errors.New("POSIX storage required")
}
func lockLogDirectory(*os.File) (*os.File, error)  { return nil, errors.New("POSIX storage required") }
func renameLogFile(*os.File, string, string) error { return errors.New("POSIX storage required") }
func removeLogFile(*os.File, string) error         { return errors.New("POSIX storage required") }
