package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestMain isolates the package from the developer's own OCTO_ environment so
// the real-makeFactory tests (which resolve credential + config from env)
// behave deterministically. OCTO_CONFIG_DIR is repointed at an empty temp dir
// after the sweep so authstore never falls back to real user profiles. Tests
// that exercise a variable set it themselves with t.Setenv.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "OCTO_") {
			_ = os.Unsetenv(name)
		}
	}
	dir, err := os.MkdirTemp("", "octo-mcp-test")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("OCTO_CONFIG_DIR", dir)
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// testRoot mirrors cmd.NewRootCmd's service-tree wiring without importing
// package cmd (which imports this package — that would be an import cycle). It
// is enough for call_op to drive an operation end-to-end.
func testRoot(f *cmdutil.Factory) *cobra.Command {
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	pf := root.PersistentFlags()
	pf.StringVar(&f.Globals.Format, "format", "", "")
	pf.StringVarP(&f.Globals.JQ, "jq", "q", "", "")
	pf.BoolVar(&f.Globals.DryRun, "dry-run", false, "")
	pf.BoolVar(&f.Globals.Verbose, "verbose", false, "")
	pf.StringVar(&f.Globals.Timeout, "timeout", "", "")
	pf.BoolVar(&f.Globals.NoRetry, "no-retry", false, "")
	pf.StringVar(&f.Globals.Space, "space", "", "")
	pf.StringVar(&f.Globals.BotID, "bot-id", "", "")
	pf.StringVar(&f.Globals.Profile, "profile", "", "")
	service.RegisterServiceCommands(root, f)
	return root
}

// fakeBackendFactory returns a factoryFn wiring call_op to a fake backend URL
// and token. It builds a factory with buffered IO and stub Config/Credential/
// Client so no environment or network is touched.
func fakeBackendFactory(srvURL, token string) func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
	return func(tc TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
		cfg := &config.Config{APIBaseURL: srvURL, BotToken: token, Format: "json", SpaceID: tc.SpaceID}
		cred := &credential.BotCredential{Token: token, SpaceID: tc.SpaceID, BotKind: credential.TokenKind(token), Source: "test"}
		cli := client.New(cfg, cred, client.Options{ErrOut: io.Discard})
		streams, _, outBuf, errBuf := cmdutil.NewTestIOStreams()
		f := &cmdutil.Factory{IOStreams: streams, Globals: &cmdutil.GlobalOptions{}}
		f.RegistryFunc = registry.MustNew
		f.ConfigFunc = func() (*config.Config, error) { return cfg, nil }
		f.CredentialFunc = func() (*credential.BotCredential, error) { return cred, nil }
		f.ClientFunc = func() (*client.Client, error) { return cli, nil }
		return f, outBuf, errBuf
	}
}

// newTestServer builds a Server on the real embedded registry + mapping with
// the test root builder. Fails the test if the embedded mapping is invalid.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	srv, err := NewServer(testRoot)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv
}

// toolText decodes the JSON text payload of a tool result into a map.
func toolText(t *testing.T, res toolResult) map[string]any {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatalf("tool result has no content")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("decode tool text: %v\n%s", err, res.Content[0].Text)
	}
	return out
}

// callTool dispatches a tools/call and returns the decoded toolResult.
func callTool(t *testing.T, s *Server, name string, args any) toolResult {
	t.Helper()
	argsRaw, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(argsRaw)})
	resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if !has {
		t.Fatalf("tools/call produced no response")
	}
	if resp.Error != nil {
		t.Fatalf("tools/call rpc error: %v", resp.Error)
	}
	var res toolResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return res
}
