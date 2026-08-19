// Package cmd — drive composite commands.
//
// Forty of the drive leaves are generated from
// internal/registry/specs/drive.json (43 spec operations, 3 of them detached).
// The six in this file and its siblings are hand-written because they are not a
// single request: `upload file` runs prepare → object PUT → confirm, `download
// file` and `share download` fetch a presigned URL and then write bytes to disk,
// `share create` branches on the node type, and `share blob-create` / `share
// access` / `share download` take an argument shape (positional file id, whole
// share URL) the metadata engine cannot express. All of them resolve their mount
// through service.MountForOperation, so the bot/user-key routing and the
// token-kind gate come from the same spec metadata as the generated leaves.
package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// transferTimeout bounds a single object-storage transfer. It is deliberately
// far longer than the API timeout: a presigned PUT/GET moves the whole file,
// while every drive metadata call is a small JSON round-trip.
const transferTimeout = 30 * time.Minute

// transferDialTimeout bounds one TCP connection attempt.
const transferDialTimeout = 30 * time.Second

// transferDialAttemptTimeout caps a single address attempt when a name resolved to
// more than one address.
//
// net.Dialer given a *name* races the address families (Happy Eyeballs, RFC 6555)
// with a short fallback delay. This guard resolves first and dials addresses itself,
// which is what makes the check and the connection agree on one address — but it
// gives up that race, so a black-holed AAAA answer ahead of a working A answer would
// otherwise stall for the whole dial budget before the address that works is tried.
// Capping each attempt recovers most of the property while keeping the guarantee.
//
// It applies only when there is another address to fall back to: with a single
// answer, shortening the budget would turn a slow-but-working connection into a
// failure for no benefit.
const transferDialAttemptTimeout = 10 * time.Second

// registerDriveCmds attaches the hand-written drive leaves after the
// metadata-driven tree exists, so they can hang off the generated `drive
// upload` / `drive download` / `drive share` groups.
//
// Three generated leaves are detached first: `share blob-create`, `share
// access` and `share download` keep their spec operations (so `octo-cli schema
// drive.share.access` still describes the real endpoint) but their CLI surface
// is the hand-written one, which takes a positional file id / whole share URL.
func registerDriveCmds(root *cobra.Command, f *cmdutil.Factory) {
	drive := service.FindChild(root, "drive")
	if drive == nil {
		return
	}
	if upload := service.FindChild(drive, "upload"); upload != nil {
		upload.AddCommand(newDriveUploadFileCmd(f))
	}
	if download := service.FindChild(drive, "download"); download != nil {
		download.AddCommand(newDriveDownloadFileCmd(f))
	}
	if share := service.FindChild(drive, "share"); share != nil {
		service.RemoveLeaf(share, "blob-create")
		service.RemoveLeaf(share, "access")
		service.RemoveLeaf(share, "download")
		share.AddCommand(newDriveShareCreateCmd(f))
		share.AddCommand(newDriveShareBlobCreateCmd(f))
		share.AddCommand(newDriveShareAccessCmd(f))
		share.AddCommand(newDriveShareDownloadCmd(f))
	}
}

// --- transport for object storage ---

// hostResolver answers "where does this name point". It is a parameter rather than
// a package-level variable so the transfer dialer keeps no mutable global state:
// production passes nil and gets the system resolver, and a test passes a table.
//
// A test seam is needed here and cannot be a public DNS name. The wildcard-DNS
// names usually cited for this (127.0.0.1.nip.io, localtest.me) resolve to loopback
// only where nothing intercepts them, and resolvers that rewrite unknown or
// loopback-pointing answers are common enough that a test built on them can quietly
// stop asserting anything while still passing.
type hostResolver func(ctx context.Context, host string) ([]net.IPAddr, error)

