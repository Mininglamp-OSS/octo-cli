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
		return nil, invalidShareURL(fmt.Sprintf("not a valid URL: %v", err))
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return nil, oerr
	}
	if verr := assertSameOrigin(u, origin); verr != nil {
		return nil, verr
	}

	switch {
	case strings.HasPrefix(u.Path, blobSharePathPrefix):
		token := strings.TrimPrefix(u.Path, blobSharePathPrefix)
		if verr := assertShareIDSegment("share token", token); verr != nil {
			return nil, verr
		}
		return &parsedShareURL{
			kind:      shareKindBlob,
			token:     token,
			canonical: buildBlobShareURL(origin, token),
		}, nil

	case strings.HasPrefix(u.Path, docSharePathPrefix):
		docID := strings.TrimPrefix(u.Path, docSharePathPrefix)
		if verr := assertShareIDSegment("document id", docID); verr != nil {
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

// assertSameOrigin enforces the "compare, never contact" rule for an incoming
// share link. A site-relative link has no authority to check. An absolute one
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
func assertSameOrigin(u, origin *url.URL) *output.ExitError {
	if u.IsAbs() || u.Host != "" {
		if u.User != nil {
			return invalidShareURL("the share link must not embed credentials")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return invalidShareURL(fmt.Sprintf("unsupported scheme %q", u.Scheme))
		}
		if !strings.EqualFold(u.Scheme, origin.Scheme) {
			return invalidShareURL(fmt.Sprintf(
				"the share link scheme %q is not the configured Octo origin's scheme %q", u.Scheme, origin.Scheme))
		}
		if !strings.EqualFold(u.Host, origin.Host) {
			return invalidShareURL(fmt.Sprintf(
				"the share link host %q is not the configured Octo origin %q", u.Host, origin.Host))
		}
	}
	if u.EscapedPath() != u.Path {
		return invalidShareURL("the share link path must not be percent-encoded")
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
func assertShareIDSegment(what, seg string) *output.ExitError {
	if seg == "" {
		return invalidShareURL("the " + what + " is missing from the link")
	}
	if strings.Contains(seg, "/") {
		return invalidShareURL("the link has extra path segments after the " + what)
	}
	if seg == "." || seg == ".." {
		return invalidShareURL(fmt.Sprintf("the %s must be an id, not the path segment %q", what, seg))
	}
	for i := 0; i < len(seg); i++ {
		if !isShareIDChar(seg[i]) {
			return invalidShareURL(fmt.Sprintf("the %s contains an unexpected character %q", what, string(seg[i])))
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
	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
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
