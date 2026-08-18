package cmd

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// shareTargetKind distinguishes the two things a share URL can point at.
const (
	shareKindBlob = "blob"
	shareKindDoc  = "doc"
)

// Share URL path prefixes on the Octo web origin.
const (
	blobSharePathPrefix = "/drive/s/"
	docSharePathPrefix  = "/d/"
	docSpaceQueryParam  = "sp"
)

// parsedShareURL is the result of resolving a share link locally.
type parsedShareURL struct {
	kind string
	// token is the blob share token (kind=blob).
	token string
	// docID / docSpaceID identify an online document (kind=doc).
	docID      string
	docSpaceID string
	// origin is the web origin the link was normalised against, kept so a masked
	// rendering can be rebuilt from the parts instead of searching the string.
	origin *url.URL
	// canonical is the link normalised against the configured web origin, which
	// is what gets echoed back so both sides see the same string.
	canonical string
}

// parseShareURL resolves a share link into a target, refusing anything that is
// not a share link on the configured Octo origin.
//
// This is the CLI's SSRF boundary. `drive share access|download` take a link
// that arrived from another person, so the value is untrusted: the CLI must
// never fetch it. What it does instead is parse out the share token (or the
// document id) and call the *configured* Octo API — the link's host is only ever
// compared, never contacted.
//
// Accepted shapes, and nothing else:
//
//   - /drive/s/<token>            → blob share
//   - /d/<docId>?sp=<docSpaceId>  → online document
//
// Either may be given as a site-relative path or as an absolute URL whose scheme
// and host match OCTO_API_BASE_URL (the Octo web origin serves both the web app
// and /v1/*). Rejected: any other host, userinfo in the authority, a non-http(s)
// scheme, percent-encoding inside the id segment (an encoded slash could smuggle
// extra path segments past the shape check), extra path segments, and an empty
// or malformed id.
func parseShareURL(cfg *config.Config, raw string) (*parsedShareURL, *output.ExitError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, invalidShareURL("the share link is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// urlParseCause, not err: *url.Error.Error() quotes its whole input, and on
		// `share access` / `share download` the whole input is the link, whose path is
		// the share token. The inner cause names what was wrong without repeating it.
		// (A couple of net/url causes do carry a very short fragment — url.EscapeError
		// quotes the three-character escape, InvalidHostError one character — which is
		// a category label rather than the token.)
		return nil, invalidShareURL(fmt.Sprintf("not a valid URL: %v", urlParseCause(err)))
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return nil, oerr
	}
	if verr := assertSameOrigin(u, origin, shareLinkPolicy); verr != nil {
		return nil, verr
	}

	switch {
	case strings.HasPrefix(u.Path, blobSharePathPrefix):
		token := strings.TrimPrefix(u.Path, blobSharePathPrefix)
		if verr := assertShareIDSegment("share token", token, shareLinkPolicy); verr != nil {
			return nil, verr
		}
		return &parsedShareURL{
			kind:      shareKindBlob,
			token:     token,
			origin:    origin,
			canonical: buildBlobShareURL(origin, token),
		}, nil

	case strings.HasPrefix(u.Path, docSharePathPrefix):
		docID := strings.TrimPrefix(u.Path, docSharePathPrefix)
		if verr := assertShareIDSegment("document id", docID, shareLinkPolicy); verr != nil {
			return nil, verr
		}
		docSpaceID := strings.TrimSpace(u.Query().Get(docSpaceQueryParam))
		if docSpaceID == "" {
			return nil, output.ErrWithHint("validation", "MISSING_DOC_SPACE_ID",
				"the document link has no sp= parameter, so the document's Octo Space is unknown",
				"ask the sharer to re-issue the link with `octo-cli drive share create`; the drive space id is NOT a valid substitute")
		}
		return &parsedShareURL{
			kind:       shareKindDoc,
			docID:      docID,
			docSpaceID: docSpaceID,
			origin:     origin,
			canonical:  buildDocShareURL(origin, docID, docSpaceID),
		}, nil
	}
	// The path is not echoed: on `share access` / `share download` the whole link
	// is the argument, so its path contains the share token, and this error is
	// raised before the token is known to be a secret. Naming the two accepted
	// shapes is the actionable part.
	return nil, invalidShareURL(fmt.Sprintf(
		"the link path is not a share link; expected %s<token> or %s<docId>?sp=<docSpaceId>",
		blobSharePathPrefix, docSharePathPrefix))
}