// systemHostResolver resolves through the system resolver.
//
// The answers keep their Zone: a scoped link-local address (fe80::1%eth0) is a
// different destination from the same address without the zone, and dropping the
// identifier makes it undiallable.
func systemHostResolver(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// transferGuard decides whether a transfer connection may be made, on the evidence
// of where the name actually points rather than how it is spelled.
//
// The string rules in assertSafeTransferTarget stay as a cheap pre-filter — they
// are correct, only incomplete. What they cannot do is classify a name: a hostname
// that is not an IP literal tells them nothing, so an attacker who points a domain
// they own at 127.0.0.1 and obtains an ordinary publicly-trusted certificate for it
// walks through every notation rule, because the notation is legitimate. Judging the
// resolved address is truthful by construction, so no future spelling can outflank
// it.
//
// # The paths, enumerated
//
// Three consecutive rounds of review each found a defect in a transport path that the
// previous round's fix had not considered — a loopback rule that judged spelling, then
// a rule that judged the proxy instead of the target, then a lookup that made a
// proxy-only network unusable. Every one was "the path we were looking at is now
// correct, and the set of paths was never enumerated". So the set is enumerated here,
// and any change to this file should be checked against every row rather than the row
// that prompted it.
//
// The transport calls Proxy for every request and DialContext for every new
// connection. Whether a proxy is selected decides who resolves the target, which
// decides what can be classified and what can be pinned:
//
//	# | configuration                      | resolves target | classifies it        | dials                | resolution failure
//	--+------------------------------------+-----------------+----------------------+----------------------+--------------------
//	1 | direct, remote Octo origin         | this guard      | this guard, on the   | the validated        | fatal (fail closed)
//	  |                                    |                 | resolved addresses   | address, pinned      |
//	2 | direct, loopback Octo origin       | this guard, in  | nobody — the rule is | the validated        | fatal
//	  |                                    | DialContext     | off by configuration | address, pinned      |
//	3 | proxied, target locally resolvable | the proxy       | this guard, on its   | the proxy's address, | n/a
//	  |                                    | (advisory: us)  | own local answer     | as given             |
//	4 | proxied, target NOT locally        | the proxy only  | nobody — unclassified| the proxy's address, | NOT fatal: allowed,
//	  | resolvable                         |                 | (pre-filter only)    | as given             | noted, --verbose
//	5 | proxied, loopback Octo origin      | the proxy only  | nobody — rule off    | the proxy's address  | n/a
//	6 | redirect hop                       | as 1-5 for the new URL, after the CheckRedirect string pre-filter
//
// # The host value, enumerated
//
// The table above enumerates *configurations*. It silently assumed something the
// round-12 review disproved: that the host is one value. It is not — it is a value
// that gets rewritten on its way to the socket, and the two halves of this file were
// reading different versions of it:
//
//	stage | who                                              | version of the host
//	------+--------------------------------------------------+---------------------------------
//	  1   | the backend, inside the presigned URL             | raw text
//	  2   | url.Parse                                        | u.Host (host[:port])
//	  3   | u.Hostname()                                     | port stripped, IPv6 brackets off
//	  4   | our string pre-filter (isLoopbackHost, …)         | stage 3
//	  5   | our classification, resolver, validated map key   | stage 3
//	  6   | net/http canonicalAddr -> idnaASCIIFromURL        | **idnaASCII(stage 3)**
//	  7   | the dial, or the CONNECT authority                | stage 6
//	  8   | our DialContext validatedFor() lookup             | stage 6, split back out of addr
//
// Stage 6 is the one nothing here accounted for. net/http's idnaASCII returns its
// input unchanged when the host is already ASCII and otherwise maps it through UTS-46
// (idna.Lookup.ToASCII), keeping the raw value when that fails. So for a non-ASCII
// host, stages 4 and 5 judge one string and stages 7 and 8 use another: the fullwidth
// spellings of 127.0.0.1 and localhost passed every string rule as ordinary names,
// failed resolution (Go's resolver performs no IDNA, so the lookup could not succeed),
// landed in row 4 as "unclassified, allowed", and were then dialled as the loopback
// address net/http had mapped them to. Deterministically, not by luck.
//
// The fix is to make stage 3 and stage 6 the same value rather than to teach stages 4
// and 5 one more spelling — that would only move the next Unicode mapping to the next
// round. Because idnaASCII is the identity on ASCII input, requiring the host to be
// ASCII at the boundary makes the two stages equal *by construction*, with nothing to
// keep in sync with a future version of net/http. assertHostIsCanonicalASCII is that
// requirement; a host needing an internationalised name is presented in its A-label
// (xn--) form, which is what DNS carries anyway.
//
// Row 4 is the one that cost a round. A proxy-only network — tightened egress with no
// external resolver, or a split-horizon name only the proxy's resolver can answer —
// is ordinary, and "CONNECT host:port" exists precisely so the proxy resolves the
// name. Insisting on a local lookup there fails a transfer that would have worked, for
// an answer nobody needs.
//
// What is given up in rows 3-5, stated rather than implied: the guard cannot pin an
// address it does not dial. The proxy performs its own resolution and its own
// connection, so between our classification and the proxy's lookup the answer may
// differ — the rebinding window the direct path closes stays open on the proxy path.
// What stands in for it is that the proxy is the operator's own component, the string
// pre-filter still rejects every literal local spelling before any of this, and the
// connection terminates at the proxy rather than at a service on this machine.
type transferGuard struct {
	field       string
	loopbackAPI bool
	resolve     hostResolver
	dial        func(ctx context.Context, network, addr string) (net.Conn, error)
	proxy       func(*http.Request) (*url.URL, error)
	// note reports a decision an operator would want to see under --verbose. nil is
	// a no-op, so a guard built for a unit test needs no wiring.
	note func(format string, args ...any)

	// mu guards the two records below. Both are written by Proxy and read by
	// DialContext, which the transport may call from a different goroutine.
	mu sync.Mutex
	// validated maps a target host to the addresses Proxy classified for it, so the
	// dial uses the answers that were checked instead of resolving a second time.
	validated map[string][]net.IPAddr
	// proxies records the hosts of proxies the selector chose, so the dialer can
	// tell "the machine the URL names" from "the machine the operator routes through".
	proxies map[string]bool
}

// newTransferGuard builds the guard for one transfer client. resolve may be nil,
// meaning the system resolver.
func newTransferGuard(field string, loopbackAPI bool, resolve hostResolver) *transferGuard {
	if resolve == nil {
		resolve = systemHostResolver
	}
	return &transferGuard{
		field:       field,
		loopbackAPI: loopbackAPI,
		resolve:     resolve,
		dial:        (&net.Dialer{Timeout: transferDialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		proxy:       http.ProxyFromEnvironment,
		validated:   map[string][]net.IPAddr{},
		proxies:     map[string]bool{},
	}
}

// Proxy is the transport's proxy selector, and it is where the target is classified.
//
// It has to be here. When the transport selects a proxy, DialContext is handed the
// *proxy's* address — for a plain-HTTP request and for the CONNECT tunnel alike — and
// the URL's own host never reaches it. A dialer-only guard therefore judged whichever
// machine the connection happened to go to, which under a configured proxy is not the
// machine the rule is about. Proxy receives the *http.Request, so it always sees the
// real target, whether or not a proxy ends up being used.
//
// Refusing here also refuses before the transport opens anything, which is the same
// guarantee the dialer gives on the direct path.
func (g *transferGuard) Proxy(req *http.Request) (*url.URL, error) {
	// The order of these three steps is the fix for round 11, and each step depends on
	// the one before it:
	//
	//  1. ask who will make the connection, because that decides whose job it is to
	//     resolve the target (rows 3-5 of the table above: the proxy's);
	//  2. ask whether the rule is switched on at all, because with a loopback Octo
	//     origin nothing is classified and a lookup here is pure cost (rows 2 and 5);
	//  3. only then resolve — and treat a failure as fatal exactly when this guard is
	//     the resolver, which is to say when no proxy was selected (row 1 vs row 4).
	//
	// Resolving first, as this did, made a proxy-only network fail on a lookup that
	// nothing needed and nobody could have satisfied.
	// The boundary refuses a non-canonical host before a transfer starts, and this is
	// the same rule again at the point that classifies. Not a second patch: it is one
	// function, called where the guarantee has to hold even if a future call site
	// reaches the transport without going through assertSafeTransferURL. Every request
	// the transport makes passes through here, hops included.
	if cerr := assertHostIsCanonicalASCII(g.field, req.URL); cerr != nil {
		return nil, cerr
	}
	proxyURL, perr := g.proxy(req)
	if perr != nil {
		return nil, invalidProxyError(perr)
	}
	if proxyURL != nil {
		g.rememberProxy(proxyURL.Hostname())
	}
	if g.loopbackAPI {
		return proxyURL, nil
	}

	host := req.URL.Hostname()
	ips, rerr := g.resolveTarget(req.Context(), host)
	if rerr != nil {
		if proxyURL != nil {
			// Row 4. The proxy resolves the target — that is what CONNECT host:port
			// is for — so a name we cannot resolve is not evidence of anything. It is
			// the ordinary shape of tightened egress with no external resolver, and of
			// a split-horizon name only the proxy's resolver answers. Unclassified is
			// narrower than the invariant claims, so it is said out loud rather than
			// passed over.
			g.notef("%s host %q was not classified locally: %v — the proxy resolves it, "+
				"and the local-machine rule cannot be applied to an address this CLI never sees",
				g.field, host, rerr)
			return proxyURL, nil
		}
		// Row 1. No proxy: this guard is the resolver, and a name it cannot resolve
		// must not be handed onward to be resolved again inside the dial.
		return nil, rerr
	}
	if cerr := g.refuseLocalAddresses(ips, g.targetIsLocalError); cerr != nil {
		// A lookup that *succeeded* and says "local" is refused on every path,
		// proxied or not. The leniency above is about not knowing, never about
		// knowing and allowing.
		return nil, cerr
	}
	g.remember(host, ips)
	return proxyURL, nil
}

// invalidProxyError reports an unusable proxy configuration without repeating the
// value that produced it.
//
// The selector's error text can carry the raw environment value — x/net/http/httpproxy
// formats it into "invalid proxy address %q" — and a proxy URL is commonly
// http://user:password@host:port, since enterprise TLS-inspection and paid egress
// proxies routinely carry basic auth. Passing that text through would put the user's
// proxy credential into the structured error on stderr, unconditionally and without
// --verbose. That is the same defect class as the share-token leaks fixed earlier in
// this PR, with the user's own credential rather than the backend's.
//
// So nothing derived from the value is included. The caller knows which variables to
// look at, and is told why the value is not shown.
func invalidProxyError(cause error) *output.ExitError {
	// The cause is not dropped, only stripped of anything derived from the value. Its
	// concrete type is what actually distinguishes the failures a caller can act on —
	// a URL that would not parse versus net/http refusing to honour HTTP_PROXY in a CGI
	// environment, which is a real and otherwise baffling case — and a type name cannot
	// carry a password.
	// The CGI case is detected from the condition rather than by matching the message
	// text: net/http exports no sentinel for it, and its wording is not part of any
	// contract, so a free-text match would silently stop distinguishing the two the
	// first time that string is reworded. REQUEST_METHOD being set is exactly what puts
	// net/http into CGI mode, so asking that is asking the real question.
	detail := "the value could not be parsed as a proxy URL"
	if cause != nil && os.Getenv("REQUEST_METHOD") != "" {
		detail = "HTTP_PROXY is ignored when REQUEST_METHOD is set, because that indicates a CGI environment"
	}
	return output.ErrWithHint("validation", "INVALID_PROXY",
		"the proxy configuration in this environment could not be used: "+detail,
		"check http_proxy / https_proxy / all_proxy: the value must be a URL such as "+
			"http://proxy.example:3128. The value itself is not repeated here because a "+
			"proxy URL often carries credentials")
}

// verboseNoter adapts progressf to the guard's note sink, so a decision the guard
// makes is visible on the same --verbose stream as the rest of the transfer.
func verboseNoter(f *cmdutil.Factory) func(string, ...any) {
	return func(format string, args ...any) { progressf(f, format, args...) }
}

// notef reports a decision under --verbose, if the caller wired a sink.
func (g *transferGuard) notef(format string, args ...any) {
	if g.note != nil {
		g.note(format, args...)
	}
}

// DialContext opens the connection, and is the only hook that can bind the address
// that was checked to the socket that gets opened.
//
// Three cases, and which one applies is decided by what Proxy recorded rather than by
// guessing from the address:
//
//   - the target on the direct path: dial one of the addresses Proxy validated for it.
//     Re-resolving here instead would let a resolver answer differently between the
//     check and the connection, which is the whole point of dialing an address.
//   - a proxy the selector chose: dial it as given. A proxy on the local machine is an
//     ordinary debugging or TLS-inspection setup, it is the operator's own
//     configuration rather than a host named by the backend, and the storage rule does
//     not apply to it. Refusing it here is what broke every transfer for those users.
//   - anything else: nobody classified this host, so classify it now and fail closed
//     if it is local — but attribute it to the environment, because the backend did
//     not choose it. This case *is* reachable, and row 2 of the table is how: under a
//     loopback Octo origin Proxy classifies nothing, so the target arrives here
//     unrecorded and this branch performs the lookup that pins the address. It is also
//     the fail-closed default for a host that reached the transport without passing the
//     boundary. (An earlier version of this comment said the case does not arise; that
//     was written before the loopback short-circuit moved above the lookup.)
func (g *transferGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ips := g.validatedFor(host); len(ips) > 0 {
		return g.dialAddrs(ctx, network, host, ips, port)
	}
	if g.isSelectedProxy(host) {
		return g.dial(ctx, network, addr)
	}
	ips, rerr := g.resolveAndRefuseLocal(ctx, host, g.connectionRedirectedLocallyError)
	if rerr != nil {
		return nil, rerr
	}
	return g.dialAddrs(ctx, network, host, ips, port)
}

// resolveAndRefuseLocal resolves host and refuses when any answer names the local
// machine, unless the configured Octo origin is itself local — the exception that
// keeps local development working.
//
// Every answer is checked, not just the first: a name with one remote and one local
// address must be refused, or the guard is decided by resolver ordering.
func (g *transferGuard) resolveAndRefuseLocal(ctx context.Context, host string, mkErr func() *output.ExitError) ([]net.IPAddr, error) {
	ips, err := g.resolveTarget(ctx, host)
	if err != nil {
		return nil, err
	}
	if cerr := g.refuseLocalAddresses(ips, mkErr); cerr != nil {
		return nil, cerr
	}
	return ips, nil
}

// refuseLocalAddresses is the classification itself, split out so Proxy can decide
// separately what a *failure to resolve* means without also loosening what a
// successful answer means.
//
// Every answer is checked, not just the first: a name with one remote and one local
// address must be refused, or the outcome is decided by resolver ordering.
func (g *transferGuard) refuseLocalAddresses(ips []net.IPAddr, mkErr func() *output.ExitError) *output.ExitError {
	if g.loopbackAPI {
		return nil
	}
	for _, ip := range ips {
		if ip.IP.IsLoopback() || ip.IP.IsUnspecified() {
			return mkErr()
		}
	}
	return nil
}

// targetIsLocalError reports a presigned URL whose own host is this machine. The
// backend chose that host, so the remedy is upstream.
func (g *transferGuard) targetIsLocalError() *output.ExitError {
	return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
		fmt.Sprintf("%s resolves to the local machine, which is not reachable object storage", g.field),
		"the storage endpoint pointed the transfer at this machine; report it")
}

// connectionRedirectedLocallyError reports a connection that landed on this machine
// without the URL naming it. Nothing upstream chose this, so the hint points at the
// local configuration that can.
//
// Keeping this distinct from targetIsLocalError is not cosmetic. One message for both
// told every user with a local proxy that object storage had attacked them, which is
// both wrong and unactionable.
func (g *transferGuard) connectionRedirectedLocallyError() *output.ExitError {
	return output.ErrWithHint("validation", "TRANSFER_REDIRECTED_LOCALLY",
		fmt.Sprintf("the connection for %s was directed to the local machine, which is not the host that URL names", g.field),
		"something in this environment is rerouting the transfer; check http_proxy / https_proxy / all_proxy and no_proxy")
}

// dialAddrs tries each validated address in turn.
func (g *transferGuard) dialAddrs(ctx context.Context, network, host string, ips []net.IPAddr, port string) (net.Conn, error) {
	var lastErr error
	for i := range ips {
		attemptCtx, cancel := g.attemptContext(ctx, len(ips)-i)
		conn, derr := g.dial(attemptCtx, network, net.JoinHostPort(ips[i].String(), port))
		// The context governs the dial, not the connection it returns, so releasing
		// it here is correct either way and is what stops the timer leaking.
		cancel()
		if derr == nil {
			return conn, nil
		}
		lastErr = derr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no address for %q", host)
	}
	return nil, lastErr
}

// attemptContext caps one address attempt when there is another to fall back to.
// See transferDialAttemptTimeout for why a single answer keeps the full budget.
//
// remaining counts this attempt and the ones after it, not the whole answer set. The
// rule is "cap an attempt only if failing it still leaves somewhere to go", and passing
// the total made the *last* attempt carry a deadline too — so a multi-address name
// could fail on a bound that existed for a fallback it no longer had. The comment
// already said this; the code now does.
func (g *transferGuard) attemptContext(ctx context.Context, remaining int) (context.Context, context.CancelFunc) {
	if remaining < 2 {
		return context.WithCancel(ctx)
	}
	// A real deadline rather than a timer that cancels: net.Dialer reads the
	// deadline, and anything inspecting the attempt can see the bound.
	return context.WithTimeout(ctx, transferDialAttemptTimeout)
}

func (g *transferGuard) remember(host string, ips []net.IPAddr) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.validated[host] = ips
}

func (g *transferGuard) rememberProxy(host string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.proxies[host] = true
}

func (g *transferGuard) validatedFor(host string) []net.IPAddr {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.validated[host]
}

func (g *transferGuard) isSelectedProxy(host string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.proxies[host]
}

// resolveTarget returns the addresses addr's host names. An IP literal is used as
// given; anything else goes through the resolver, and a name that cannot be
// resolved fails rather than being handed to the dialer as a name — otherwise an
// unresolvable host would slip past the check and be resolved again inside the
// dial.
func (g *transferGuard) resolveTarget(ctx context.Context, host string) ([]net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IPAddr{{IP: ip}}, nil
	}
	ips, err := g.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for %q", host)
	}
	return ips, nil
}

