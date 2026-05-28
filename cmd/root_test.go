package cmd

import (
	"bytes"
	"strings"
	"testing"

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
// `octo thread bogus` into UNAUTHORIZED instead of "unknown subcommand".
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
			name:          "thread delete (removed) -> validation, not auth_error",
			args:          []string{"thread", "delete", "g", "s"},
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
	// `octo group list` is a leaf under `group` (which has the annotation).
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
