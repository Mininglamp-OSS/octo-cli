package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// docsInvitePathPrefix is where the Octo web app serves a document invite link:
// {octo web origin}/docs/invite/<inviteToken>.
const docsInvitePathPrefix = "/docs/invite/"

// inviteLinkPolicy is the invite-link spelling of the shared link rules: the
// same enforcement as a share link, reported as INVALID_INVITE_URL with a hint
// naming the two accepted forms.
var inviteLinkPolicy = linkPolicy{noun: "invite link", fail: invalidInviteURL}

// docsInviteAcceptOp is the operation this file wraps. It is named once, here,
// because the wrapper's correctness depends on it matching the spec: see
// registerDocsInviteAcceptURL for what happens when it stops matching.
const docsInviteAcceptOp = "docs.invite.accept"

// registerDocsInviteAcceptURL lets `docs invite accept` take the whole invite
// link in place of the bare token.
//
// The leaf itself stays the generated one — flags, risk gate, secret masking and
// `octo-cli schema docs.invite.accept` all keep coming from the spec. Only the
// *value* of the inviteToken slot is normalised first, because that is the whole
// difference: the backend endpoint is unchanged, and what a person actually has
// in hand is the link the web app gave them, not the token buried in it.
//
// Wrapping rather than detaching is deliberate. A hand-written composite (the
// shape `drive share access` needs, because it takes an argument the engine
// cannot express at all) would have to re-derive the path, the mount, the risk
// annotation and the secret list, and would drift from the spec the first time
// one of them changed. Here the engine still builds and runs the request.
//
// # Why the shape is asserted rather than tolerated
//
// This function used to return quietly whenever it could not find what it was
// looking for. That made the whole feature silently optional: rename the
// operationId, regroup the leaf, or change the flag, and link support would
// vanish with nothing failing — and the first symptom would be a user pasting a
// link and getting a backend 410 for a "token" that is really a whole URL.
//
// So the expectation is derived from the registry instead of assumed. If the
// embedded spec does not declare the operation at all, there is genuinely
// nothing to wrap and returning is correct. If it *does* declare it, the command
// tree must carry the matching leaf with a runnable RunE and a string
// invite-token flag, and anything else is a wiring bug in embedded data — the
// same class registry.MustNew panics on (internal/registry/loader.go:96, used by
// cmdutil's default RegistryFunc on the stated grounds that "specs are embedded;
// parse failure is a build-time bug"). It panics for the same reason: it cannot
// be provoked by user input or by the network, only by a change in this repo,
// and every test that builds a root command runs straight into it.
func registerDocsInviteAcceptURL(root *cobra.Command, f *cmdutil.Factory) {
	reg := f.Registry()
	if reg == nil {
		return
	}
	if _, declared := reg.GetOperation(docsInviteAcceptOp); !declared {
		// The spec does not offer this operation (a fixture registry, or the
		// operation withdrawn on purpose). Nothing to wrap, and nothing lost.
		return
	}

	accept := docsInviteAcceptLeaf(root)
	if accept == nil {
		panic(docsInviteAcceptOp + " is declared in the embedded spec but `docs invite accept` is not in the " +
			"command tree: the invite-link wrapper in cmd/docs_inviteurl.go has nothing to attach to, so pasting " +
			"an invite link would send the whole URL to the backend as a token. Re-point the wrapper at the leaf " +
			"the spec now produces.")
	}
	if accept.RunE == nil {
		panic(docsInviteAcceptOp + " leaf has no RunE, so the invite-link wrapper would replace nothing " +
			"(cmd/docs_inviteurl.go).")
	}
	if flag := accept.Flags().Lookup(docsInviteTokenFlag); flag == nil || flag.Value.Type() != "string" {
		panic(docsInviteAcceptOp + " leaf has no string --" + docsInviteTokenFlag + " flag, which is the slot the " +
			"invite-link wrapper rewrites (cmd/docs_inviteurl.go). Check x-octo-flag on the inviteToken path " +
			"parameter in internal/registry/specs/docs.json.")
	}

	generated := accept.RunE
	accept.RunE = func(cmd *cobra.Command, args []string) error {
		if err := normalizeInviteTokenArg(cmd, f, args); err != nil {
			return failErr(f, err)
		}
		return generated(cmd, args)
	}
	accept.Long += docsInviteLinkHelp
}

// docsInviteAcceptLeaf walks root to the generated `docs invite accept` leaf, or
// returns nil if any step of the path is missing. Split out so a test can assert
// the wrapper landed on the same command a user reaches.
func docsInviteAcceptLeaf(root *cobra.Command) *cobra.Command {
	cur := root
	for _, name := range []string{"docs", "invite", "accept"} {
		if cur == nil {
			return nil
		}
		cur = service.FindChild(cur, name)
	}
	return cur
}

// docsInviteTokenFlag is the flag form of the inviteToken path parameter
// (x-octo-flag in docs.json). The wrapper rewrites this slot, so the name is a
// contract between this file and the spec.
const docsInviteTokenFlag = "invite-token"