// transferClient is the HTTP client used for presigned object-storage
// PUT/GET. It is deliberately separate from the API client so a caller
// credential can never reach the storage endpoint: the presigned URL already
// carries its own authorisation, and forwarding an Octo token to a third-party
// host would leak it. Nothing else is inherited either.
//
// Redirects are followed for a GET (storage gateways use them) but every hop is
// re-validated by assertSafeTransferTarget, so the https-or-loopback-http rule
// holds for the destination that actually serves the bytes and not merely for
// the first URL the backend handed over. An unsafe hop fails the transfer rather
// than downgrading it, and the 10-hop cap Go's default policy applies is kept.
//
// A redirect that would change the method, or any redirect on a body-carrying
// request, is refused. For 301/302/303 Go rewrites a PUT into a bodiless GET, so
// a storage host answering 2xx after such a hop would report a successful upload
// with nothing written — and the caller would then confirm a drive row pointing
// at an object that does not exist. (307/308 preserve the method and fail on their
// own, because an *os.File body cannot be replayed.) Refusing is not merely safe
// but correct: a presigned signature is bound to the original URL and can never be
// honoured at a different one, so there is no legitimate upload redirect to lose.
//
// The Referer header is dropped on every hop. Go fills it in from the previous
// request's full URL, and for a presigned URL that includes the signature in the
// query string — a short-lived bearer credential for the object. The redirect
// target addresses its own URL and never presents the original signature, so it
// has no use for the value; sending it would leave object read (GET) or write
// (PUT) access sitting in a third party's access log.
//
// loopbackAPI reflects whether the configured Octo origin is itself loopback. It
// gates the plain-http exception: that exception exists so local development
// works, and against a remote origin a cooperating storage host could otherwise
// answer 302 http://127.0.0.1:<port>/… and steer the transfer at a service on
// the caller's own machine.
//
// The rule now covers **every** connection under a remote origin, the initial one as
// well as each hop, whatever its scheme. That is wider than the rule this docstring
// used to state: while the check lived in CheckRedirect it could only judge hops, and
// the text said the initial URL "comes from the trusted backend and may legitimately
// name an internal host". Deciding on the resolved address moved the check into the
// connection path, so the initial URL is judged too. The consequence is worth stating
// plainly rather than leaving to be discovered: a deployment whose Octo origin is
// remote while its object storage resolves to the caller's own machine no longer
// transfers. Private and other internal ranges are still allowed — only the local
// machine is not — and pointing OCTO_API_BASE_URL at loopback restores the whole
// local setup.
//
// Under a configured proxy the invariant holds in a narrower form, and the difference
// is worth knowing: the target is resolved and classified locally in transferGuard.Proxy,
// but the connection is made by the proxy, so the *address* cannot be pinned the way
// the direct path pins it. What backs the rule there is that the operator chose the
// proxy; what this guard adds is that a target naming the local machine is refused
// before the request is handed over.
//
// resolve may be nil, meaning the system resolver.
func transferClient(field string, loopbackAPI bool, resolve hostResolver, note func(string, ...any)) *http.Client {
	guard := newTransferGuard(field, loopbackAPI, resolve)
	guard.note = note
	return transferClientWithGuard(guard)
}