// linkPolicy names the kind of link being parsed and how to fail it, so the two
// enforcement helpers below can be shared by every command that accepts a link
// someone else handed over.
//
// It exists because the rules — not the wording — are the load-bearing part. A
// second link surface (`docs invite accept`, cmd/docs_inviteurl.go) needs the
// identical same-origin and segment checks, and the alternative to a parameter
// here was a second copy that a future hardening of this one would not reach.
// The drive spellings are the zero value of this indirection: shareLinkPolicy
// reproduces the previous strings and code exactly.
type linkPolicy struct {
	// noun is how the link is named in a diagnostic ("share link").
	noun string
	// fail builds the rejection, and owns the error code and hint.
	fail func(msg string) *output.ExitError
}

// shareLinkPolicy is the drive share-link spelling: INVALID_SHARE_URL, worded
// as before.
var shareLinkPolicy = linkPolicy{noun: "share link", fail: invalidShareURL}

// assertSameOrigin enforces the "compare, never contact" rule for an incoming
// link. A site-relative link has no authority to check. An absolute one
// must match the configured origin exactly — same scheme, and host equal
// (comparison is case-insensitive but includes the port, so a link on another
// port is a different origin) — and must carry no userinfo, which would
// otherwise be sent to the host and can also disguise the real target.
//
// The scheme is compared rather than merely required to be http(s): the
// docstring has always described strict same-origin matching, and accepting
// `http://host/…` against an `https://host` origin would let a downgraded link
// pass a check whose whole purpose is to establish that the link names the
// configured Octo deployment and nothing else.
//
// Percent-encoding in the path is refused rather than decoded: %2F would decode
// to a slash and let a crafted link pass the later shape check while actually
// addressing a different path.
func assertSameOrigin(u, origin *url.URL, p linkPolicy) *output.ExitError {
	if u.IsAbs() || u.Host != "" {
		if u.User != nil {
			return p.fail("the " + p.noun + " must not embed credentials")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return p.fail(fmt.Sprintf("unsupported scheme %q", u.Scheme))
		}
		if !strings.EqualFold(u.Scheme, origin.Scheme) {
			return p.fail(fmt.Sprintf(
				"the %s scheme %q is not the configured Octo origin's scheme %q", p.noun, u.Scheme, origin.Scheme))
		}
		if !strings.EqualFold(u.Host, origin.Host) {
			return p.fail(fmt.Sprintf(
				"the %s host %q is not the configured Octo origin %q", p.noun, u.Host, origin.Host))
		}
	}
	if u.EscapedPath() != u.Path {
		return p.fail("the " + p.noun + " path must not be percent-encoded")
	}
	return nil
}

// assertShareIDSegment enforces that a link's id segment is a single opaque
// token: one path segment, non-empty, not a dot segment, and limited to the
// characters Octo ids use. Anything else (a slash, a dot-dot, whitespace) is a
// malformed link.
//
// The dot check is explicit because "." is a legal character *inside* an id and
// url.PathEscape does not escape it, so a segment that is exactly "." or ".."
// would reach the URL as a real dot segment — see rejectDotSegments in
// cmd/service for why that matters on a DELETE.
func assertShareIDSegment(what, seg string, p linkPolicy) *output.ExitError {
	if seg == "" {
		return p.fail("the " + what + " is missing from the link")
	}
	if strings.Contains(seg, "/") {
		return p.fail("the link has extra path segments after the " + what)
	}
	if seg == "." || seg == ".." {
		return p.fail(fmt.Sprintf("the %s must be an id, not the path segment %q", what, seg))
	}
	for i := 0; i < len(seg); i++ {
		if !isShareIDChar(seg[i]) {
			return p.fail(fmt.Sprintf("the %s contains an unexpected character %q", what, string(seg[i])))
		}
	}
	return nil
}

