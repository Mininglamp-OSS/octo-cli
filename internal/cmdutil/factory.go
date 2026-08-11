// Package cmdutil contains the command-layer DI container (Factory) and shared
// command-runtime helpers. Commands receive a *Factory and resolve everything
// else — config, client, credential, io streams — through it.
package cmdutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/authstore"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// GlobalOptions holds the values of root-level persistent flags that command
// RunE functions need to consult. Populated by the root command before
// dispatch. Zero values are meaningful defaults (e.g. Format="" → json).
type GlobalOptions struct {
	Format  string
	JQ      string
	DryRun  bool
	Verbose bool
	Timeout string // raw string; parsed where used
	NoRetry bool
	Space   string
	PageAll bool
	PageMax int

	// Profile selects a stored credential by friendly name; BotID selects (and
	// asserts) it by robot id. BotID falls back to OCTO_BOT_ID when the flag is
	// empty. Both feed the credential chain's FileProvider.
	Profile string
	BotID   string
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

	// ErrorEmitted is set to true after EmitError writes an envelope to
	// stderr. The top-level main func checks this to avoid double-emitting
	// when RunE returns the same error that was already rendered.
	ErrorEmitted bool

	// Cached resolutions.
	config *config.Config
	cred   *credential.BotCredential
	cli    *client.Client
	reg    *registry.Registry
	store  *authstore.Store
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
		cfg, err := f.buildConfig()
		if err != nil {
			return nil, err
		}
		f.config = cfg
		return cfg, nil
	}

	f.CredentialFunc = func() (*credential.BotCredential, error) {
		if f.cred != nil {
			return f.cred, nil
		}
		cred, err := f.buildCredential()
		if err != nil {
			return nil, err
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

// buildConfig loads the env config and reflects the resolved credential into it
// so cfg.Validate (the auth gate in root's PersistentPreRunE) passes for
// profile-based use and config show reports the active token. Only the "no
// credential found" case is swallowed (so cfg.Validate reports the familiar
// OCTO_BOT_TOKEN hint for the zero-config case); structured resolution errors
// (ambiguous / missing profile) AND real IO errors (home dir, salt read) both
// surface rather than masquerading as a missing token.
func (f *Factory) buildConfig() (*config.Config, error) {
	cfg := config.Load()
	if f.Globals.Format != "" {
		cfg.Format = f.Globals.Format
	}
	cred, err := f.CredentialFunc()
	if err != nil {
		if errors.Is(err, credential.ErrNoCredential) {
			return cfg, nil
		}
		return nil, err
	}
	if cred != nil {
		if cred.Token != "" {
			cfg.BotToken = cred.Token
		}
		if cred.SpaceID != "" {
			cfg.SpaceID = cred.SpaceID
		}
		if err := f.overlayProfileBaseURL(cfg, cred.Profile); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// buildCredential resolves the credential through the file→env chain, applying
// the --bot-id (or OCTO_BOT_ID) / --profile selectors and the --space override.
func (f *Factory) buildCredential() (*credential.BotCredential, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(config.EnvCredentialMode)))
	if mode == config.CredentialModeTask {
		return f.buildTaskCredential()
	}
	if mode != "" {
		return nil, fmt.Errorf("unsupported %s %q", config.EnvCredentialMode, mode)
	}

	store, err := f.AuthStore()
	if err != nil {
		return nil, err
	}
	botID := f.Globals.BotID
	if botID == "" {
		botID = os.Getenv(config.EnvBotID)
	}
	chain := credential.NewChain(
		credential.NewFileProvider(store, f.Globals.Profile, botID),
		credential.NewEnvProvider(),
	)
	cred, err := chain.Resolve()
	if err != nil {
		return nil, err
	}
	if f.Globals.Space != "" {
		cred.SpaceID = f.Globals.Space
	}
	return cred, nil
}

// buildTaskCredential is deliberately separate from the normal provider
// chain when OCTO_CREDENTIAL_MODE=task is present. The daemon is responsible
// for preserving that marker and isolating OCTO_CONFIG_DIR for task processes;
// the CLI cannot make a caller-controlled environment tamper-proof. Fleet
// verifies the opaque bearer and enforces its bindings/actions.
func (f *Factory) buildTaskCredential() (*credential.BotCredential, error) {
	if f.Globals.Profile != "" || f.Globals.BotID != "" ||
		strings.TrimSpace(os.Getenv(config.EnvBotID)) != "" {
		return nil, fmt.Errorf("%s=task does not allow --profile, --bot-id, or %s", config.EnvCredentialMode, config.EnvBotID)
	}
	if f.Globals.Space != "" {
		return nil, fmt.Errorf("%s=task does not allow --space; use the Loop command's --workspace-id", config.EnvCredentialMode)
	}
	if strings.TrimSpace(os.Getenv(config.EnvToken)) != "" {
		return nil, fmt.Errorf("%s=task does not allow %s; use the daemon-injected %s", config.EnvCredentialMode, config.EnvToken, config.EnvBotToken)
	}

	provider := credential.NewEnvProvider()
	provider.TokenVar = config.EnvBotToken
	cred, err := provider.Resolve()
	if err != nil {
		return nil, err
	}
	if cred == nil {
		return nil, fmt.Errorf("%w: %s=task requires %s", credential.ErrNoCredential, config.EnvCredentialMode, config.EnvBotToken)
	}
	if credential.TokenKind(cred.Token) != "loop_credential" {
		return nil, fmt.Errorf("%s=task requires a Loop task credential in %s", config.EnvCredentialMode, config.EnvBotToken)
	}
	// Task credentials are server-bound; never forward a caller-selected Space
	// context alongside them.
	cred.SpaceID = ""
	cred.BotKind = "agent_task"
	return cred, nil
}

// AuthStore lazily constructs and caches the credential store.
func (f *Factory) AuthStore() (*authstore.Store, error) {
	if f.store != nil {
		return f.store, nil
	}
	s, err := authstore.New()
	if err != nil {
		return nil, err
	}
	f.store = s
	return s, nil
}

// overlayProfileBaseURL fills cfg.APIBaseURL from the active profile's metadata,
// but only when OCTO_API_BASE_URL is not explicitly set (env wins for the URL).
func (f *Factory) overlayProfileBaseURL(cfg *config.Config, profile string) error {
	if profile == "" || os.Getenv(config.EnvAPIBaseURL) != "" {
		return nil
	}
	store, err := f.AuthStore()
	if err != nil {
		return err
	}
	profiles, err := store.LoadProfiles()
	if err != nil {
		return err
	}
	if m, ok := profiles[profile]; ok && m.APIBaseURL != "" {
		normalized, err := config.NormalizeAPIBaseURL(m.APIBaseURL)
		if err != nil {
			return fmt.Errorf("profile %q has an invalid API base URL: %w; update it with `octo-cli auth login --profile %s`", profile, err, profile)
		}
		cfg.APIBaseURL = normalized
	}
	return nil
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

// identityValue builds the envelope's identity field from the credential
// resolved during the command. It only inspects the already-cached credential
// (never forces resolution), so commands that don't authenticate keep the plain
// "bot" tag. Returns nil to mean "use the default".
func (f *Factory) identityValue() any {
	if f.cred == nil {
		return nil
	}
	identityType := "bot"
	if f.cred.BotKind == "agent_task" {
		identityType = "agent_task"
	}
	id := map[string]any{"type": identityType}
	if f.cred.Profile != "" {
		id["profile"] = f.cred.Profile
	}
	if f.cred.RobotID != "" {
		id["robot_id"] = f.cred.RobotID
	}
	if f.cred.BotKind == "agent_task" {
		id["credential_kind"] = "agent_task"
	} else if kind := credential.TokenKind(f.cred.Token); kind != "" {
		id["bot_kind"] = kind
	}
	if f.cred.Source != "" {
		id["source"] = f.cred.Source
	}
	return id
}

func (f *Factory) emit(raw []byte, meta output.EnvelopeMeta) error {
	if meta.Identity == nil {
		meta.Identity = f.identityValue()
	}
	// Build the success envelope into an in-memory buffer first so --jq can
	// operate on the canonical envelope shape (not the backend shape).
	var envBuf bytes.Buffer
	if err := output.WriteSuccess(&envBuf, raw, meta); err != nil {
		return err
	}
	// Re-parse into any so Format/ApplyJQ can work with it. UseNumber keeps every
	// integer at its exact decimal text: a plain unmarshal would turn a uint64 id
	// above 2^53 into a rounded float64, silently corrupting it on the way to
	// stdout even when the backend and the transport got it right. gojq handles
	// json.Number for arithmetic, comparison and tostring, and the table/csv
	// renderer prints it verbatim.
	var envelope any
	dec := json.NewDecoder(bytes.NewReader(envBuf.Bytes()))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil {
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

// EmitError renders an error envelope to stderr. Non-ExitError values are
// classified via WrapCLIError before rendering so the envelope always carries
// a proper taxonomy. Sets ErrorEmitted so the top-level main func can avoid
// double-emitting. Always returns nil so cobra doesn't print its own error
// on top of ours; the caller sets the process exit code separately.
func (f *Factory) EmitError(err error) error {
	f.ErrorEmitted = true
	return output.WriteError(f.IOStreams.ErrOut, WrapCLIError(err))
}

// --- helpers ---

// WrapCLIError classifies a plain error into an *output.ExitError using narrow
// heuristics for the common agent-facing failures (missing token, unknown
// flag, missing arg). Already-wrapped *ExitError values pass through
// unchanged. Everything else falls through to a generic config error so exit
// codes and envelopes stay predictable.
//
// Shared by the root command (for cobra-framework errors) and the Factory
// (for EmitError callers) so both paths produce identical taxonomy.
//
// # Which messages may be echoed, and why
//
// This is the last funnel before stderr, and the stderr envelope is unconditional —
// no --verbose gates it. Five separate rounds of review found a caller-supplied secret
// arriving here through some formatting point, so the rule is written down rather than
// re-derived each time: **a message reaches the envelope only if its text cannot
// contain a value the caller typed.**
//
// The messages that arrive here, by origin:
//
//	origin                     format                                     embeds argv?
//	---------------------------+-------------------------------------------+------------
//	pflag failf (bad syntax)   bad flag syntax: %s                         YES (the whole token)
//	pflag UnknownFlagError     unknown flag: --%s                          YES (the name IS argv)
//	pflag UnknownFlagError     unknown shorthand flag: %q in -%s           YES (the whole run)
//	pflag ValueRequiredError   flag needs an argument: [%q in -]%s         YES (same run)
//	pflag InvalidArgumentError invalid argument %q for %q flag: %v         YES (the value)
//	cobra unknown command      unknown command %q for %q                   YES (the token)
//	this project's             unknown subcommand for %q; available: %s     no (our own words:
//	rejectUnknownSubcommand                                                 the command path and
//	                                                                        our subcommand names)
//	cobra arg count            accepts %d arg(s), received %d              no (counts)
//	cobra minimum args         requires at least %d arg(s), only received  no (counts)
//	cobra required flags       required flag(s) %q not set                 no (flag names)
//	this project's config/     "token is required", "…OCTO_TOKEN…"         no (our own text)
//	credential packages
//
// Note the two "unknown …" rows are different strings from different producers and must
// not be collapsed: cobra quotes the token it did not recognise, while this project's own
// rejectUnknownSubcommand was rewritten in an earlier round precisely so that it names the
// command path and lists the real subcommands instead of echoing the argument. Blanking
// that one would throw away the fix rather than extend it.
//
// pflag has six failf call sites, and the list above matched five of them: `bad flag
// syntax: %s`, whose argument is the entire argv token, was in no branch and printed in
// full. So the rule is inverted rather than extended by one: **within the flag-parse
// family, echoing requires an allowlist entry**, and everything else is reported by
// category. The allowlist is checked first because "required flag(s) …" mentions flags
// too. A seventh failf in a future pflag release is covered without being enumerated,
// which is the whole point — enumerating the unsafe shapes is what missed one.
//
// The categories that embed argv are reported by name instead. That is a real loss of
// detail — an operator no longer sees which flag was rejected — and it is accepted
// because the alternative cannot be made safe here: this runs *before* collectSecrets,
// so there is no list of declared secrets to mask against, and a base64url share or
// invite token beginning with "-" is exactly what pflag reports as an unknown flag.
// The hint carries the remedy, including the "-" case, so the caller is not left
// guessing.
//
// The fallback keeps its text. Everything reaching it either comes from this project's
// own code or is a cobra error not matched above; the standing contract for our own
// code is that an error carrying a caller value must be constructed as an *ExitError
// with the value already masked, which is checked at the sites that have a secret list.
// If a future error type formats argv into an unclassified message, that contract is
// where it has to be fixed — not by widening the guesses here.
func WrapCLIError(err error) error {
	if err == nil {
		return nil
	}
	if ee := output.AsExitError(err); ee != nil {
		return ee
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "octo_token"),
		strings.Contains(lower, "octo_bot_token"),
		strings.Contains(lower, "bot token"),
		strings.Contains(lower, "token is required"):
		return output.ErrAuth(msg, "set OCTO_TOKEN (or OCTO_BOT_TOKEN) to an app_*, bf_*, or uk_* token")
	// --- allowlist first: shapes proven to carry no caller value keep their text ---
	//
	// Order matters. These are checked before the family catch-all below, because
	// "required flag(s) …" also mentions flags and would otherwise be blinded along
	// with everything else.
	case strings.Contains(lower, "unknown subcommand"),
		strings.Contains(lower, "required flag"),
		strings.Contains(lower, "accepts "),
		strings.Contains(lower, "requires at "),
		strings.Contains(lower, "arg(s)"):
		return output.ErrValidation(msg, "run `octo-cli <command> --help` to see valid flags and args")

	// --- then the categories reported by name, for a useful remedy ---
	case strings.Contains(lower, "unknown flag"),
		strings.Contains(lower, "unknown shorthand"):
		return output.ErrValidation("a flag in the command line was not recognised",
			"run `octo-cli <command> --help` for the valid flags. If this is an id that starts "+
				"with \"-\" (base64url ids do), pass it as its named flag or after a \"--\" separator")
	case strings.Contains(lower, "flag needs an argument"):
		return output.ErrValidation("a flag was given without its value",
			"run `octo-cli <command> --help` for the valid flags and their arguments")
	case strings.Contains(lower, "invalid argument"):
		return output.ErrValidation("a flag value was rejected by its type",
			"run `octo-cli <command> --help` to see the expected type for each flag")
	case strings.Contains(lower, "bad flag syntax"):
		return output.ErrValidation("a flag in the command line is malformed",
			"a flag is --name or --name=value (or -n); check for a stray dash or a missing name")
	case strings.Contains(lower, "unknown command"):
		return output.ErrValidation("that is not a known command",
			"run `octo-cli --help`, or `octo-cli <domain> --help`, for the available commands")

	// --- and the family catch-all: anything else about flags is not echoed ---
	//
	// This is what makes the rule default-deny rather than a list of known-bad shapes.
	// The previous version enumerated the formats that embed argv and blinded those,
	// which matched five of pflag's six failf sites and missed "bad flag syntax: %s" —
	// the one whose argument is the entire argv token. A sixth strings.Contains would
	// have repeated the same mistake at a different index; this covers the seventh
	// failf a future pflag release adds without anyone having to notice it.
	case strings.Contains(lower, "flag"), strings.Contains(lower, "shorthand"):
		return output.ErrValidation("the command line could not be parsed",
			"run `octo-cli <command> --help` for the valid flags and args")
	}
	return output.ErrWithHint("config", "CLI_ERROR", msg, "")
}

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
