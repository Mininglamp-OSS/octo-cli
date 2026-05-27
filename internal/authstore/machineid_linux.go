//go:build linux

package authstore

import (
	"fmt"
	"os"
	"strings"
)

// machineID returns the systemd/D-Bus machine id, a stable per-install id.
func machineID() (string, error) {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		data, err := os.ReadFile(p)
		if err == nil {
			if id := strings.TrimSpace(string(data)); id != "" {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("machine-id not found")
}
