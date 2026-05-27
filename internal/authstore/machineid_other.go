//go:build !darwin && !linux && !windows

package authstore

import "fmt"

// machineID has no portable source on this platform; the key derivation degrades
// to salt-only material (still functional, without off-machine binding).
func machineID() (string, error) {
	return "", fmt.Errorf("machine id unsupported on this platform")
}
