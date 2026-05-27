//go:build darwin

package authstore

import (
	"fmt"
	"os/exec"
	"regexp"
)

var ioPlatformUUIDRe = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// machineID returns the macOS IOPlatformUUID (a stable per-machine identifier).
func machineID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("ioreg: %w", err)
	}
	m := ioPlatformUUIDRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("IOPlatformUUID not found")
	}
	return string(m[1]), nil
}