// transferClientWithGuard is the constructor a test can hand a guard with its own
// seams to, so the proxy path is exercised through the real transport instead of
// through the process-wide proxy environment. http.ProxyFromEnvironment memoises that
// environment in a package-level sync.Once, so an env-based assertion silently depends
// on which test in the binary ran first — it is not a usable seam.
//
// The transport is built field by field rather than cloned from
// http.DefaultTransport. Clone() inherits whatever that package-level variable holds
// at the time, which means a dependency setting DialTLSContext on it would have HTTPS
// transfers use that hook and bypass this guard entirely, and a dependency replacing
// http.DefaultTransport with another type would panic the type assertion on every
// transfer. Neither is live today; constructing explicitly removes both futures and
// leaves the TLS dial hooks nil by definition.
func transferClientWithGuard(guard *transferGuard) *http.Client {
	field, loopbackAPI := guard.field, guard.loopbackAPI
	transport := &http.Transport{
		Proxy:       guard.Proxy,
		DialContext: guard.DialContext,
		// Content-Encoding is refused on this transport, because accepting it hands the
		// storage host a switch that turns off assertCompleteBody.
		//
		// Left unset, the transport advertises Accept-Encoding: gzip on its own,
		// decompresses the reply transparently, and then sets resp.ContentLength to -1 —
		// correctly, since the length it was given describes the compressed bytes and no
		// longer describes the body the caller reads. assertCompleteBody's comparison is
		// guarded on `contentLength >= 0`, so a host that compresses gets the truncation
		// check skipped: the guard was disabled at the discretion of the same untrusted
		// party it defends against. It also made the transfer unbounded in the one
		// direction that matters here — a 20 KB reply expanded to 20 MB on the caller's
		// -o path and was reported complete, with an attacker-chosen ratio and no
		// declared size or checksum in DownloadURL to bound it.
		//
		// Two further reasons this is right rather than merely safe. The reported size and
		// sha256 must describe the *stored object*, and under transparent decompression
		// they described its expansion — a checksum that certifies the wrong bytes, which
		// is the harm assertCompleteBody's own docstring is written to prevent. And an
		// object another client stored with Content-Encoding: gzip was written out
		// decompressed, so `download file` did not round-trip what `upload file` sent.
		//
		// The cost is the wire bytes for an object that would have compressed. Object
		// storage serves what was stored, so that cost is paid only when the stored object
		// is itself compressible and uncompressed — and byte-exact transfer is what a
		// checksum-reporting download owes its caller.
		DisableCompression:    true,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   transferTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Referer")
			if len(via) >= maxTransferRedirects {
				return fmt.Errorf("stopped after %d redirects", maxTransferRedirects)
			}
			if err := assertRedirectKeepsTheRequest(field, req, via); err != nil {
				return err
			}
			if err := assertSafeTransferTarget(field, req.URL, loopbackAPI); err != nil {
				return err
			}
			if !loopbackAPI && isLoopbackHost(req.URL.Hostname()) {
				return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
					fmt.Sprintf("%s redirected to a loopback host, which is not reachable object storage", field),
					"the storage endpoint redirected the transfer at the local machine; report it")
			}
			return nil
		},
	}
}

// assertRedirectKeepsTheRequest refuses a redirect that would not carry the
// original request. Go silently rewrites a PUT into a bodiless GET on 301/302/303,
// which for an upload turns "the object was never written" into a 2xx the caller
// reports as success.
func assertRedirectKeepsTheRequest(field string, req *http.Request, via []*http.Request) *output.ExitError {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	if req.Method != previous.Method {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s redirected a %s into a %s, which would not carry the request body",
				field, previous.Method, req.Method),
			"the storage endpoint answered with a method-changing redirect; report it")
	}
	if previous.Method != http.MethodGet && previous.Method != http.MethodHead {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s redirected a %s request; a presigned signature is bound to its own URL and cannot be honoured elsewhere",
				field, previous.Method),
			"the storage endpoint redirected an upload; report it")
	}
	return nil
}

// maxTransferRedirects mirrors the cap Go's default redirect policy applies.
// Setting CheckRedirect replaces that policy wholesale, so the cap has to be
// restated or a redirect loop would run until the transfer timeout.
const maxTransferRedirects = 10

