//go:build !linux && !darwin && !windows

package mcp

import (
	"errors"
	"os"
)

func openUploadRoot(_ string) (*os.File, error) {
	return nil, errors.New("confined uploads are unsupported on this platform")
}
func openUploadComponent(_ *os.File, _ string, _ bool) (*os.File, error) {
	return nil, errors.New("confined uploads are unsupported on this platform")
}
func openLocalUpload(path string) (*os.File, error) { return os.Open(path) }
