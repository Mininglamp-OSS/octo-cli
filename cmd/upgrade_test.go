package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const (
	upgradeHandoffEnv = "OCTO_CLI_UPGRADE_HANDOFF"
	upgradeReportEnv  = "OCTO_CLI_UPGRADE_REPORT"
)

type upgradePublicEnvelope struct {
	OK    bool `json:"ok"`
	Data  any  `json:"data"`
	Error *struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
}

func newUpgradeTestFactory() *cmdutil.TestFactory {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{Format: output.FormatJSON})
	return f
}

func setUpgradeTestVersion(t *testing.T) {
	t.Helper()
	previous := Version
	Version = "v0.18.0"
	t.Cleanup(func() { Version = previous })
}

func writeUpgradeResult(t *testing.T, handoffDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(handoffDir, "result.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write upgrade result: %v", err)
	}
}

func decodeUpgradeEnvelope(t *testing.T, raw string) upgradePublicEnvelope {
	t.Helper()
	var envelope upgradePublicEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("decode public envelope: %v\n%s", err, raw)
	}
	return envelope
}

func TestUpgrade_CredentialExemptionIsCommandSpecific(t *testing.T) {
	parent := &cobra.Command{
		Use:         "service",
		Annotations: map[string]string{"skipValidation": "true"},
	}
	leaf := &cobra.Command{Use: "upgrade"}
	parent.AddCommand(leaf)
	if skipValidation(leaf) {
		t.Error("a service leaf named upgrade must require credentials")
	}
	f := newUpgradeTestFactory()
	if !skipValidation(newUpgradeCmd(f.Factory)) {
		t.Error("the local upgrade command must remain credential-free")
	}
}

func TestUpgrade_CredentialFreeLeafRequiresOutputPreflight(t *testing.T) {
	leaf := &cobra.Command{
		Use:         "offline",
		Annotations: map[string]string{"skipValidation": "true"},
	}
	if skipOutputPreflight(leaf, nil) {
		t.Error("a credential-free leaf must validate output options")
	}
	parent := &cobra.Command{
		Use:         "service",
		Annotations: map[string]string{"skipValidation": "true"},
	}
	parent.AddCommand(leaf)
	if !skipOutputPreflight(parent, nil) {
		t.Error("a service group rendering help must skip output preflight")
	}
}

func TestUpgrade_DirectGoInvocationReturnsValidationGuidance(t *testing.T) {
	t.Setenv(upgradeHandoffEnv, "")
	t.Setenv(upgradeReportEnv, "")
	f := newUpgradeTestFactory()

	stdout, stderr, err := execRoot(t, f, "upgrade")
	if err == nil {
		t.Fatal("direct Go upgrade invocation succeeded without launcher handoff")
	}
	if stdout != "" {
		t.Fatalf("direct invocation wrote success output: %q", stdout)
	}
	envelope := decodeUpgradeEnvelope(t, stderr)
	if envelope.OK || envelope.Error == nil {
		t.Fatalf("direct invocation envelope = %+v, want a structured error", envelope)
	}
	if envelope.Error.Type != "validation" {
		t.Fatalf("error type = %q, want validation", envelope.Error.Type)
	}
	guidance := strings.ToLower(envelope.Error.Message + " " + envelope.Error.Hint)
	for _, manager := range []string{"npm", "bun", "pnpm"} {
		if !strings.Contains(guidance, manager) {
			t.Errorf("direct invocation guidance %q does not mention %s", guidance, manager)
		}
	}
	if !strings.Contains(guidance, "global") {
		t.Errorf("direct invocation guidance %q does not identify the global launcher", guidance)
	}
}

func TestUpgrade_HandoffWritesRequestWithoutCredentialsOrPublicOutput(t *testing.T) {
	setUpgradeTestVersion(t)
	handoffDir := t.TempDir()
	t.Setenv(upgradeHandoffEnv, handoffDir)
	t.Setenv(upgradeReportEnv, "")
	f := newUpgradeTestFactory()
	credentialLookups := 0
	f.CredentialFunc = func() (*credential.BotCredential, error) {
		credentialLookups++
		return nil, errors.New("upgrade must not resolve an Octo credential")
	}

	stdout, stderr, err := execRoot(t, f, "upgrade", "--dry-run")
	if err != nil {
		t.Fatalf("handoff invocation: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("private handoff produced public output: stdout=%q stderr=%q", stdout, stderr)
	}
	if credentialLookups != 0 {
		t.Fatalf("credential resolutions = %d, want 0", credentialLookups)
	}

	requestPath := filepath.Join(handoffDir, "request.json")
	body, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatalf("read handoff request: %v", err)
	}
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatalf("decode handoff request: %v\n%s", err, body)
	}

	var version string
	if err := json.Unmarshal(request["version"], &version); err != nil {
		t.Fatalf("decode request version: %v", err)
	}
	if version != "0.18.0" {
		t.Errorf("request version = %q, want normalized build version 0.18.0", version)
	}
	var dryRun bool
	if err := json.Unmarshal(request["dry_run"], &dryRun); err != nil {
		t.Fatalf("decode request dry_run: %v", err)
	}
	if !dryRun {
		t.Errorf("request dry_run = false, want true")
	}
}