// docsInviteLinkHelp is the help text the wrapper appends. It is a package-level
// constant so a test can assert it is present on the assembled leaf and absent
// from a bare generated one — i.e. that the wrapper is what put it there.
const docsInviteLinkHelp = "\n\n<invite-token> may also be given as the whole invite link " +
	"(" + docsInvitePathPrefix + "<token> on the configured Octo origin). The link is " +
	"parsed locally and never fetched: only the token is extracted, and the request goes " +
	"to the configured Octo API."

// normalizeInviteTokenArg rewrites the inviteToken value in place when the
// caller supplied a link instead of a token. A bare token is left untouched, so
// the ordinary path is byte-identical to the generated leaf's.
func normalizeInviteTokenArg(cmd *cobra.Command, f *cmdutil.Factory, args []string) *output.ExitError {
	if cmd.Flags().Changed(docsInviteTokenFlag) {
		raw, err := cmd.Flags().GetString(docsInviteTokenFlag)
		if err != nil {
			// Fail closed. The caller set the flag, so a value exists and this
			// code cannot read it; forwarding regardless would hand an
			// unvalidated value — possibly a whole URL — straight to the
			// backend, which is exactly the check this wrapper exists to make.
			// registerDocsInviteAcceptURL already refuses to attach unless the
			// flag is a string, so reaching here means the leaf changed shape
			// under us at runtime.
			return invalidInviteURL("the --" + docsInviteTokenFlag + " value could not be read for checking")
		}
		token, verr := inviteTokenFromValue(f, raw)
		if verr != nil {
			return verr
		}
		if token != raw {
			if err := cmd.Flags().Set(docsInviteTokenFlag, token); err != nil {
				return invalidInviteURL("the invite link could not be reduced to its token")
			}
		}
		return nil
	}
	if len(args) == 0 {
		return nil // the engine reports the missing-value error, naming both forms
	}
	token, verr := inviteTokenFromValue(f, args[0])
	if verr != nil {
		return verr
	}
	args[0] = token
	return nil
}

// inviteTokenFromValue returns the invite token a caller supplied, accepting
// either the token itself or the link that contains it.
//
// A value with no "/" cannot be a link — an absolute URL needs one for its
// authority and a site-relative path starts with one, and the token charset
// (isShareIDChar) excludes it — so a bare token is returned as given without
// even resolving the configured origin. Everything else is parsed as a link,
// which means a mistyped link fails as a link rather than being forwarded to the
// backend as a nonsense token.
func inviteTokenFromValue(f *cmdutil.Factory, raw string) (string, *output.ExitError) {
	trimmed := strings.TrimSpace(raw)
	if !strings.Contains(trimmed, "/") {
		return raw, nil
	}
	cfg, err := f.Config()
	if err != nil {
		return "", output.ErrWithHint("config", "MISSING_API_BASE_URL",
			"the invite link cannot be checked because the configuration is unreadable",
			"set "+config.EnvAPIBaseURL+", or pass the bare invite token instead of the link")
	}
	return parseInviteURL(cfg, trimmed)
}

// parseInviteURL resolves a document invite link to its token, refusing
// anything that is not an invite link on the configured Octo origin.
//
// This is the same boundary parseShareURL draws, for the same reason and with
// the same rules — an invite link arrives from another person, so the CLI must
// never fetch it. The link's host is only ever *compared* against the configured
// Octo origin; the request that follows goes to the configured API with the
// token that was parsed out. Rejected, exactly as on the share path: any other
// host, userinfo in the authority, a non-http(s) scheme, a scheme that differs
// from the configured origin's, percent-encoding in the path (%2F would decode
// to a slash and smuggle extra segments past the shape check), extra path
// segments, and an empty or malformed token.
//
// The two share the enforcement helpers (assertSameOrigin, assertShareIDSegment)
// rather than restating them, so a future hardening of the share boundary
// applies here too instead of leaving a second, older copy behind.
func parseInviteURL(cfg *config.Config, raw string) (string, *output.ExitError) {
	if raw == "" {
		return "", invalidInviteURL("the invite link is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// urlParseCause, not err: *url.Error.Error() quotes its whole input, and
		// the whole input here is the link, whose last path segment is the invite
		// token. The inner cause names what was wrong without repeating it.
		return "", invalidInviteURL(fmt.Sprintf("not a valid URL: %v", urlParseCause(err)))
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return "", oerr
	}
	if verr := assertSameOrigin(u, origin, inviteLinkPolicy); verr != nil {
		return "", verr
	}
	if !strings.HasPrefix(u.Path, docsInvitePathPrefix) {
		// The path is not echoed: it contains the token on every link that is
		// nearly right, and this runs before the token is known to be a secret.
		return "", invalidInviteURL(fmt.Sprintf(
			"the link path is not a document invite link; expected %s<inviteToken>", docsInvitePathPrefix))
	}
	token := strings.TrimPrefix(u.Path, docsInvitePathPrefix)
	if verr := assertShareIDSegment("invite token", token, inviteLinkPolicy); verr != nil {
		return "", verr
	}
	return token, nil
}

// invalidInviteURL is the shared failure for an unusable invite link. The
// message never carries the link or the token.
func invalidInviteURL(msg string) *output.ExitError {
	return output.ErrWithHint("validation", "INVALID_INVITE_URL", msg,
		"pass the invite link exactly as `octo-cli docs invite create` / the web app produced it, "+
			"or pass the bare inviteToken")
}
