package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const outputPreflightMessageBody = `{"channel_id":"channel-1","channel_type":1,"payload":{"content":"hello"}}`

func newOutputPreflightServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"client_msg_no":"client-1","message_id":42,"message_seq":7}`)
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func newOutputPreflightTestFactory(serverURL string) *cmdutil.TestFactory {
	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: serverURL, BotToken: "app_preflight", Format: output.FormatJSON}
	cred := &credential.BotCredential{Token: "app_preflight", Source: "test"}
	f.SetConfig(cfg)
	f.SetCredential(cred)
	f.SetClient(client.New(cfg, cred, client.Options{}))
	return f
}

func executeOutputPreflightRoot(t *testing.T, f *cmdutil.Factory, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	f.IOStreams.Out = &stdout
	f.IOStreams.ErrOut = &stderr
	root := NewRootCmd(f)
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	returnOutput := root.ExecuteContext(context.Background())
	return stdout.String(), returnOutput
}

func requireOutputConfigError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected output option error")
	}
	ee := output.AsExitError(cmdutil.WrapCLIError(err))
	if ee == nil {
		t.Fatalf("error did not retain the CLI error taxonomy: %T", err)
	}
	if ee.Type != "config" || ee.Code != "CLI_ERROR" {
		t.Fatalf("error type/code = %q/%q, want config/CLI_ERROR", ee.Type, ee.Code)
	}
}

func configureOutputPreflightProfileEnv(t *testing.T, configDir string) {
	t.Helper()
	t.Setenv(authstore.EnvConfigDir, configDir)
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvBotToken, "")
	t.Setenv(config.EnvBotID, "")
	t.Setenv(config.EnvCredentialMode, "")
	t.Setenv(config.EnvTaskToken, "")
	t.Setenv(config.EnvAPIBaseURL, "")
	t.Setenv(config.EnvSpaceID, "")
}

func saveOutputPreflightProfile(t *testing.T, store *authstore.Store, name, apiBaseURL string) {
	t.Helper()
	if err := store.SaveProfile(name, &authstore.ProfileMeta{
		APIBaseURL: apiBaseURL,
		RobotID:    "robot-output-preflight",
		BotKind:    "app_bot",
	}, "app_output_preflight"); err != nil {
		t.Fatalf("save profile: %v", err)
	}
}

func TestOutputPreflight_InvalidExplicitFormatDoesNotSendRequest(t *testing.T) {
	server, calls := newOutputPreflightServer(t)
	f := newOutputPreflightTestFactory(server.URL)

	_, err := executeOutputPreflightRoot(t, f.Factory,
		"--format", "unsupported",
		"message", "send", "--data", outputPreflightMessageBody,
	)
	requireOutputConfigError(t, err)
	if got := calls.Load(); got != 0 {
		t.Fatalf("business HTTP requests = %d, want 0 before rejecting --format", got)
	}
}

func TestOutputPreflight_InvalidEnvironmentFormatWithProfileDoesNotSendRequest(t *testing.T) {
	server, calls := newOutputPreflightServer(t)
	configDir := t.TempDir()
	configureOutputPreflightProfileEnv(t, configDir)
	t.Setenv(config.EnvFormat, "unsupported")
	store, err := authstore.New()
	if err != nil {
		t.Fatal(err)
	}
	saveOutputPreflightProfile(t, store, "agent", server.URL)

	f := cmdutil.NewDefaultFactory()
	_, err = executeOutputPreflightRoot(t, f, "--profile", "agent",
		"message", "send", "--data", outputPreflightMessageBody,
	)
	requireOutputConfigError(t, err)
	if got := calls.Load(); got != 0 {
		t.Fatalf("business HTTP requests = %d, want 0 before rejecting OCTO_FORMAT", got)
	}
}

func TestOutputPreflight_InvalidJQDoesNotSendRequest(t *testing.T) {
	server, calls := newOutputPreflightServer(t)
	f := newOutputPreflightTestFactory(server.URL)

	_, err := executeOutputPreflightRoot(t, f.Factory,
		"--jq", ".[",
		"message", "send", "--data", outputPreflightMessageBody,
	)
	requireOutputConfigError(t, err)
	if got := calls.Load(); got != 0 {
		t.Fatalf("business HTTP requests = %d, want 0 before rejecting --jq", got)
	}
}

func TestOutputPreflight_UncompilableJQDoesNotSendRequest(t *testing.T) {
	server, calls := newOutputPreflightServer(t)
	f := newOutputPreflightTestFactory(server.URL)

	_, err := executeOutputPreflightRoot(t, f.Factory,
		"--jq", "missing_audit_function",
		"message", "send", "--data", outputPreflightMessageBody,
	)
	if got := calls.Load(); got != 0 {
		t.Fatalf("business HTTP requests = %d, want 0 before rejecting --jq that fails compilation", got)
	}
	requireOutputConfigError(t, err)
}

func TestOutputPreflight_CompletionCommandsIgnoreOutputControls(t *testing.T) {
	t.Setenv(config.EnvFormat, "unsupported")
	f := cmdutil.NewDefaultFactory()
	f.IOStreams.Out = io.Discard
	f.IOStreams.ErrOut = io.Discard
	var stdout bytes.Buffer
	root := NewRootCmd(f)
	root.SetArgs([]string{"--jq", ".[", "completion", "bash"})
	root.SetOut(&stdout)
	root.SetErr(io.Discard)

	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("completion failed for output controls it does not consume: %v", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("completion produced no shell output")
	}
}

func TestOutputPreflight_InvalidOptionsPreventProfileRemoval(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
	}{
		{name: "format", flags: []string{"--format", "unsupported"}},
		{name: "jq", flags: []string{"--jq", ".["}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configDir := t.TempDir()
			configureOutputPreflightProfileEnv(t, configDir)
			store, err := authstore.New()
			if err != nil {
				t.Fatal(err)
			}
			saveOutputPreflightProfile(t, store, "agent", "https://gateway.example.test")

			f := newTestFactoryWithReg()
			_, err = executeOutputPreflightRoot(t, f.Factory,
				append(tc.flags, "--profile", "agent", "auth", "logout")...,
			)
			requireOutputConfigError(t, err)
			profiles, err := store.LoadProfiles()
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := profiles["agent"]; !exists {
				t.Fatal("auth logout removed the profile before rejecting the output option")
			}
		})
	}
}

func TestOutputPreflight_LegitimateControlsRemainAvailable(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		check func(t *testing.T, stdout string)
	}{
		{
			name:  "json",
			flags: []string{"--format", "json"},
			check: func(t *testing.T, stdout string) {
				var got struct {
					OK   bool `json:"ok"`
					Data struct {
						ClientMsgNo string `json:"client_msg_no"`
						MessageID   int64  `json:"message_id"`
						MessageSeq  int64  `json:"message_seq"`
					} `json:"data"`
				}
				if err := json.Unmarshal([]byte(stdout), &got); err != nil {
					t.Fatalf("decode JSON success result: %v", err)
				}
				if !got.OK || got.Data.ClientMsgNo != "client-1" || got.Data.MessageID != 42 || got.Data.MessageSeq != 7 {
					t.Fatalf("JSON success result = %+v, want the message send response", got)
				}
			},
		},
		{
			name:  "table",
			flags: []string{"--format", "table"},
			check: func(t *testing.T, stdout string) {
				for _, want := range []string{"client-1", "message_id", "42", "7"} {
					if !strings.Contains(stdout, want) {
						t.Fatalf("table output lacks result field %q: %s", want, stdout)
					}
				}
			},
		},
		{
			name:  "csv",
			flags: []string{"--format", "csv"},
			check: func(t *testing.T, stdout string) {
				rows, err := csv.NewReader(strings.NewReader(stdout)).ReadAll()
				if err != nil {
					t.Fatalf("decode CSV result: %v", err)
				}
				if len(rows) != 2 || len(rows[0]) != len(rows[1]) {
					t.Fatalf("CSV rows = %#v, want one header and one result row", rows)
				}
				values := make(map[string]string, len(rows[0]))
				for i, header := range rows[0] {
					values[header] = rows[1][i]
				}
				if values["client_msg_no"] != "client-1" || values["message_id"] != "42" || values["message_seq"] != "7" {
					t.Fatalf("CSV result = %#v, want the message send response", values)
				}
			},
		},
		{
			name:  "ndjson",
			flags: []string{"--format", "ndjson"},
			check: func(t *testing.T, stdout string) {
				var got struct {
					ClientMsgNo string `json:"client_msg_no"`
					MessageID   int64  `json:"message_id"`
					MessageSeq  int64  `json:"message_seq"`
				}
				decoder := json.NewDecoder(strings.NewReader(stdout))
				if err := decoder.Decode(&got); err != nil {
					t.Fatalf("decode NDJSON result: %v", err)
				}
				if got.ClientMsgNo != "client-1" || got.MessageID != 42 || got.MessageSeq != 7 {
					t.Fatalf("NDJSON result = %+v, want the message send response", got)
				}
				if err := decoder.Decode(new(any)); err != io.EOF {
					t.Fatalf("NDJSON has extra records: %v", err)
				}
			},
		},
		{
			name:  "valid jq",
			flags: []string{"--jq", ".data.message_id"},
			check: func(t *testing.T, stdout string) {
				var got int64
				if err := json.Unmarshal([]byte(stdout), &got); err != nil {
					t.Fatalf("decode jq result: %v", err)
				}
				if got != 42 {
					t.Fatalf("jq result = %d, want 42", got)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newOutputPreflightServer(t)
			f := newOutputPreflightTestFactory(server.URL)
			args := append(append([]string(nil), tc.flags...),
				"message", "send", "--data", outputPreflightMessageBody,
			)
			stdout, err := executeOutputPreflightRoot(t, f.Factory, args...)
			if err != nil {
				t.Fatalf("command with supported output controls failed: %v", err)
			}
			tc.check(t, stdout)
		})
	}
}

func TestOutputPreflight_ServiceHelpIgnoresInvalidOutputControls(t *testing.T) {
	cases := []struct {
		name          string
		commandPath   []string
		outputControl string
	}{
		{name: "service parent with malformed jq", commandPath: []string{"thread"}, outputControl: "jq"},
		{name: "service parent with invalid environment format", commandPath: []string{"thread"}, outputControl: "format"},
		{name: "nested group with malformed jq", commandPath: []string{"docs", "comments"}, outputControl: "jq"},
		{name: "nested group with invalid environment format", commandPath: []string{"docs", "comments"}, outputControl: "format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			configureOutputPreflightProfileEnv(t, t.TempDir())
			t.Setenv(config.EnvFormat, "")
			args := make([]string, 0, len(tc.commandPath)+2)
			switch tc.outputControl {
			case "jq":
				args = append(args, "--jq", ".[")
			case "format":
				t.Setenv(config.EnvFormat, "unsupported")
			default:
				t.Fatalf("unknown output control %q", tc.outputControl)
			}
			args = append(args, tc.commandPath...)

			f := cmdutil.NewDefaultFactory()
			root := NewRootCmd(f)
			helpCommand, _, err := root.Find(tc.commandPath)
			if err != nil {
				t.Fatalf("find help command: %v", err)
			}
			root.SetArgs(args)
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(io.Discard)

			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("help command rejected output controls it does not consume: %v", err)
			}
			if !strings.Contains(stdout.String(), helpCommand.CommandPath()) {
				t.Fatalf("help output does not identify requested command %q: %s", helpCommand.CommandPath(), stdout.String())
			}
		})
	}
}