// apiOriginIsLoopback reports whether the configured Octo origin is a loopback
// host, which is the only situation in which plain-http object storage is
// accepted. A config that cannot be read is treated as non-loopback: the strict
// rule is the safe default.
func apiOriginIsLoopback(f *cmdutil.Factory) bool {
	cfg, err := f.Config()
	if err != nil {
		return false
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return false
	}
	return isLoopbackHost(origin.Hostname())
}

// assertSafeTransferURL rejects a presigned URL that is not safe to fetch, and
// returns it parsed for the caller.
func assertSafeTransferURL(field, raw string, loopbackAPI bool) (*url.URL, *output.ExitError) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Error's text embeds the whole raw URL, signature included, so the
		// parse cause is reported without it.
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s is not a valid URL: %v", field, urlParseCause(err)),
			"the backend returned an unusable presigned URL; report it")
	}
	if serr := assertSafeTransferTarget(field, u, loopbackAPI); serr != nil {
		return nil, serr
	}
	return u, nil
}

// assertHostIsCanonicalASCII requires the host to be the same string net/http will
// dial, which is what makes every other rule in this file mean what it says.
//
// net/http's canonicalAddr sends the host through idnaASCII: the identity when the host
// is already ASCII, UTS-46 (idna.Lookup.ToASCII) when it is not, and the raw value again
// when that mapping fails. Go's resolver performs no IDNA at all. So a non-ASCII host
// gave this file two different strings to reason about — the pre-filter and the
// classification saw the spelling from the URL, the dial and the CONNECT authority saw
// the mapped form — and the fullwidth spellings of 127.0.0.1 and localhost went through
// as unclassifiable names and came out as loopback.
//
// Requiring ASCII is chosen over mapping it ourselves deliberately. Because idnaASCII is
// the identity on ASCII, this makes the checked string and the dialled string equal *by
// construction*: there is one spelling, so "every literal local spelling is refused by
// the pre-filter" is true again. Mapping it ourselves would instead require reproducing
// net/http's UTS-46 behaviour exactly, including which failures it salvages, and staying
// identical to it across Go releases — and any divergence is a bypass of precisely this
// kind. It also needs a dependency this project does not carry (see the docstring on
// transferGuard for the whole enumeration).
//
// Nothing is lost that DNS itself carries: an internationalised host has an A-label
// (xn--) form, that form is ASCII, and it is what a resolver is given anyway.
func assertHostIsCanonicalASCII(field string, u *url.URL) *output.ExitError {
	host := u.Hostname()
	for i := 0; i < len(host); i++ {
		if host[i] >= utf8.RuneSelf {
			// %q escapes the non-ASCII runes, so a host chosen to read like a
			// different one is visible in the message rather than rendered as it.
			return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s host %q is not in canonical ASCII form, so it is not the host that would be connected to", field, host),
				"an internationalised host must be presented in its A-label (xn--…) form, which is what DNS carries; "+
					"the CLI will not map it, because the mapping applied later would not be the string that was checked")
		}
	}
	return nil
}

// urlParseCause strips the *url.Error wrapper that url.Parse returns, whose
// Error() quotes the entire input URL.
func urlParseCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

// assertSafeTransferTarget holds the transfer safety rules for an already-parsed
// URL, so the initial presigned URL and every redirect hop are judged by exactly
// the same code. Only absolute http(s) URLs are allowed, plain http only for
// loopback hosts and only when the configured Octo origin is itself loopback, so
// local development works without weakening production. Embedded credentials are
// refused outright — a userinfo component would be silently sent to the host and
// can also be used to disguise the real target.
func assertSafeTransferTarget(field string, u *url.URL, loopbackAPI bool) *output.ExitError {
	if u == nil || u.Host == "" || !u.IsAbs() {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must be an absolute URL", field),
			"the backend returned an unusable presigned URL; report it")
	}
	if u.User != nil {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must not embed credentials", field),
			"the backend returned an unusable presigned URL; report it")
	}
	// First, because every rule below reasons about the host as a string and this is
	// what guarantees that string is the one that gets dialled.
	if err := assertHostIsCanonicalASCII(field, u); err != nil {
		return err
	}
	if err := assertNumericHostIsAnIP(field, u); err != nil {
		return err
	}
	// An initial presigned URL is judged exactly as a redirect hop is. The https arm used to
	// be empty while CheckRedirect refused every local spelling, so `https://localhost/obj`
	// was accepted as the *first* upload_url or download_url and refused only if a hop reached
	// the same place. Two things made that worth closing rather than filing as narrow:
	//
	// The comment on the proxy-path narrowing (see transferGuard's row 4) justifies itself with
	// "the string pre-filter still rejects every literal local spelling before any of this".
	// That was not true for https, so the note a future reader uses to decide not to look here
	// was resting on a check that did not exist.
	//
	// And the exploitable path, while conditional, is real: with a proxy configured and a name
	// that does not resolve locally, the guard deliberately lets the proxy resolve it, so a
	// compromised backend returning `https://foo.localhost/obj` gets the PUT body — the user's
	// file — delivered to a service on the caller's own machine, or on the download side gets
	// that service's response written to -o. http.ProxyFromEnvironment's own loopback filter
	// does not cover "localhost." or "*.localhost", so it does not stand in for this. On the
	// direct path the dial-time address classification already closes it; this makes the two
	// paths agree instead of relying on which one is in use.
	if !loopbackAPI && isLoopbackHost(u.Hostname()) {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s points at a loopback host, which is not reachable object storage", field),
			"the storage endpoint named the local machine; report it")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s uses plain http on a non-loopback host", field),
				"object storage must be https outside local development")
		}
		if !loopbackAPI {
			return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s points at a loopback host, but the configured Octo origin is not local", field),
				"plain-http loopback object storage is accepted only against a local Octo origin")
		}
	default:
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s uses unsupported scheme %q", field, u.Scheme),
			"only http (loopback) and https are fetched")
	}
	return nil
}

// transferNetworkError reports an object-storage transfer failure without
// printing the URL.
//
// A presigned URL's signature lives in its query string, which makes the whole
// URL a short-lived bearer credential for that object — and *url.Error.Error()
// embeds it, so formatting the error straight into an envelope publishes it on
// stderr with no --verbose needed. The host is the part that carries the
// diagnostic value ("can I reach storage at all"), and the unwrapped cause keeps
// the actual reason (connection refused, TLS failure, timeout).
//
// A rejection raised by our own CheckRedirect arrives wrapped in *url.Error;
// that ExitError is returned as-is so an unsafe redirect keeps reporting
// UNSAFE_PRESIGNED_URL rather than being reclassified as a network fault.
func transferNetworkError(op string, u *url.URL, err error) *output.ExitError {
	cause := unwrapTransferError(err)
	if ee := output.AsExitError(cause); ee != nil {
		return ee
	}
	host := "object storage"
	if u != nil && u.Host != "" {
		host = strconv.Quote(u.Host)
	}
	return output.ErrNetwork(
		fmt.Sprintf("%s against %s failed: %v", op, host, cause),
		"check network access to object storage")
}

// unwrapTransferError strips the *url.Error wrappers whose Error() would print
// the presigned URL, leaving the cause that actually describes the failure.
func unwrapTransferError(err error) error {
	for {
		var ue *url.Error
		if !errors.As(err, &ue) || ue.Err == nil {
			return err
		}
		err = ue.Err
	}
}

