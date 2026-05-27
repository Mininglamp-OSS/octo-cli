//go:build !darwin && !linux && !windows

package authstore

// machineID has no portable source on this platform; the key derivation degrades
// to salt-only material (still functional, without off-machine binding). The
// sentinel error tells deriveKey this is expected, not a transient failure.
func machineID() (string, error) {
	return "", errMachineIDUnsupported
}