// isShareIDChar reports whether c may appear in an Octo id segment: base64url
// plus the two separators Octo ids use. A dot is included because ids contain
// them; a segment that is *entirely* dots is refused by the caller.
func isShareIDChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '-' || c == '_' || c == '.' || c == ':':
		return true
	}
	return false
}

// webOrigin derives the Octo web origin from the configured API base URL. The
// nginx origin serves the web app and /v1/* together, so one setting covers
// both; there is deliberately no separate web-origin variable to drift.
func webOrigin(cfg *config.Config) (*url.URL, *output.ExitError) {
	if cfg == nil || cfg.APIBaseURL == "" {
		return nil, output.ErrWithHint("config", "MISSING_API_BASE_URL",
			"no API base URL configured", "set "+config.EnvAPIBaseURL)
	}
	u, err := url.Parse(strings.TrimSuffix(cfg.APIBaseURL, "/"))
	if err != nil || u.Host == "" {
		return nil, output.ErrWithHint("config", "MISSING_API_BASE_URL",
			fmt.Sprintf("%s is not a valid absolute URL: %q", config.EnvAPIBaseURL, cfg.APIBaseURL),
			"set it to the Octo origin, e.g. https://im.example.com (no path)")
	}
	// A configured path is refused rather than dropped. Returning scheme+host silently
	// discarded it, so a deployment served under a path prefix got share links built
	// against the wrong origin — links that look right, are handed to a recipient, and
	// resolve to nothing. The setting is documented as an origin with no path, so the
	// honest failure is to say the value is unusable instead of quietly reinterpreting it.
	if u.Path != "" {
		return nil, output.ErrWithHint("config", "MISSING_API_BASE_URL",
			fmt.Sprintf("%s must be an origin with no path, but it has %q", config.EnvAPIBaseURL, u.Path),
			"set it to the scheme and host only, e.g. https://im.example.com")
	}
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

// shareURLMask is the placeholder a masked share link carries in place of its token.
// It matches the spelling the rest of the dry-run envelope already uses for the same
// value, so the two halves of one envelope agree.
const shareURLMask = "***REDACTED***"

// redactedShareURL renders the link with its token replaced, for a --dry-run envelope.
//
// The dry-run description used to contradict itself: it masked the token inside "path",
// because a leaked description must not hand the share over, and printed the same token
// verbatim in "share_url" on the grounds that the caller supplied it so nothing new was
// disclosed. Both arguments cannot hold at once, and the standard for this output is that
// it is safe to paste into a ticket or a log, so masking wins on both.
//
// The masked link is *rebuilt from the parsed parts*, not produced by substituting the
// token into the finished string. The first version did the latter — one
// strings.Replace of the token, count 1 — which replaces the first occurrence anywhere in
// the URL, so a token that also appears in the scheme or the host had that earlier
// occurrence masked while the real token stayed in the path. A token of "https" produced
// "***REDACTED***://octo.example/drive/s/https": masked-looking and masking nothing.
// Searching for the value was the wrong shape of answer, because the token's position is
// already known — rebuilding leaves nothing to search and nothing to mismatch.
//
// A document link has no token — its identifiers are the doc id and its Octo Space, both
// of which the same envelope reports as their own fields — so its canonical link is
// returned unchanged.
func redactedShareURL(p *parsedShareURL) string {
	if p == nil {
		return ""
	}
	if p.token == "" || p.origin == nil {
		return p.canonical
	}
	return buildBlobShareURL(p.origin, shareURLMask)
}

// buildBlobShareURL renders the link the sharer hands over for a blob share.
func buildBlobShareURL(origin *url.URL, token string) string {
	return origin.String() + blobSharePathPrefix + token
}

// buildDocShareURL renders the link for an online document. The sp parameter
// carries the document's own Octo Space, which is what the docs frontend needs;
// the drive space id is a different scope and must never be substituted.
func buildDocShareURL(origin *url.URL, docID, docSpaceID string) string {
	q := url.Values{docSpaceQueryParam: []string{docSpaceID}}
	return origin.String() + docSharePathPrefix + docID + "?" + q.Encode()
}

func invalidShareURL(msg string) *output.ExitError {
	return output.ErrWithHint("validation", "INVALID_SHARE_URL", msg,
		"pass the share_url exactly as `octo-cli drive share create` produced it")
}
