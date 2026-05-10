package cmdutil

import (
	"bytes"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/config"
	"github.com/dmwork-org/octo-cli/internal/credential"
)

// TestFactory is a pre-wired Factory for unit tests. Callers can poke at
// In/Out/ErrOut to seed input or read emitted envelopes. Client/Credential/
// Config hooks can be replaced freely before the command runs.
type TestFactory struct {
	*Factory
	In     *bytes.Buffer
	Out    *bytes.Buffer
	ErrOut *bytes.Buffer
}

// NewTestFactory returns a TestFactory with in-memory streams, a minimal
// config (BotToken set so Validate passes), and a BotCredential. The Client
// hook returns an error by default — tests that exercise commands must
// inject a client via SetClient.
func NewTestFactory() *TestFactory {
	streams, in, out, errOut := NewTestIOStreams()
	cfg := &config.Config{
		APIURL:   "http://test.local",
		BotToken: "app_test",
		Format:   "json",
	}
	cred := &credential.BotCredential{
		Token:  "app_test",
		Source: "test",
	}

	f := &Factory{
		IOStreams: streams,
		Globals:   &GlobalOptions{},
	}
	f.ConfigFunc = func() (*config.Config, error) { return cfg, nil }
	f.CredentialFunc = func() (*credential.BotCredential, error) { return cred, nil }
	f.ClientFunc = func() (*client.Client, error) {
		return nil, errTestClientNotSet
	}

	return &TestFactory{Factory: f, In: in, Out: out, ErrOut: errOut}
}

// SetClient injects a client (usually backed by httptest.Server) for tests.
func (t *TestFactory) SetClient(c *client.Client) {
	t.Factory.ClientFunc = func() (*client.Client, error) { return c, nil }
}

// SetConfig replaces the config hook.
func (t *TestFactory) SetConfig(cfg *config.Config) {
	t.Factory.ConfigFunc = func() (*config.Config, error) { return cfg, nil }
}

// SetCredential replaces the credential hook.
func (t *TestFactory) SetCredential(c *credential.BotCredential) {
	t.Factory.CredentialFunc = func() (*credential.BotCredential, error) { return c, nil }
}

// errTestClientNotSet is returned when a test command calls Client() without
// setting one first. Kept as a sentinel so tests can assert on it if needed.
var errTestClientNotSet = stringError("test factory: client not set (call SetClient first)")

type stringError string

func (e stringError) Error() string { return string(e) }
