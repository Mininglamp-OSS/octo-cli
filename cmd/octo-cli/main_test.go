package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hermeticEnv returns the inherited environment with every OCTO_-prefixed
// variable removed, followed by extra. Subprocess tests must not read the
// developer's own credentials or output settings: the CLI resolves a token from
// an ordered variable list (internal/credential/env_provider.go) and also reads
// OCTO_CONFIG_DIR, OCTO_SPACE_ID and OCTO_FORMAT, so neutralising names one by
// one silently drifts out of date whenever a variable is added. Sweeping the
// prefix and re-adding only what a test needs keeps the list explicit.
func hermeticEnv(extra ...string) []string {
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+len(extra))
	for _, kv := range inherited {
		if strings.HasPrefix(kv, "OCTO_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

// TestMain_VersionCommand builds the binary and runs `octo-cli version` as a
// subprocess so the real main() path (including signal wiring, error
// classification, and os.Exit) is exercised.
func TestMain_VersionCommand(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "octo-cli")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "version")
	cmd.Env = hermeticEnv()
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
	bin := filepath.Join(t.TempDir(), "octo-cli")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "no-such-command")
	cmd.Env = hermeticEnv()
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
	bin := filepath.Join(t.TempDir(), "octo-cli")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build: %v", err)
	}

	cmd := exec.Command(bin, "event", "list")
	// Drop every OCTO_ token/selector variable and point at an empty credential
	// store so no profile and no env token resolve, and Validate trips on the
	// missing token instead of the command reaching the network.
	cmd.Env = hermeticEnv("OCTO_CONFIG_DIR=" + t.TempDir())
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