// isLoopbackHost reports whether host names the local machine.
//
// The host is lower-cased and a trailing root dot stripped first, because
// url.Parse normalises neither while a resolver treats "LOCALHOST", "localhost."
// and "localhost" alike. Both directions this helper gates were wrong without
// that: a hop onto "LOCALHOST" was followed under a remote origin, and a
// developer whose configured origin read "LOCALHOST" was refused plain-http
// object storage.
//
// A numeric-looking host that net.ParseIP rejects is handled by
// assertNumericHostIsAnIP rather than here, so no resolver lookup happens on a
// validation path.
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	// RFC 6761 reserves .localhost for the loopback interface.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		// IsUnspecified as well as IsLoopback: connect() to 0.0.0.0 or [::]
		// reaches the local machine, so treating them as remote let a redirect
		// steer a transfer at a local service under a remote origin — and, in the
		// mirror-image, refused a dev object store advertising http://0.0.0.0:9000
		// under a local one.
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// assertNumericHostIsAnIP refuses a host that net.ParseIP rejects but a resolver would
// still read as an address.
//
// The gap is inet_aton-style parsing, which every platform resolver still implements for
// compatibility: each dot-separated label is read with strtoul semantics — decimal, a
// leading 0 for octal, a leading 0x for hex — and fewer than four labels are accepted by
// packing the last one into the remaining bytes. net.ParseIP implements none of that, so
// 0177.0.0.1, 0x7f.0.0.1, 0x7f000001, 2130706433 and 127.1 all fail ParseIP while
// resolving to 127.0.0.1.
//
// The previous rule asked whether the host was made of "digits and dots", which closed
// the zero-padded notation and left every other base open: 0x7f.0.0.1 contains an "x", so
// it was classified as an ordinary name. Rather than add the hex spelling — the next base
// would then be the next round — the question is inverted to mirror what the resolver
// itself does: **if ParseIP will not accept it, it must not be interpretable as a number
// in any of strtoul's bases.**
//
// This is the same move as requiring a canonical ASCII host one dimension over: make the
// set of accepted representations small enough that the guard and the resolver cannot
// disagree, instead of teaching the guard one more representation.
//
// A label of hex *letters* is not a number — a number needs a 0x prefix or a leading
// digit — so ordinary names keep working, including all-hex-letter labels like the .de
// TLD, which a rule keyed on "contains hex characters" would have broken. A host whose
// every label is numeric and which is not a valid IP is refused rather than resolved,
// which also keeps DNS off the validation path.
func assertNumericHostIsAnIP(field string, u *url.URL) *output.ExitError {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil {
		return nil
	}
	for _, label := range strings.Split(host, ".") {
		if !isNumericLabel(label) {
			return nil // at least one label is not a number: a real name
		}
	}
	return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
		fmt.Sprintf("%s has a numeric host %q that is not a valid IP address", field, u.Hostname()),
		"a host a resolver would read as a packed, octal, or hexadecimal address is refused "+
			"rather than resolved; report it")
}

// isNumericLabel reports whether strtoul would read label as a number, which is what
// decides whether a resolver may treat the host as an address rather than a name.
//
// Mirrors strtoul's base detection: "0x" prefix means hexadecimal, a leading "0" means
// octal, anything else decimal. Octal digits are a subset of decimal ones, so a single
// all-decimal-digits test covers both of the latter — and treating "09" as numeric even
// though strtoul would reject it as octal is deliberate: refusing is the fail-closed
// direction, and no storage host is spelled that way.
func isNumericLabel(label string) bool {
	if label == "" {
		return false
	}
	if rest, ok := strings.CutPrefix(label, "0x"); ok {
		if rest == "" {
			return false
		}
		for i := 0; i < len(rest); i++ {
			c := rest[i]
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(label); i++ {
		if label[i] < '0' || label[i] > '9' {
			return false
		}
	}
	return true
}

// --- local file writing ---

// downloadResult is the payload `download file` and `share download` emit.
type downloadResult struct {
	Path     string `json:"path"`
	Size     string `json:"size"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename"`
	ShareURL string `json:"share_url,omitempty"`
}

// fetchToFile downloads rawURL into target. The bytes land in a randomly-named
// sibling "<base>.<random>.part" first, are fsync'd, and only then renamed over
// target, so an interrupted transfer never leaves a truncated file that looks
// complete. The partial file is removed on any failure. Unless overwrite is set,
// an existing target is refused before a single byte is fetched.
func fetchToFile(cmd *cobra.Command, f *cmdutil.Factory, field, rawURL, target string, overwrite bool) (*downloadResult, *output.ExitError) {
	loopbackAPI := apiOriginIsLoopback(f)
	u, err := assertSafeTransferURL(field, rawURL, loopbackAPI)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, output.ErrValidation("--output is required", "pass -o with a destination file path")
	}
	if err := assertWritableTarget(target, overwrite); err != nil {
		return nil, err
	}

	req, rerr := http.NewRequestWithContext(cmd.Context(), http.MethodGet, rawURL, http.NoBody)
	if rerr != nil {
		return nil, transferNetworkError("download", u, rerr)
	}
	resp, rerr := transferClient(field, loopbackAPI, nil, verboseNoter(f)).Do(req)
	if rerr != nil {
		return nil, transferNetworkError("download", u, rerr)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	// Exactly 200, not any 2xx. This request never sends a Range header, so a 206 is
	// the storage host deciding to send part of the object on its own — and 204 is no
	// object at all. Accepting the whole 2xx family published both as complete
	// downloads: 204 wrote a 0-byte file and reported ok with the sha256 of nothing,
	// and 206 wrote a truncation and reported ok with a sha256 that certifies the
	// wrong bytes, which is worse than no checksum. The upload half of this command
	// applies the mirror of this rule — it admits only 200/201 and refuses the rest of
	// the 2xx family as "stored, unconfirmed" (see putObject). That sentence used to
	// claim the upload half "already refuses the same shape", which was not true when it
	// was written: putObject accepted every 2xx, so a 202/204 PUT was confirmed as a
	// stored object. It is true now, and it is stated here because a reader deciding
	// whether the other half needs looking at relies on it.
	if resp.StatusCode != http.StatusOK {
		return nil, output.ErrWithHint("api_error", "DOWNLOAD_FAILED",
			fmt.Sprintf("object storage returned status %d, not 200", resp.StatusCode),
			"the signed URL may have expired; re-run the command to get a fresh one")
	}

	// A random part file, created O_EXCL by os.CreateTemp, rather than a
	// predictable "<target>.part": the fixed name could be pre-created as a
	// symlink by anyone able to write the destination directory (truncating
	// whatever it pointed at with downloaded bytes — assertWritableTarget Lstats
	// target, never the part file), and two concurrent downloads to the same
	// destination would interleave into one file.
	part, oerr := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.part")
	if oerr != nil {
		return nil, output.ErrValidation(fmt.Sprintf("create a partial file next to %q: %v", target, oerr),
			"check the destination directory exists and is writable")
	}
	partPath := part.Name()
	cleanup := func() { _ = os.Remove(partPath) } //nolint:errcheck // best-effort cleanup

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(part, hasher), resp.Body)
	if copyErr != nil {
		_ = part.Close() //nolint:errcheck // already returning the copy error
		cleanup()
		ee := transferNetworkError("download", u, copyErr)
		ee.Hint = "transfer interrupted; the partial file was removed"
		return nil, ee
	}
	if terr := assertCompleteBody(written, resp.ContentLength); terr != nil {
		_ = part.Close() //nolint:errcheck // already failing
		cleanup()
		return nil, terr
	}
	if err := part.Sync(); err != nil {
		_ = part.Close() //nolint:errcheck // already returning the sync error
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("flush the partial file for %q: %v", target, err), "check available disk space")
	}
	partInfo, serr := sealPartFile(part, target)
	if serr != nil {
		cleanup()
		return nil, serr
	}
	// Re-check just before publishing so --overwrite=false does not clobber a
	// file that appeared while the transfer was running.
	if err := assertWritableTarget(target, overwrite); err != nil {
		cleanup()
		return nil, err
	}
	if err := publishDownload(partPath, target, overwrite, partInfo, nil); err != nil {
		cleanup()
		return nil, err
	}

	filename := resp.Header.Get("X-Octo-Filename")
	if filename == "" {
		filename = filepath.Base(target)
	}
	progressf(f, "downloaded %d bytes to %s", written, target)
	return &downloadResult{
		Path:     target,
		Size:     fmt.Sprintf("%d", written),
		SHA256:   hex.EncodeToString(hasher.Sum(nil)),
		Filename: filename,
	}, nil
}

