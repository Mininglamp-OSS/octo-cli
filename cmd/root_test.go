package cmd

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// TestNewRootCmd_ParentCommandSkipAuthAndRejectUnknown drives the real
// NewRootCmd so PersistentPreRunE and WrapCLIError are in the loop. Service
// parents must:
//  1. skip the auth gate so help works zero-config, and
//  2. reject unknown subcommands with a validation envelope (not auth_error).
//
// Regression guard for PR #20 review: the parent-`RunE` change made parents
// Runnable() and routed them through the auth gate, turning
// `octo-cli thread bogus` into UNAUTHORIZED instead of "unknown subcommand".
func TestNewRootCmd_ParentCommandSkipAuthAndRejectUnknown(t *testing.T) {
	cases := []struct {
		name          string
		args          []string
		wantErr       bool
		wantErrType   string
		wantErrSubstr string
	}{
		{
			name: "thread no args prints help without a token",
			args: []string{"thread"},
		},
		{
			name:          "thread unknown subcommand -> validation, not auth_error",
			args:          []string{"thread", "bogus"},
			wantErr:       true,
			wantErrType:   "validation",
			wantErrSubstr: "unknown subcommand",
		},
		{
			name:          "thread destroy (unknown leaf) -> validation, not auth_error",
			args:          []string{"thread", "destroy", "g", "s"},
			wantErr:       true,
			wantErrType:   "validation",
			wantErrSubstr: "unknown subcommand",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTestFactoryWithReg()
			// Empty token: the auth gate would fail loudly if it ran. The
			// whole point of the annotation is that it does NOT run for
			// service parents.
			f.SetConfig(&config.Config{APIBaseURL: "http://localhost", BotToken: ""})
			root := NewRootCmd(f.Factory)
			root.SetArgs(tc.args)
			var buf bytes.Buffer
			root.SetOut(&buf)
			root.SetErr(&buf)
			err := root.Execute()

			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected nil error, got %v; output=%s", err, buf.String())
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error, got nil; output=%s", buf.String())
			}
			// Same path main.go uses to derive exit code and envelope shape.
			wrapped := cmdutil.WrapCLIError(err)
			ee := output.AsExitError(wrapped)
			if ee == nil {
				t.Fatalf("expected ExitError after WrapCLIError, got %T: %v", wrapped, wrapped)
			}
			if ee.Type != tc.wantErrType {
				t.Errorf("error type: got %q, want %q (msg=%s)", ee.Type, tc.wantErrType, ee.Message)
			}
			if !strings.Contains(strings.ToLower(ee.Message), tc.wantErrSubstr) {
				t.Errorf("error msg %q should contain %q", ee.Message, tc.wantErrSubstr)
			}
		})
	}
}

// TestNewRootCmd_LeafStillRequiresAuth proves the per-command skipValidation
// annotation does NOT inherit through the parent chain: a leaf operation
// under a service parent (which has the annotation) must still authenticate.
// Without this guard the annotation fix would silently exempt every real
// command from auth.
func TestNewRootCmd_LeafStillRequiresAuth(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{APIBaseURL: "http://localhost", BotToken: ""})
	root := NewRootCmd(f.Factory)
	// `octo-cli group list` is a leaf under `group` (which has the annotation).
	root.SetArgs([]string{"group", "list"})
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected leaf to require auth, got nil; output=%s", buf.String())
	}
	wrapped := cmdutil.WrapCLIError(err)
	ee := output.AsExitError(wrapped)
	if ee == nil {
		t.Fatalf("expected ExitError, got %T: %v", wrapped, wrapped)
	}
	if ee.Type != "auth_error" {
		t.Errorf("leaf should hit auth gate (type=auth_error), got type=%q msg=%s", ee.Type, ee.Message)
	}
}

// TestNewRootCmd_ReservedGlobalFlagNamesMatchPersistentFlags is the drift guard
// for the engine's reserved-flag set.
//
// cmd/service refuses a spec-declared flag whose name is one of root's global
// flags, because cobra merges inherited flags with AddFlagSet and skips a name
// the leaf already has: no panic, but the leaf's local flag wins and the global
// becomes unreachable for that one command. The engine cannot read root's flag
// set — package cmd imports cmd/service, not the reverse — so the names are
// listed there and this test holds the two in step. It is the only place the
// divergence can be reported.
//
// The `-q` shorthand of `--jq` is deliberately absent from the reserved set:
// pflag keeps shorthands in a separate namespace, so a spec param legitimately
// named `q` (matter.list, marketplace plugin.list) does not shadow it.
func TestNewRootCmd_ReservedGlobalFlagNamesMatchPersistentFlags(t *testing.T) {
	f := cmdutil.NewTestFactory()
	f.SetConfig(&config.Config{Format: "json"})
	root := NewRootCmd(f.Factory)

	registered := persistentFlagNames(root)
	reserved := map[string]bool{}
	for _, name := range service.ReservedGlobalFlagNames() {
		reserved[name] = true
	}

	for name := range registered {
		if !reserved[name] {
			t.Errorf("root registers persistent flag --%s but cmd/service does not reserve it; "+
				"a spec param of that name would silently shadow the global for its leaf — "+
				"add it to rootPersistentFlagNames", name)
		}
	}
	for name := range reserved {
		if !registered[name] {
			t.Errorf("cmd/service reserves --%s but root no longer registers it; "+
				"a spec is being refused a name that is free — remove it from rootPersistentFlagNames", name)
		}
	}
}

