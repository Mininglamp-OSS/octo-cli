// Package cmdutil contains the command-layer DI container (Factory) and shared
// command-runtime helpers. Commands receive a *Factory and resolve everything
// else — config, client, credential, io streams — through it.
package cmdutil

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/config"
	"github.com/dmwork-org/octo-cli/internal/credential"
	"github.com/dmwork-org/octo-cli/internal/output"
	"github.com/dmwork-org/octo-cli/internal/registry"
)

// GlobalOptions holds the values of root-level persistent flags that command
// RunE functions need to consult. Populated by the root command before
// dispatch. Zero values are meaningful defaults (e.g. Format="" → json).
type GlobalOptions struct {
	Format   string
	JQ       string
	DryRun   bool
	Verbose  bool
	Timeout  string // raw string; parsed where used
	NoRetry  bool
	Space    string
	Yes      bool
	PageAll  bool
	PageMax  int
}

// Factory is the DI container. All accessors are lazy + cached so a command
// can ask for only what it needs. The hooks (ConfigFunc, CredentialFunc etc.)
// are exposed so tests can substitute stubs without touching the environment.
type Factory struct {
	IOStreams *IOStreams
	Globals   *GlobalOptions

	ConfigFunc     func() (*config.Config, error)
	CredentialFunc func() (*credential.BotCredential, error)
	ClientFunc     func() (*client.Client, error)
	RegistryFunc   func() *registry.Registry

	// Cached resolutions.
	config *config.Config
	cred   *credential.BotCredential
	cli    *client.Client
	reg    *registry.Registry
}

// NewDefaultFactory wires the production providers: config from env, cred from
// env chain, client built from both. IO defaults to stdio. Callers (root cmd)
// inject the global options after parsing flags.
func NewDefaultFactory() *Factory {
	f := &Factory{
		IOStreams: NewStdIOStreams(),
		Globals:   &GlobalOptions{},
	}

	f.ConfigFunc = func() (*config.Config, error) {
		if f.config != nil {
			return f.config, nil
		}
		cfg := config.Load()
		if f.Globals.Format != "" {
			cfg.Format = f.Globals.Format
		}
		f.config = cfg
		return cfg, nil
	}

	f.CredentialFunc = func() (*credential.BotCredential, error) {
		if f.cred != nil {
			return f.cred, nil
		}
		chain := credential.NewChain(credential.NewEnvProvider())
		cred, err := chain.Resolve()
		if err != nil {
			return nil, err
		}
		if f.Globals.Space != "" {
			cred.SpaceID = f.Globals.Space
		}
		f.cred = cred
		return cred, nil
	}

	f.ClientFunc = func() (*client.Client, error) {
		if f.cli != nil {
			return f.cli, nil
		}
		cfg, err := f.ConfigFunc()
		if err != nil {
			return nil, err
		}
		cred, err := f.CredentialFunc()
		if err != nil {
			return nil, err
		}
		cli := client.New(cfg, cred, client.Options{
			Verbose: f.Globals.Verbose,
			DryRun:  f.Globals.DryRun,
			NoRetry: f.Globals.NoRetry,
			Timeout: f.Globals.Timeout,
			ErrOut:  f.IOStreams.ErrOut,
		})
		f.cli = cli
		return cli, nil
	}

	f.RegistryFunc = func() *registry.Registry {
		if f.reg != nil {
			return f.reg
		}
		// Specs are embedded; parse failure is a build-time bug.
		f.reg = registry.MustNew()
		return f.reg
	}

	return f
}

// Config returns the resolved config (cached).
func (f *Factory) Config() (*config.Config, error) { return f.ConfigFunc() }

// Credential returns the resolved credential (cached).
func (f *Factory) Credential() (*credential.BotCredential, error) { return f.CredentialFunc() }

// Client returns the resolved HTTP client (cached).
func (f *Factory) Client() (*client.Client, error) { return f.ClientFunc() }

// Registry returns the spec registry (cached). Tests may inject a stub by
// replacing RegistryFunc.
func (f *Factory) Registry() *registry.Registry {
	if f.RegistryFunc == nil {
		return nil
	}
	return f.RegistryFunc()
}

// Format returns the effective output format (CLI flag > env/config default).
func (f *Factory) Format() string {
	if f.Globals != nil && f.Globals.Format != "" {
		return f.Globals.Format
	}
	if cfg, err := f.Config(); err == nil {
		return cfg.Format
	}
	return output.FormatJSON
}

// EmitSuccess renders a success envelope, applying --jq first (if set) and
// then the configured output format. Non-nil raw is placed inside the envelope.
func (f *Factory) EmitSuccess(raw []byte) error {
	rm := normalizeRaw(raw)
	return f.emit(rm, output.EnvelopeMeta{})
}

// EmitSuccessWithMeta is EmitSuccess plus envelope meta (rate limit, notice).
func (f *Factory) EmitSuccessWithMeta(raw []byte, meta output.EnvelopeMeta) error {
	return f.emit(normalizeRaw(raw), meta)
}

func (f *Factory) emit(raw []byte, meta output.EnvelopeMeta) error {
	// Build the success envelope into an in-memory buffer first so --jq can
	// operate on the canonical envelope shape (not the backend shape).
	var envBuf writableBuffer
	if err := output.WriteSuccess(&envBuf, raw, meta); err != nil {
		return err
	}
	// Re-parse into any so Format/ApplyJQ can work with it.
	var envelope any
	if err := json.Unmarshal(envBuf.Bytes(), &envelope); err != nil {
		return fmt.Errorf("re-parse envelope: %w", err)
	}

	if f.Globals != nil && f.Globals.JQ != "" {
		results, err := output.ApplyJQ(envelope, f.Globals.JQ)
		if err != nil {
			return err
		}
		// When jq returns a single value, emit it directly; else emit the slice.
		if len(results) == 1 {
			envelope = results[0]
		} else {
			envelope = results
		}
	}

	return output.Format(f.IOStreams.Out, f.Format(), envelope)
}

// EmitError renders an error envelope to stderr. Always returns nil so that
// cobra doesn't print its own error on top of ours; the caller is responsible
// for setting the process exit code separately.
func (f *Factory) EmitError(err error) error {
	return output.WriteError(f.IOStreams.ErrOut, err)
}

// --- helpers ---

// writableBuffer is a small buffer that implements io.Writer so we can reuse
// output.WriteSuccess without importing bytes here for the single allocation.
type writableBuffer struct {
	data []byte
}

func (b *writableBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}
func (b *writableBuffer) Bytes() []byte { return b.data }

func normalizeRaw(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// --- IOStreams writer shim for outbound commands ---

// Out returns the factory's stdout writer. Small convenience used by write paths.
func (f *Factory) Out() io.Writer { return f.IOStreams.Out }

// ErrOut returns the factory's stderr writer.
func (f *Factory) ErrOut() io.Writer { return f.IOStreams.ErrOut }
