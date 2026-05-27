package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain_VersionCommand builds the binary and runs `octo version` as a
// subprocess so the real main() path (including signal wiring, error
// classification, and os.Exit) is exercised.
func TestMain_VersionCommand(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "octo")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "version")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if !strings.Contains(string(out), `"ok": true`) {
		t.Errorf("envelope missing: %s", out)
	}
	if !strings.Contains(string(out), `"version"`) {
		t.Errorf("version field missing: %s", out)
	}
}

// TestMain_UnknownCommandExitCode confirms that cobra framework errors reach
// main() and are wrapped into a validation-exit-code=2 envelope on stderr.
func TestMain_UnknownCommandExitCode(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "octo")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "no-such-command")
	// stderr carries the envelope for framework errors.
	errOut, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2 (validation)", exitErr.ExitCode())
	}
	if !strings.Contains(string(errOut), "validation") {
		t.Errorf("envelope missing validation type:\n%s", errOut)
	}
}

// TestMain_MissingTokenExitCode verifies that the missing-token
// PersistentPreRunE error produces exit code 3 (auth_error).
func TestMain_MissingTokenExitCode(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "octo")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "matter", "list")
	// Clear OCTO_BOT_TOKEN / OCTO_BOT_ID and point at an empty credential store
	// so no profile resolves and Validate trips on the missing token.
	cmd.Env = append(os.Environ(),
		"OCTO_BOT_TOKEN=", "OCTO_BOT_ID=", "OCTO_CONFIG_DIR="+t.TempDir())
	errOut, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	if exitErr.ExitCode() != 3 {
		t.Errorf("exit code = %d, want 3 (auth)", exitErr.ExitCode())
	}
	if !strings.Contains(string(errOut), "auth_error") {
		t.Errorf("envelope missing auth_error:\n%s", errOut)
	}
}