// persistentFlagNames reads root's persistent flag names out of the rendered
// usage block. Enumerating the flag set directly would mean importing pflag by
// name, which promotes it from an indirect to a direct module requirement for a
// test — the usage text is already the exact list cobra will show a caller, so it
// is the cheaper source for the same assertion.
func persistentFlagNames(root *cobra.Command) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(root.PersistentFlags().FlagUsages(), "\n") {
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "--") {
				continue
			}
			names[strings.TrimSuffix(strings.TrimPrefix(field, "--"), ",")] = true
			break
		}
	}
	return names
}

func TestNewRootCmd_ExplicitCredentialMode(t *testing.T) {
	cases := []struct {
		name, mode, taskToken, token, botToken, wantToken string
		args                                              []string
	}{
		{"task ignores both native tokens", "task", "opaque-task-credential", "bf_canonical", "bf_alias", "opaque-task-credential", []string{"loop", "task", "list", "--workspace-id", "workspace-test"}},
		{"task works without other credentials", "task", "opaque-task-credential", "", "", "opaque-task-credential", []string{"loop", "task", "list", "--workspace-id", "workspace-test"}},
		{"normal Loop keeps native identity", "", "opaque-task-credential", "bf_canonical", "bf_alias", "bf_canonical", []string{"loop", "task", "list", "--workspace-id", "workspace-test"}},
		{"normal documents ignore task token", "", "opaque-task-credential", "", "bf_alias", "bf_alias", []string{"docs", "list"}},
		{"normal drive keeps user key", "", "opaque-task-credential", "uk_user", "bf_alias", "uk_user", []string{"drive", "space", "list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvCredentialMode, tc.mode)
			t.Setenv(config.EnvTaskToken, tc.taskToken)
			t.Setenv(config.EnvToken, tc.token)
			t.Setenv(config.EnvBotToken, tc.botToken)
			t.Setenv(config.EnvSpaceID, "native-space")
			t.Setenv(config.EnvBotID, "")
			blockedStore := t.TempDir() + "/not-a-directory"
			if err := os.WriteFile(blockedStore, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OCTO_CONFIG_DIR", blockedStore)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get("Authorization"); got != "Bearer "+tc.wantToken {
					t.Errorf("Authorization = %q, want explicit mode's credential", got)
				}
				if tc.mode == "task" && r.Header.Get("X-Space-ID") != "" {
					t.Error("task request leaked native Space context")
				}
				if tc.token == "uk_user" && r.URL.Path != "/v1/user/drive/spaces" {
					t.Errorf("user-key path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[]}`)
			}))
			defer server.Close()
			t.Setenv(config.EnvAPIBaseURL, server.URL)
			f := cmdutil.NewDefaultFactory()
			f.IOStreams.Out, f.IOStreams.ErrOut = io.Discard, io.Discard
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if calls != 1 {
				t.Fatalf("request count = %d, want 1", calls)
			}
		})
	}
}

func TestNewRootCmd_TaskModeFailsClosed(t *testing.T) {
	cases := []struct {
		name, taskToken, token, botToken, wantError string
		args                                        []string
	}{
		{"generic task alias is ignored", "", "octo_loop_legacy", "bf_native", "OCTO_TASK_TOKEN", []string{"loop", "task", "list", "--workspace-id", "workspace-test"}},
		{"missing task token fails locally", "", "", "", "OCTO_TASK_TOKEN", []string{"loop", "task", "list", "--workspace-id", "workspace-test"}},
		{"profiles remain blocked", "task-credential", "", "bf_native", "--profile", []string{"loop", "task", "list", "--workspace-id", "workspace-test", "--profile", "host"}},
		{"space overrides remain blocked", "task-credential", "", "", "--space", []string{"loop", "task", "list", "--workspace-id", "workspace-test", "--space", "other"}},
		{"native command cannot switch identity", "task-credential", "bf_native", "", "only allows Loop commands", []string{"docs", "list"}},
		{"auth remains blocked", "task-credential", "", "bf_native", "only allows Loop commands", []string{"auth", "status"}},
		{"config remains blocked", "task-credential", "", "bf_native", "only allows Loop commands", []string{"config", "show"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
			t.Setenv(config.EnvTaskToken, tc.taskToken)
			t.Setenv(config.EnvToken, tc.token)
			t.Setenv(config.EnvBotToken, tc.botToken)
			t.Setenv(config.EnvBotID, "")
			t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
			t.Setenv(config.EnvAPIBaseURL, "http://127.0.0.1:1")
			f := cmdutil.NewDefaultFactory()
			f.IOStreams.Out, f.IOStreams.ErrOut = io.Discard, io.Discard
			root := NewRootCmd(f)
			root.SetArgs(tc.args)
			if err := root.Execute(); err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("Execute error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestNewRootCmd_TaskModeAllowsOfflineDiscovery(t *testing.T) {
	t.Setenv(config.EnvCredentialMode, config.CredentialModeTask)
	f := newTestFactoryWithReg()
	root := NewRootCmd(f.Factory)
	root.SetArgs([]string{"schema", "task.get"})
	if err := root.Execute(); err != nil {
		t.Fatalf("offline schema: %v", err)
	}
}