// sealPartFile finishes the partial file and returns its identity.
//
// Mode and identity are both taken through the descriptor, before it is closed.
// fchmod cannot be redirected by a symlink the way a path-based chmod can, and the
// recorded identity is what publishDownload compares the path against — so a part
// file swapped between here and publication is detected rather than acted on.
func sealPartFile(part *os.File, target string) (os.FileInfo, *output.ExitError) {
	if err := applyDownloadMode(part, target); err != nil {
		_ = part.Close() //nolint:errcheck // already returning the chmod error
		return nil, err
	}
	info, statErr := part.Stat()
	if statErr != nil {
		_ = part.Close() //nolint:errcheck // already returning the stat error
		return nil, output.ErrValidation(fmt.Sprintf("stat the partial file for %q: %v", target, statErr), "")
	}
	if err := part.Close(); err != nil {
		return nil, output.ErrValidation(fmt.Sprintf("close the partial file for %q: %v", target, err), "")
	}
	return info, nil
}

// publishDownload moves the completed partial file onto target.
//
// Without --overwrite the publication is a hard link, which fails with EEXIST
// atomically and in the same directory — so "refuse an existing destination"
// becomes an actual guarantee rather than a check-then-rename window a second
// writer can slip through. With --overwrite, replacement is the point, so rename
// is correct and unconditional replacement is what the caller asked for. A
// filesystem without hard links falls back to rename, which is the previous
// behaviour rather than a failure.
//
// created is the part file as it was when the descriptor was still open. The path
// is re-Lstat'd here and compared against it, because os.Link and os.Rename act on
// whatever the name resolves to now: if something replaced the part file between
// close and publication, this refuses instead of publishing a file the CLI never
// wrote — the alternative being a success envelope whose path and sha256 describe
// different files.
// beforePublish is a test seam, nil in production, invoked between the
// pre-publication check and the publication itself. It exists because the identity
// comparison after publication is only reachable through that window: its own unit
// test proves what the function does, and this parameter is what proves the function
// is still called.
func publishDownload(partPath, target string, overwrite bool, created os.FileInfo, beforePublish func()) *output.ExitError {
	return publishDownloadWithLinker(partPath, target, overwrite, created, beforePublish, os.Link)
}

// publishDownloadWithLinker is publishDownload with the link step injectable, so a test
// can exercise the no-hard-links fallback without needing a filesystem that lacks them.
func publishDownloadWithLinker(partPath, target string, overwrite bool, created os.FileInfo,
	beforePublish func(), link func(oldname, newname string) error,
) *output.ExitError {
	// published records whether the rename actually happened, so the reserved name is
	// cleaned up on failure and kept on success.
	var published bool

	if err := assertPartFileUnchanged(partPath, created); err != nil {
		return err
	}
	if beforePublish != nil {
		beforePublish()
	}
	if !overwrite {
		switch err := link(partPath, target); {
		case err == nil:
			_ = os.Remove(partPath) //nolint:errcheck // best-effort cleanup of the link source
			return assertPublishedFileMatches(target, created)
		case errors.Is(err, os.ErrExist):
			return output.ErrWithHint("validation", "FILE_EXISTS",
				fmt.Sprintf("%q already exists", target),
				"pass --overwrite to replace it, or choose another path")
		}
		// Hard links unavailable (no link support, or cross-device). Falling straight
		// through to os.Rename used to silently turn --overwrite=false into overwrite,
		// because rename replaces its destination unconditionally — so the refusal
		// guarantee depended on which filesystem the download happened to land on.
		//
		// O_CREATE|O_EXCL restores it: the same atomic "already exists" refusal os.Link
		// was providing, from a syscall every filesystem supports. The rename below then
		// replaces this CLI's own placeholder rather than someone else's file, so the
		// name is reserved for the whole window instead of being re-checked and hoped for.
		placeholder, perr := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if perr != nil {
			if errors.Is(perr, os.ErrExist) {
				return output.ErrWithHint("validation", "FILE_EXISTS",
					fmt.Sprintf("%q already exists", target),
					"pass --overwrite to replace it, or choose another path")
			}
			return output.ErrValidation(fmt.Sprintf("reserve %q: %v", target, perr), "")
		}
		// Remember what was created so a failed rename can clean it up without
		// deleting a file that something else put at the name in the meantime.
		reserved, sterr := placeholder.Stat()
		_ = placeholder.Close() //nolint:errcheck // nothing was written to it
		if sterr == nil {
			defer removeReservedPlaceholder(target, reserved, &published)
		}
	}
	if err := os.Rename(partPath, target); err != nil {
		return output.ErrValidation(fmt.Sprintf("finalise %q: %v", target, err), "")
	}
	published = true
	return assertPublishedFileMatches(target, created)
}

// removeReservedPlaceholder deletes the empty file the no-hard-links fallback created to
// reserve the destination name, when publication did not go on to replace it.
//
// Without this, a rename that fails after the reservation left a zero-length file at the
// destination: the command reports the error, so it is not a false success, but it does
// leave something that looks like a finished download, which is the property the whole
// part-file dance exists to provide. The identity is checked before removing, so a file
// another writer put at that name between the failure and the cleanup is left alone
// rather than deleted on its behalf.
func removeReservedPlaceholder(target string, reserved os.FileInfo, published *bool) {
	if *published || reserved == nil {
		return
	}
	// sameDownloadedFile, not bare os.SameFile: this PR established at that helper that
	// device+inode alone cannot tell "the file I created" from "a different file that
	// reused its inode", which ext4 and tmpfs both do. The other two identity checks
	// were switched over; this one was missed, and it is the one that *deletes* — so
	// the failure mode was removing a file another writer had just created, while the
	// docstring above promised exactly the opposite.
	now, err := os.Lstat(target)
	if err != nil || !sameDownloadedFile(now, reserved) {
		return
	}
	_ = os.Remove(target) //nolint:errcheck // best-effort cleanup of our own placeholder
}

