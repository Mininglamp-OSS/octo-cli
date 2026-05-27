//go:build windows

package authstore

import (
	"fmt"
	"os/exec"
	"strings"
)

// machineID returns the Windows MachineGuid from the registry, a stable
// per-install identifier.
func machineID() (string, error) {
	out, err := exec.Command("reg", "query",
		`HKLM\SOFTWARE\Microsoft\Cryptography`, "/v", "MachineGuid").Output()
	if err != nil {
		return "", fmt.Errorf("reg query: %w", err)
	}
	// Output line looks like: "    MachineGuid    REG_SZ    <guid>"
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "MachineGuid") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 {
			return fields[len(fields)-1], nil
		}
	}
	return "", fmt.Errorf("MachineGuid not found")
}