func TestUpgrade_PreflightAndTaskModeRefuseHandoff(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		mode       string
		wantReason string
	}{
		{
			name:       "invalid output format",
			args:       []string{"--format", "unsupported", "upgrade"},
			wantReason: `unknown format "unsupported"`,
		},
		{
			name:       "invalid jq expression",
			args:       []string{"--jq", "[", "upgrade"},
			wantReason: "jq expression could not be parsed",
		},
		{
			name:       "positional argument",
			args:       []string{"upgrade", "unexpected"},
			wantReason: "unexpected",
		},
		{
			name:       "Loop task mode",
			args:       []string{"upgrade"},
			mode:       config.CredentialModeTask,
			wantReason: "only allows Loop commands",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handoffDir := t.TempDir()
			t.Setenv(upgradeHandoffEnv, handoffDir)
			t.Setenv(upgradeReportEnv, "")
			t.Setenv(config.EnvCredentialMode, tc.mode)
			f := newUpgradeTestFactory()

			_, _, err := execRoot(t, f, tc.args...)
			if err == nil {
				t.Fatal("refused upgrade invocation succeeded")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantReason)) {
				t.Errorf("error = %q, want reason %q", err, tc.wantReason)
			}
			requestPath := filepath.Join(handoffDir, "request.json")
			if _, statErr := os.Stat(requestPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("handoff request stat error = %v, want file absent", statErr)
			}
		})
	}
}

func TestUpgrade_ReportUsesJQAndRequestedFormat(t *testing.T) {
	handoffDir := t.TempDir()
	t.Setenv(upgradeHandoffEnv, handoffDir)
	t.Setenv(upgradeReportEnv, "1")
	writeUpgradeResult(t, handoffDir, `{"data":{"manager":"pnpm","previous_version":"0.18.0","target_version":"0.19.0","current_version":"0.19.0","updated":true}}`)
	f := newUpgradeTestFactory()

	stdout, stderr, err := execRoot(t, f,
		"--format", "csv",
		"--jq", ".data | {target_version: .target_version, updated: .updated}",
		"upgrade",
	)
	if err != nil {
		t.Fatalf("private success report: %v", err)
	}
	if stderr != "" {
		t.Fatalf("private success report wrote stderr: %q", stderr)
	}
	if want := "target_version,updated\n0.19.0,true\n"; stdout != want {
		t.Fatalf("formatted jq output = %q, want %q", stdout, want)
	}
}

func TestUpgrade_ReportTreatsEmptyJQResultAsSuccess(t *testing.T) {
	handoffDir := t.TempDir()
	t.Setenv(upgradeHandoffEnv, handoffDir)
	t.Setenv(upgradeReportEnv, "1")
	writeUpgradeResult(t, handoffDir, `{"data":{"manager":"npm","previous_version":"0.18.0","target_version":"0.19.0","current_version":"0.19.0","updated":true}}`)
	f := newUpgradeTestFactory()

	_, stderr, err := execRoot(t, f, "--jq", "empty", "upgrade")
	if err != nil {
		t.Fatalf("empty jq result was treated as failure: %v", err)
	}
	if stderr != "" {
		t.Fatalf("successful empty jq result wrote stderr: %q", stderr)
	}
}

func TestUpgrade_ReportPreservesStructuredErrors(t *testing.T) {
	handoffDir := t.TempDir()
	t.Setenv(upgradeHandoffEnv, handoffDir)
	t.Setenv(upgradeReportEnv, "1")
	writeUpgradeResult(t, handoffDir, `{"error":{"type":"validation","code":"UPGRADE_NOT_GLOBAL","message":"installation is not a supported global package-manager install","hint":"install octo-cli globally with npm, Bun, or pnpm","exit_code":2}}`)
	f := newUpgradeTestFactory()

	stdout, stderr, err := execRoot(t, f, "upgrade")
	if err == nil {
		t.Fatal("private error report succeeded")
	}
	if stdout != "" {
		t.Fatalf("private error report wrote stdout: %q", stdout)
	}
	envelope := decodeUpgradeEnvelope(t, stderr)
	if envelope.OK || envelope.Error == nil {
		t.Fatalf("private error envelope = %+v, want a structured error", envelope)
	}
	if envelope.Error.Type != "validation" || envelope.Error.Code != "UPGRADE_NOT_GLOBAL" {
		t.Fatalf("private error type/code = %q/%q, want validation/UPGRADE_NOT_GLOBAL", envelope.Error.Type, envelope.Error.Code)
	}
	if envelope.Error.Message != "installation is not a supported global package-manager install" {
		t.Errorf("private error message = %q", envelope.Error.Message)
	}
	if envelope.Error.Hint != "install octo-cli globally with npm, Bun, or pnpm" {
		t.Errorf("private error hint = %q", envelope.Error.Hint)
	}
	structured := output.AsExitError(cmdutil.WrapCLIError(err))
	if structured == nil {
		t.Fatalf("returned error lost structured taxonomy: %T", err)
	}
	if got := structured.ExitCode(); got != 2 {
		t.Errorf("structured error exit code = %d, want 2", got)
	}
}

func TestUpgrade_ReportPreservesApiErrorTaxonomy(t *testing.T) {
	handoffDir := t.TempDir()
	t.Setenv(upgradeHandoffEnv, handoffDir)
	t.Setenv(upgradeReportEnv, "1")
	writeUpgradeResult(t, handoffDir, `{"error":{"type":"api_error","code":"UPGRADE_MANAGER_FAILED","message":"the package manager could not complete the update","hint":"retry through the owning package manager","exit_code":1}}`)
	f := newUpgradeTestFactory()

	_, stderr, err := execRoot(t, f, "upgrade")
	if err == nil {
		t.Fatal("private API error report succeeded")
	}
	envelope := decodeUpgradeEnvelope(t, stderr)
	if envelope.Error == nil || envelope.Error.Type != "api_error" || envelope.Error.Code != "UPGRADE_MANAGER_FAILED" {
		t.Fatalf("private API error = %+v, want api_error/UPGRADE_MANAGER_FAILED", envelope.Error)
	}
	if structured := output.AsExitError(err); structured == nil || structured.Type != "api_error" || structured.ExitCode() != 1 {
		t.Fatalf("returned API error = %v, want api_error with exit code 1", err)
	}
}