// assertPublishedFileMatches confirms the destination now holds the file this
// transfer wrote.
//
// The Lstat in assertPartFileUnchanged narrows the swap window to a single syscall
// but cannot close it: os.Link and os.Rename act on whatever the part path resolves
// to at the moment they run, and on Linux linking or renaming a symlink publishes the
// symlink. Closing the window itself would need linkat on the retained descriptor via
// /proc/self/fd/N, which is Linux-only; refusing to *report success* is portable and
// removes the harm that mattered — a caller reading back "its" file and getting
// someone else's, under an envelope carrying a path and sha256 that describe
// different bytes.
// sameDownloadedFile decides whether now is still the file described by created.
//
// os.SameFile alone is not enough, and CI on Linux is what proved it: it compares
// device and inode, and a filesystem is free to hand the inode of a just-deleted file
// straight back to the next file created in that directory. ext4 and tmpfs do exactly
// that, so remove-then-create at the same path produced a *different* file that
// SameFile called identical — precisely the substitution these two checks exist to
// refuse. macOS did not reuse the inode, which is why it went unnoticed until the
// change ran on another platform.
//
// Size and modification time close that gap. Both come from the fstat taken while the
// CLI still held the descriptor, so they describe the bytes it wrote, and a
// replacement has to match all three to pass. That is not a cryptographic identity —
// a local process able to set an exact mtime and size could still forge it — and it is
// not meant to be: the guarantee for the no-overwrite path is the atomic os.Link
// below, and this is defence in depth for the window around it.
func sameDownloadedFile(now, created os.FileInfo) bool {
	return now.Mode().IsRegular() &&
		os.SameFile(now, created) &&
		now.Size() == created.Size() &&
		now.ModTime().Equal(created.ModTime())
}

func assertPublishedFileMatches(target string, created os.FileInfo) *output.ExitError {
	if created == nil {
		return output.ErrValidation("the published file was not identified before publication", "")
	}
	now, err := os.Lstat(target)
	if err != nil {
		return output.ErrValidation(fmt.Sprintf("verify the published file: %v", err), "")
	}
	if !sameDownloadedFile(now, created) {
		return output.ErrWithHint("validation", "PARTIAL_FILE_REPLACED",
			"the destination does not hold the file this download wrote",
			"another process wrote to the destination directory mid-download; re-run, and prefer a directory only you can write")
	}
	return nil
}

// assertPartFileUnchanged refuses to publish when the part path no longer names
// the file the transfer wrote. Lstat, not Stat: a symlink planted at the path must
// be seen as a symlink rather than followed to its target.
func assertPartFileUnchanged(partPath string, created os.FileInfo) *output.ExitError {
	if created == nil {
		return output.ErrValidation("the partial file was not identified before publication", "")
	}
	now, err := os.Lstat(partPath)
	if err != nil {
		return output.ErrValidation(fmt.Sprintf("re-check the partial file: %v", err), "")
	}
	if !sameDownloadedFile(now, created) {
		return output.ErrWithHint("validation", "PARTIAL_FILE_REPLACED",
			"the partial download file was replaced before it could be published",
			"another process wrote to the destination directory mid-download; re-run, and prefer a directory only you can write")
	}
	return nil
}

// assertCompleteBody refuses a body that is not the whole object.
//
// Extracted so fetchToFile stays under the complexity limit, and because the two
// conditions are one question: did we receive the object, or part of it?
//
// contentLength is -1 for a chunked or close-delimited response, where nothing was
// promised and there is nothing to compare against — the copy error is then the only
// signal available. (Under a Content-Length the transport's own body reader reports a short
// read before this is reached; the comparison here is the backstop for the shapes it does
// not police, and Content-Encoding is refused on this transport so a compressed reply cannot
// blank the length out. See transferClientWithGuard.)
//
// # The empty case is this CLI's rule, not a claim about storage
//
// An empty body is refused, and the reason is worth stating precisely because the hint used
// to blame the wrong party. Every upload path in *this* CLI rejects an empty file
// (cmd/drive_upload.go:132), so within this CLI a zero-byte blob cannot be created — but the
// backend contract is wider: `blob create` documents `size: 0` as "a stated value, not an
// omission" (internal/registry/specs/drive.json), so a 0-byte blob registered by another
// client is a legitimate row rather than evidence of a storage fault.
//
// The behaviour is deliberately unchanged: a zero-byte object and a transfer that delivered
// nothing are indistinguishable at this point — DownloadURL carries no declared size or
// checksum to separate them — and publishing an empty file with the sha256 of nothing is the
// failure this guard exists to prevent. What changed is the attribution: the refusal now
// names itself as a CLI limitation instead of telling the operator to report a storage bug
// they do not have. Widening it to admit 0 bytes would need the backend to declare a length
// the CLI can check against, which is a product decision rather than a fix here.
//
// Publishing either shape reported ok with a sha256 that describes the wrong bytes, and a
// checksum that certifies a truncation is worse than no checksum, because a caller
// verifying it concludes the transfer was sound.
func assertCompleteBody(written, contentLength int64) *output.ExitError {
	if contentLength >= 0 && written != contentLength {
		return output.ErrWithHint("api_error", "DOWNLOAD_TRUNCATED",
			fmt.Sprintf("object storage sent %d of %d bytes", written, contentLength),
			"the transfer ended early; re-run the command to get a fresh signed URL")
	}
	if written == 0 {
		return output.ErrWithHint("api_error", "DOWNLOAD_TRUNCATED",
			"object storage sent an empty body",
			"this CLI refuses a zero-byte download, because an empty object and a transfer that "+
				"delivered nothing cannot be told apart here; re-run once, and if the blob really is "+
				"0 bytes report the refusal as a CLI limitation rather than a storage fault")
	}
	return nil
}

// applyDownloadMode gives the partial file the mode its destination should end up
// with: an existing target's own mode, so --overwrite does not narrow a file the
// caller widened on purpose, and 0600 for a fresh one. (The binary --output path
// in internal/client uses 0644, which is why leaving this implicit made two
// downloads in one CLI disagree.)
//
// The chmod goes through the open descriptor. A path-based os.Chmod is the one
// operation in this sequence that follows a symlink, so doing it by path handed
// back exactly the arbitrary-file write the random O_EXCL name exists to prevent.
func applyDownloadMode(part *os.File, target string) *output.ExitError {
	// Lstat, matching the write path: os.Stat follows a symlink, so a symlink planted
	// at the destination would have donated its *target's* mode to the file this
	// download publishes. Only a regular file's mode is inherited.
	mode := os.FileMode(0o600)
	if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	if err := part.Chmod(mode); err != nil {
		return output.ErrValidation(fmt.Sprintf("set mode on the partial file for %q: %v", target, err), "")
	}
	return nil
}

// assertWritableTarget refuses an existing destination unless overwrite is set,
// and refuses a destination that is not a regular file even with --overwrite
// (renaming over a directory or a device would fail or do damage).
//
// The Lstat is deliberate: a symlink at the destination is refused even with
// --overwrite, so a download never writes through one.
func assertWritableTarget(target string, overwrite bool) *output.ExitError {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return output.ErrValidation(fmt.Sprintf("stat %q: %v", target, err), "check the destination path")
	}
	if !info.Mode().IsRegular() {
		return output.ErrValidation(
			fmt.Sprintf("%q exists and is not a regular file", target),
			"choose a different destination path")
	}
	if !overwrite {
		return output.ErrWithHint("validation", "FILE_EXISTS",
			fmt.Sprintf("%q already exists", target),
			"pass --overwrite to replace it, or choose another path")
	}
	return nil
}

// progressf writes a transfer progress line to stderr. It is gated on
// --verbose so stdout stays pure JSON and a normal agent run stays quiet.
func progressf(f *cmdutil.Factory, format string, args ...any) {
	if f.Globals == nil || !f.Globals.Verbose {
		return
	}
	fmt.Fprintf(f.ErrOut(), "[octo] "+format+"\n", args...) //nolint:errcheck // stderr progress line
}

// --- shared JSON helpers ---

// decodeLossless unmarshals a backend body with UseNumber so uint64 ids keep
// their exact decimal text on the way through the composite commands.
func decodeDriveResponse(raw []byte, into any) *output.ExitError {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			fmt.Sprintf("unexpected response shape: %v", err),
			"report the response the backend returned")
	}
	return nil
}
