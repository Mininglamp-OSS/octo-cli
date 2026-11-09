//go:build !linux && !darwin

package mcp

import (
	"errors"
	"os"
)

func openPrivatePermissionConfig(*os.File, string) (*os.File, error) {
	return nil, errors.New("private host config platform unavailable")
}
