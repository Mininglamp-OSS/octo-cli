//go:build !linux && !darwin

package permission

import "errors"

var errOwnedPlatform = errors.New("permission: owned first-write platform unavailable")

func writeNonblocking(uintptr, []byte) (int, error) { return 0, errOwnedPlatform }
func wouldBlock(err error) bool                     { return err == errOwnedPlatform }
