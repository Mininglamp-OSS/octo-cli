package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

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
	identity := policyFixtureServer(http.NotFoundHandler())
	_ = os.Setenv("OCTO_BOT_TOKEN", "bf_fixture")
	_ = os.Setenv("OCTO_API_BASE_URL", identity.URL)
	code := m.Run()
	identity.Close()
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
		streams, _, outBuf, errBuf := cmdutil.NewTestIOStreams()
		f := &cmdutil.Factory{IOStreams: streams, Globals: &cmdutil.GlobalOptions{}}
		f.RegistryFunc = registry.MustNew
		f.ConfigFunc = func() (*config.Config, error) { return cfg, nil }
		f.CredentialFunc = func() (*credential.BotCredential, error) { return cred, nil }
		// Build the client lazily so it honours the --dry-run global the cobra tree
		// sets during execution (the engine's dry-run short-circuits in client.Do);
		// a pre-built client would ignore it and still hit the fake backend.
		f.ClientFunc = func() (*client.Client, error) {
			opts := client.Options{ErrOut: io.Discard}
			if f.Globals != nil && f.Globals.DryRun {
				opts.DryRun = true
			}
			return client.New(cfg, cred, opts), nil
		}
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
	srv.WithAdmission(testAdmission(t))
	return srv
}

// Historical facade fixtures now include the approved read-only policy wire.
// Authentication traffic is kept separate from business-delivery counters.
func policyFixtureServer(next http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/verify-bot" {
			next.ServeHTTP(w, r)
			return
		}
		var body struct {
			Token  string `json:"bot_token"`
			Policy bool   `json:"include_mcp_policy"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || !strings.HasPrefix(body.Token, "bf_") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.Policy {
			_, _ = io.WriteString(w, `{"bot_uid":"bot_fixture","mcp_policy":`+compatiblePolicy+`}`)
		} else {
			_, _ = io.WriteString(w, `{"bot_uid":"bot_fixture"}`)
		}
	}))
}

func testHTTPHandler(t *testing.T, build RootBuilder, tc TrustedContext, base cmdutil.GlobalOptions, facades ...facadeMode) (*HTTPHandler, error) { //nolint:gocritic // copy operator globals to match the production constructor boundary
	t.Helper()
	h, e := NewHTTPHandlerWithAdmission(build, tc, base, testAdmission(t), facades...)
	if e == nil {
		t.Cleanup(h.Close)
	}
	return h, e
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
	// Inventory tests intentionally issue many sequential calls. Respect the
	// production admission budget while preserving every business assertion.
	for resp.Error != nil && resp.Error.Code == -32006 {
		time.Sleep(130 * time.Millisecond)
		resp, has = s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	}
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

func expectAdmissionRPC(t *testing.T, s *Server, name string, args any, code int) {
	t.Helper()
	raw, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(raw)})
	r, _ := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if r.Error == nil || r.Error.Code != code {
		t.Fatalf("expected admission %d, got %+v", code, r)
	}
}
