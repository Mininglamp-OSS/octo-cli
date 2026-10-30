package mcp_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestBuiltBinary_AdmissionStartupRefusals(t *testing.T) {
	bin := buildOctoCLI(t)
	for _, tc := range []struct {
		name   string
		args   []string
		env    string
		reason string
	}{
		{"offline", []string{"--dry-run"}, "", "MCP_OFFLINE_POLICY_UNAVAILABLE"},
		{"orphan file", []string{"--policy-debug-file", "/private/path-canary"}, "", "DEBUG_POLICY_FLAG_INVALID"},
		{"missing file", []string{"--policy-debug"}, "", "DEBUG_POLICY_FLAG_INVALID"},
		{"unsafe file", []string{"--policy-debug", "--policy-debug-file", "/private/path-canary"}, "", "DEBUG_POLICY_FILE_UNSAFE"},
		{"flag OBO", []string{"--force-on-behalf-of", "person-canary"}, "", "OBO_FORBIDDEN"},
		{"environment OBO", nil, "OCTO_FORCE_ON_BEHALF_OF= ", "OBO_FORBIDDEN"},
		{"missing storage", nil, "OCTO_MCP_REQUEST_LOG_DIR=", "REQUEST_LOG_STARTUP_FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"mcp", "serve"}, tc.args...)
			cmd := exec.Command(bin, args...)
			cmd.Env = isolatedEnv(t)
			if tc.env != "" {
				cmd.Env = append(cmd.Env, tc.env)
			}
			cmd.Stdin = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
			out, err := cmd.CombinedOutput()
			if err == nil || !bytes.Contains(out, []byte(tc.reason)) || bytes.Contains(out, []byte("path-canary")) || bytes.Contains(out, []byte("person-canary")) {
				t.Fatalf("startup refusal did not return only the fixed diagnostic: %s", out)
			}
		})
	}
}
