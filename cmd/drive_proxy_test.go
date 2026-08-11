package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Round-10 P1. The transfer transport was built with Clone(), which preserves
// Proxy: http.ProxyFromEnvironment. When the transport picks a proxy, DialContext is
// handed the *proxy's* host:port — for plain HTTP and for the CONNECT tunnel alike —
// and the presigned URL's host never reaches it. So the round-9 guard, which is
// truthful about whatever address it is given, was classifying the wrong machine:
//
//   - false positive: a proxy on the local machine (an ordinary debugging or
//     TLS-inspection setup) was refused as though object storage had pointed the
//     transfer at the caller, breaking all three transfer commands and telling the
//     user to report a backend bug;
//   - false negative: with any proxy set, the target was never resolved and never
//     classified, so the invariant the docstring states was not enforced at all.
//
// The fix splits the two questions across the two hooks that can answer them. Proxy
// receives the *request*, so it is the only place that knows the target when a proxy
// is in play; DialContext receives the *connection*, so it is the only place that can
// bind the address that was validated to the socket that opens.
//
// The proxy is injected rather than set in the environment. http.ProxyFromEnvironment
// memoises the environment in a package-level sync.Once, so the *first* call anywhere
// in the test binary fixes the answer for every later one: a t.Setenv-based proxy test
// asserts whatever the first test to touch a transport happened to see, and flips
// meaning when test order changes. The package's TestMain sweeps the proxy family so
// no ambient value leaks in, and the cases below inject g.proxy for determinism —
// the same reason the resolver is injected.

// TestTransferClient_KeepsProxySupport is the other half of choosing to wrap Proxy
// rather than set it to nil. Setting it to nil would make the guard's claim true by
// construction, at the price of every deployment that can only reach object storage
// through a proxy — enterprise egress and offshore networks are exactly that shape.
// Nothing may quietly take that back.
func TestTransferClient_KeepsProxySupport(t *testing.T) {
	tr, ok := transferClient("download_url", false, nil, nil).Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T, want *http.Transport", tr)
	}
	if tr.Proxy == nil {
		t.Error("Proxy is nil, so a deployment that can only reach storage through a proxy can no longer transfer")
	}
	// The clone-from-global hazard: a TLS dial hook would take precedence over
	// DialContext for https and bypass the guard entirely.
	if tr.DialTLSContext != nil || tr.DialTLS != nil { //nolint:staticcheck // DialTLS is deprecated but a non-nil value would still be honoured
		t.Error("a TLS dial hook is set, which would bypass DialContext for https transfers")
	}
}

// fixedProxy selects the same proxy for every request.
func fixedProxy(t *testing.T, rawURL string) func(*http.Request) (*url.URL, error) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return func(*http.Request) (*url.URL, error) { return u, nil }
}

// TestTransferProxy_ClassifiesTheTargetNotTheProxy is the false-negative half: with a
// proxy configured, the presigned target must still be resolved and classified.
//
// Before the fix this passed the request to the proxy without ever looking at the
// target, so the assertion is not "an error came back" — a bogus proxy address
// produces one of those too. It is "the guard resolved the target host, and the
// refusal names the target".
func TestTransferProxy_ClassifiesTheTargetNotTheProxy(t *testing.T) {
	const target = "storage-evil.example.invalid"

	var resolved []string
	resolve := func(_ context.Context, host string) ([]net.IPAddr, error) {
		resolved = append(resolved, host)
		switch host {
		case target:
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		case "proxy.example":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}

	g := newTestTransferGuard(t, false, resolve)
	g.proxy = fixedProxy(t, "http://proxy.example:8080")

	req, err := http.NewRequest(http.MethodGet, "https://"+target+"/obj", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, derr := transferClientWithGuard(g).Do(req)
	if derr == nil {
		_ = resp.Body.Close()
		t.Fatal("a target on the local machine was allowed through because a proxy was configured")
	}

	var sawTarget bool
	for _, h := range resolved {
		if h == target {
			sawTarget = true
		}
	}
	if !sawTarget {
		t.Errorf("the guard never resolved the target %q (it resolved %v), so the invariant is not enforced "+
			"under a proxy — an error alone does not show it was", target, resolved)
	}
	if !strings.Contains(derr.Error(), "UNSAFE_PRESIGNED_URL") {
		t.Errorf("the refusal should be the target rule, not an incidental proxy failure, got %v", derr)
	}
}

// TestTransferProxy_LoopbackProxyWithRemoteTargetIsNotRefused is the regression lock
// for the false positive. A proxy on the local machine is the operator's own
// configuration and is not the storage host, so the storage rule does not apply to
// it. The transfer must complete.
//
// The proxy here is a real local server, so this exercises the whole path rather
// than only the guard's decision.
func TestTransferProxy_LoopbackProxyWithRemoteTargetIsNotRefused(t *testing.T) {
	const target = "storage-ok.example.invalid"

	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.String())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("object-bytes"))
	}))
	defer proxy.Close()

	g := newTransferGuard("download_url", false, stubIPAddrResolver(t, map[string][]string{
		target: {"203.0.113.9"},
	}))
	g.proxy = fixedProxy(t, proxy.URL)

	req, err := http.NewRequest(http.MethodGet, "http://"+target+"/obj", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, derr := transferClientWithGuard(g).Do(req)
	if derr != nil {
		if ee := output.AsExitError(derr); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
			t.Fatalf("a proxy on the local machine is not the storage host and must not be refused as one: %v", derr)
		}
		t.Fatalf("transfer through a local proxy failed: %v", derr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(proxied) == 0 {
		t.Error("the request did not reach the proxy, so the proxy is not being used")
	}
}

// TestTransferProxy_ErrorsAttributeTheRightMachine pins the distinction the previous
// single message destroyed. Both conditions put a connection on the local machine,
// but they have opposite causes and opposite remedies, and a user whose only mistake
// is a local proxy must not be told to file a backend bug.
func TestTransferProxy_ErrorsAttributeTheRightMachine(t *testing.T) {
	resolve := stubIPAddrResolver(t, map[string][]string{
		"storage-evil.example.invalid": {"127.0.0.1"},
		"elsewhere.example.invalid":    {"127.0.0.1"},
	})

	t.Run("the presigned target is the local machine: the backend is at fault", func(t *testing.T) {
		g := newTestTransferGuard(t, false, resolve)
		req, _ := http.NewRequest(http.MethodGet, "https://storage-evil.example.invalid/obj", http.NoBody)
		_, err := g.Proxy(req)
		ee := output.AsExitError(err)
		if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
			t.Fatalf("err = %v, want UNSAFE_PRESIGNED_URL", err)
		}
		if !strings.Contains(ee.Hint, "report") {
			t.Errorf("the storage endpoint chose this address, so the hint should ask for it to be reported; hint = %q", ee.Hint)
		}
		if strings.Contains(strings.ToLower(ee.Hint), "proxy") {
			t.Errorf("this is not a proxy problem and the hint must not suggest it is; hint = %q", ee.Hint)
		}
	})

	t.Run("something local redirected the connection: the environment is at fault", func(t *testing.T) {
		g := newTestTransferGuard(t, false, resolve)
		// A host the request never named and no proxy selected — the shape a
		// connection takes when something in the environment reroutes it.
		_, err := g.DialContext(context.Background(), "tcp", "elsewhere.example.invalid:443")
		ee := output.AsExitError(err)
		if ee == nil {
			t.Fatalf("err = %v, want a structured error", err)
		}
		if ee.Code == "UNSAFE_PRESIGNED_URL" {
			t.Error("this is not the presigned target, so it must not be reported as one")
		}
		if strings.Contains(ee.Hint, "report") {
			t.Errorf("the backend did not choose this address and must not be blamed for it; hint = %q", ee.Hint)
		}
		if !strings.Contains(strings.ToLower(ee.Hint), "proxy") {
			t.Errorf("the remedy is in the local proxy configuration, so the hint should say so; hint = %q", ee.Hint)
		}
	})
}

// TestTransferProxy_SelectedProxyOnLoopbackIsDialledNotClassified isolates the
// dialer's half of the rule from the transport, so the allow direction is pinned even
// if the wiring changes.
func TestTransferProxy_SelectedProxyOnLoopbackIsDialledNotClassified(t *testing.T) {
	const target = "storage-ok.example.invalid"
	resolve := stubIPAddrResolver(t, map[string][]string{target: {"203.0.113.9"}})

	g := newTestTransferGuard(t, false, resolve)
	g.proxy = fixedProxy(t, "http://127.0.0.1:3128")

	req, _ := http.NewRequest(http.MethodGet, "https://"+target+"/obj", http.NoBody)
	p, err := g.Proxy(req)
	if err != nil {
		t.Fatalf("a remote target with a local proxy must be allowed: %v", err)
	}
	if p == nil {
		t.Fatal("the proxy selection was dropped")
	}

	if _, derr := g.DialContext(context.Background(), "tcp", "127.0.0.1:3128"); derr != nil {
		if ee := output.AsExitError(derr); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
			t.Fatalf("the selected proxy must be dialled, not classified as storage: %v", derr)
		}
	}
}

// stubIPAddrResolver maps names to addresses deterministically.
func stubIPAddrResolver(t *testing.T, table map[string][]string) hostResolver {
	t.Helper()
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		addrs, ok := table[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		out := make([]net.IPAddr, 0, len(addrs))
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				t.Fatalf("stub table has a bad address %q for %q", a, host)
			}
			out = append(out, net.IPAddr{IP: ip})
		}
		return out, nil
	}
}

// newTestTransferGuard builds the guard with the dial step stubbed, so no test in
// this file opens a socket to a target.
func newTestTransferGuard(t *testing.T, loopbackAPI bool, resolve hostResolver) *transferGuard {
	t.Helper()
	g := newTransferGuard("download_url", loopbackAPI, resolve)
	g.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		return nil, errors.New("stub dial reached " + addr)
	}
	return g
}

// --- Round-11 P1: the proxy path must not depend on a local lookup ---
//
// Round 10 put the target classification in Proxy, which was the right place, but
// resolved *before* asking whether a proxy was selected and treated a resolution
// failure as fatal. http.Transport never resolves the target when it proxies —
// "CONNECT host:port" exists so the proxy resolves it — so a proxy-only network
// (tightened egress with no external resolver, or a split-horizon name only the
// proxy's resolver answers) had all three transfer commands fail on a lookup nobody
// needed, while the API client kept working because it does not pre-resolve.
//
// These cases are one per row of the path table on transferGuard: row 4 (the
// regression), row 1 unchanged, row 3 still classified, row 2/5 not paying for a
// discarded lookup.

// newProxyServer starts a local server that answers any proxied request 200.
//
// The requests are handed back through a snapshot function, not a pointer to the slice:
// the handler runs on the server's goroutine. Reading it directly happened to be safe
// here because completing the response creates a happens-before edge, but relying on
// that is how a test becomes flaky later, and the sibling helper below has no such edge.
func newProxyServer(t *testing.T) (srv *httptest.Server, requests func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.String())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("object-bytes"))
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// failingResolver answers nothing, like a host with no external DNS.
func failingResolver(t *testing.T, resolved *[]string) hostResolver {
	t.Helper()
	return func(_ context.Context, host string) ([]net.IPAddr, error) {
		*resolved = append(*resolved, host)
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
}

// TestTransferProxy_UnresolvableTargetStillTransfersThroughTheProxy is row 4: the
// half of "we support proxy-only deployments" that was missing.
//
// TestTransferClient_KeepsProxySupport only asserted Proxy != nil, which covers the
// variant where outbound TCP is blocked but public DNS still answers. It says nothing
// about the variant where the proxy is also the resolver — and in that variant the
// claim the comment makes was false.
func TestTransferProxy_UnresolvableTargetStillTransfersThroughTheProxy(t *testing.T) {
	const target = "storage.internal.example.invalid"
	proxy, proxied := newProxyServer(t)

	var resolved []string
	g := newTransferGuard("download_url", false, failingResolver(t, &resolved))
	g.proxy = fixedProxy(t, proxy.URL)

	req, err := http.NewRequest(http.MethodGet, "http://"+target+"/obj", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, derr := transferClientWithGuard(g).Do(req)
	if derr != nil {
		t.Fatalf("the proxy resolves the target, so a local lookup failure must not fail the transfer: %v", derr)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if len(proxied()) == 0 {
		t.Error("the request never reached the proxy")
	}
}

// TestTransferProxy_UnclassifiedTargetIsReportedUnderVerbose keeps the previous case
// honest. Allowing an unclassified target is a deliberate narrowing of the invariant,
// so it has to be visible to an operator rather than silent.
func TestTransferProxy_UnclassifiedTargetIsReportedUnderVerbose(t *testing.T) {
	const target = "storage.internal.example.invalid"
	proxy, _ := newProxyServer(t)

	var resolved, notes []string
	g := newTransferGuard("download_url", false, failingResolver(t, &resolved))
	g.proxy = fixedProxy(t, proxy.URL)
	g.note = func(format string, args ...any) {
		notes = append(notes, fmt.Sprintf(format, args...))
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+target+"/obj", http.NoBody)
	resp, derr := transferClientWithGuard(g).Do(req)
	if derr != nil {
		t.Fatalf("transfer: %v", derr)
	}
	defer resp.Body.Close()

	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, target) {
		t.Errorf("--verbose should name the host that went unclassified; notes = %q", joined)
	}
	if !strings.Contains(joined, "not classified") {
		t.Errorf("--verbose should say the target was not classified; notes = %q", joined)
	}
}

// TestTransferProxy_DirectPathStillFailsClosedOnAResolutionFailure is row 1, and the
// boundary of the change: leniency is conditional on a proxy having been selected. With
// no proxy this guard *is* the resolver, so a name it cannot resolve must not be
// handed onward — that is round 9's property and it stays.
func TestTransferProxy_DirectPathStillFailsClosedOnAResolutionFailure(t *testing.T) {
	var resolved, dialled []string
	g := newTransferGuard("download_url", false, failingResolver(t, &resolved))
	g.proxy = func(*http.Request) (*url.URL, error) { return nil, nil } // no proxy
	g.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		return nil, errors.New("stub dial reached " + addr)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://nothing-here.example.invalid/obj", http.NoBody)
	if _, err := g.Proxy(req); err == nil {
		t.Error("with no proxy selected, a name this guard cannot resolve must fail rather than be handed onward")
	}
	if len(dialled) > 0 {
		t.Errorf("nothing should have been dialled, got %v", dialled)
	}
}

// TestTransferProxy_AResolvableTargetIsStillClassifiedUnderAProxy is row 3. Leniency
// applies to a *failure to resolve*, never to an answer that says "local": when the
// lookup succeeds, the rule is enforced exactly as before.
func TestTransferProxy_AResolvableTargetIsStillClassifiedUnderAProxy(t *testing.T) {
	const target = "storage-evil.example.invalid"
	g := newTestTransferGuard(t, false, stubIPAddrResolver(t, map[string][]string{
		target: {"127.0.0.1"},
	}))
	g.proxy = fixedProxy(t, "http://proxy.example:8080")

	req, _ := http.NewRequest(http.MethodGet, "https://"+target+"/obj", http.NoBody)
	_, err := g.Proxy(req)
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
		t.Fatalf("a target that resolves to the local machine must still be refused under a proxy, got %v", err)
	}
}

// TestTransferProxy_LoopbackOriginDoesNotPayForADiscardedLookup is rows 2 and 5. With
// the rule switched off by configuration there is nothing to classify, so resolving in
// Proxy buys nothing — and on a proxy-only network it was another way to fail.
func TestTransferProxy_LoopbackOriginDoesNotPayForADiscardedLookup(t *testing.T) {
	var resolved []string
	g := newTransferGuard("download_url", true, failingResolver(t, &resolved))
	g.proxy = fixedProxy(t, "http://proxy.example:8080")

	req, _ := http.NewRequest(http.MethodGet, "https://storage.example.invalid/obj", http.NoBody)
	if _, err := g.Proxy(req); err != nil {
		t.Fatalf("a loopback origin classifies nothing and must not fail here: %v", err)
	}
	if len(resolved) > 0 {
		t.Errorf("Proxy resolved %v with the rule switched off; the answer is discarded, so the lookup is pure cost "+
			"and one more way to fail on a network without external DNS", resolved)
	}
}

// TestTransferProxy_InvalidProxyNeverEchoesTheRawValue is the credential assertion.
//
// The selector's error text can carry the raw environment value: x/net/httpproxy's
// parseProxy formats it with %q into "invalid proxy address %q", and a proxy URL is
// commonly http://user:password@host:port — enterprise TLS-inspection and paid egress
// proxies routinely carry basic auth. Passing that text through put the user's proxy
// password into the structured error on stderr, with no --verbose required. Same defect
// class as the share-token leaks earlier in this PR, with the user's credential instead
// of the backend's.
func TestTransferProxy_InvalidProxyNeverEchoesTheRawValue(t *testing.T) {
	const password = "s3cr3t-pass"
	const raw = "http://alice:" + password + "@proxy.example:3128\x7f"

	g := newTestTransferGuard(t, false, stubIPAddrResolver(t, map[string][]string{
		"storage-ok.example.invalid": {"203.0.113.9"},
	}))
	g.proxy = func(*http.Request) (*url.URL, error) {
		// Verbatim shape of x/net/http/httpproxy.parseProxy's error.
		return nil, fmt.Errorf("invalid proxy address %q: parse error", raw)
	}

	req, _ := http.NewRequest(http.MethodGet, "https://storage-ok.example.invalid/obj", http.NoBody)
	_, err := g.Proxy(req)
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("a bad proxy configuration should be a structured error, got %v", err)
	}
	visible := ee.Code + " " + ee.Message + " " + ee.Hint + " " + string(ee.Detail)
	if strings.Contains(visible, password) {
		t.Errorf("the proxy password reached the error envelope: %s", visible)
	}
	if strings.Contains(visible, "alice") {
		t.Errorf("the proxy userinfo reached the error envelope: %s", visible)
	}
	if !strings.Contains(strings.ToLower(visible), "proxy") {
		t.Errorf("the diagnostic must still say what went wrong: %s", visible)
	}
}

// TestTransferProxy_SelectedProxyHostIsNamedWithoutCredentials is the other half: a
// proxy URL that parses may be named in diagnostics, but only by host.
func TestTransferProxy_SelectedProxyHostIsNamedWithoutCredentials(t *testing.T) {
	g := newTestTransferGuard(t, false, stubIPAddrResolver(t, map[string][]string{
		"storage-ok.example.invalid": {"203.0.113.9"},
	}))
	g.proxy = fixedProxy(t, "http://alice:s3cr3t-pass@proxy.example:3128")

	req, _ := http.NewRequest(http.MethodGet, "https://storage-ok.example.invalid/obj", http.NoBody)
	if _, err := g.Proxy(req); err != nil {
		t.Fatalf("a valid proxy must be accepted: %v", err)
	}
	// The recorded key is what any later diagnostic would use.
	for host := range g.proxies {
		if strings.Contains(host, "s3cr3t-pass") || strings.Contains(host, "alice") {
			t.Errorf("the recorded proxy identity carries credentials: %q", host)
		}
	}
}

// --- Round-12 P0: one host, one spelling ---
//
// The round-11 path table enumerated configurations and assumed the host was a single
// value. It is not. net/http rewrites it on the way to the socket — canonicalAddr runs
// idnaASCII, which is the identity on ASCII and UTS-46 (idna.Lookup.ToASCII) otherwise
// — so for a non-ASCII host this file's string rules and classification judged one
// string while the dial and the CONNECT authority used another.
//
// Concretely: the fullwidth spellings of 127.0.0.1 and localhost are ordinary names to
// every string rule, and Go's resolver performs no IDNA so the lookup *cannot* succeed,
// which put them in row 4 of the table — unclassified, allowed — after which net/http
// mapped them to the loopback address and dialled it. Deterministic, not luck. Direction
// matters too: `upload file` PUTs local file bytes to whatever that turns out to be.
//
// Every existing fixture in this file is ASCII, which is exactly why the round-11 tests
// covering rows 1-5 could not observe any of this.

// Fullwidth spellings, written as escapes so the intent survives any editor or diff
// tool. UTS-46 maps each to the ASCII on the right.
const (
	fullwidthDottedIP = "127．0．0．1" // -> 127.0.0.1
	fullwidthDigitIP  = "１２７．0．0．１" // -> 127.0.0.1
	fullwidthLocalimp = "ｌｏｃａｌｈｏｓｔ"
)

// connectCapturingProxy is a proxy that records the authority of every request it is
// asked to make — the CONNECT target for https, the absolute-URI host for plain http.
// That authority is the host net/http actually decided to reach, which is the value the
// guard has to have agreed with.
// The authorities are returned through a snapshot function rather than a pointer to the
// slice: the handler runs on the server's own goroutine, so reading the slice directly
// from the test goroutine is a data race, and -race is part of the gate for a reason.
func connectCapturingProxy(t *testing.T) (srv *httptest.Server, asked func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method == http.MethodConnect {
			seen = append(seen, r.Host)
		} else {
			seen = append(seen, r.URL.Host)
		}
		mu.Unlock()

		if r.Method == http.MethodConnect {
			// Refuse the tunnel: what was asked for is the whole assertion, and
			// completing it would mean actually connecting somewhere.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), seen...)
	}
}

// TestTransferProxy_TheHostClassifiedIsTheHostDialled is the property, stated so it
// cannot be satisfied by learning one more spelling: no host may be reached that the
// guard did not classify under that exact name.
//
// It is deliberately not "the fullwidth dot is rejected". A test for the three spellings
// in the report would pass while the next Unicode mapping walked through, which is the
// same shape of mistake as fixing the path in front of you without enumerating the set.
func TestTransferProxy_TheHostClassifiedIsTheHostDialled(t *testing.T) {
	for _, host := range []string{
		fullwidthDottedIP,
		fullwidthDigitIP,
		fullwidthLocalimp,
		"storage.example.invalid", // ASCII control: must still work end to end
		"xn--nxasmq6b.example",    // an A-label is ASCII, so it is the supported way to name an IDN
	} {
		t.Run(url.QueryEscape(host), func(t *testing.T) {
			proxy, asked := connectCapturingProxy(t)

			var classified []string
			var mu sync.Mutex
			g := newTransferGuard("download_url", false, func(_ context.Context, h string) ([]net.IPAddr, error) {
				mu.Lock()
				classified = append(classified, h)
				mu.Unlock()
				// Only the plain ASCII control name resolves; everything else fails,
				// which is what a resolver does with a name it cannot answer.
				if h == "storage.example.invalid" {
					return []net.IPAddr{{IP: net.ParseIP("203.0.113.9")}}, nil
				}
				return nil, &net.DNSError{Err: "no such host", Name: h, IsNotFound: true}
			})
			g.proxy = fixedProxy(t, proxy.URL)

			req, err := http.NewRequest(http.MethodGet, "https://"+host+"/obj", http.NoBody)
			if err != nil {
				t.Fatal(err)
			}
			resp, derr := transferClientWithGuard(g).Do(req)
			if derr == nil {
				_ = resp.Body.Close()
			}

			mu.Lock()
			seenClassified := append([]string(nil), classified...)
			mu.Unlock()

			for _, authority := range asked() {
				dialledHost, _, splitErr := net.SplitHostPort(authority)
				if splitErr != nil {
					dialledHost = authority
				}
				var agreed bool
				for _, c := range seenClassified {
					if c == dialledHost {
						agreed = true
					}
				}
				if !agreed {
					t.Errorf("the transport asked to reach %q, which the guard never classified "+
						"(it classified %v).\nThe two are reading different versions of the same host: "+
						"net/http canonicalises it with idnaASCII and this guard does not, so every rule "+
						"here judged a string that is not the one connected to.",
						dialledHost, seenClassified)
				}
			}
		})
	}
}

// TestTransferTarget_RejectsANonCanonicalHost is the boundary half. It belongs at
// assertSafeTransferURL because that is where a presigned URL enters, so one check
// covers the initial URL and every redirect hop rather than four separate rules.
func TestTransferTarget_RejectsANonCanonicalHost(t *testing.T) {
	refused := []string{fullwidthDottedIP, fullwidthDigitIP, fullwidthLocalimp,
		"storage．example．invalid", // a plain name, non-ASCII: still refused
	}
	for _, host := range refused {
		t.Run("refused/"+url.QueryEscape(host), func(t *testing.T) {
			_, err := assertSafeTransferURL("download_url", "https://"+host+"/obj", false)
			if err == nil {
				t.Fatalf("%q is not the host that would be dialled, so it must be refused", host)
			}
			if err.Code != "UNSAFE_PRESIGNED_URL" {
				t.Errorf("code = %q, want UNSAFE_PRESIGNED_URL", err.Code)
			}
		})
	}

	allowed := []string{
		"storage.example.invalid",
		"xn--nxasmq6b.example", // the A-label form of an IDN is ASCII and stays supported
		"203.0.113.9",
		"storage-internal.example.invalid",
	}
	for _, host := range allowed {
		t.Run("allowed/"+host, func(t *testing.T) {
			if _, err := assertSafeTransferURL("download_url", "https://"+host+"/obj", false); err != nil {
				t.Errorf("%q is a canonical ASCII host and must be accepted: %v", host, err)
			}
		})
	}
}

// --- Round-13 P2-4: the numeric-representation dimension ---
//
// assertNumericHostIsAnIP refused a host made of "digits and dots" that net.ParseIP
// rejects. That closed the zero-padded notation and nothing else, because it decided
// "is this numeric" by looking for decimal digits — so 0x7f.0.0.1 contains an "x", was
// classified as an ordinary name, and passed. The resolver disagrees: inet_aton-style
// parsing reads each label with strtoul, which accepts decimal, 0-prefixed octal and
// 0x-prefixed hex, and accepts fewer than four labels by packing the last one.
//
// Round 12 closed the *spelling* dimension (IDNA) by making one representation the only
// one accepted. This is the same shape one dimension over, so it is closed the same way:
// a host that net.ParseIP does not accept must not be interpretable as a number at all.
// The rule mirrors strtoul's bases rather than enumerating notations, which is what stops
// the next base from being a fourteenth round.
func TestTransferTarget_RefusesEveryNumericSpellingParseIPRejects(t *testing.T) {
	refused := []string{
		"127.000.000.001", // zero-padded — the only one the old rule caught
		"0177.0.0.1",      // octal
		"0x7f.0.0.1",      // hex label
		"0x7f000001",      // single hex word
		"2130706433",      // single decimal word
		"127.1",           // two labels, packed
		"0x7f.1",          // mixed hex and decimal
		"017700000001",    // single octal word
		"127.0.0.01",      // one padded label
	}
	for _, host := range refused {
		t.Run("refused/"+host, func(t *testing.T) {
			_, err := assertSafeTransferURL("download_url", "https://"+host+"/obj", false)
			if err == nil {
				t.Fatalf("%q is a numeric spelling net.ParseIP rejects, so the guard cannot classify it "+
					"while the resolver still reads it as an address", host)
			}
			if err.Code != "UNSAFE_PRESIGNED_URL" {
				t.Errorf("code = %q, want UNSAFE_PRESIGNED_URL", err.Code)
			}
		})
	}

	// The allow direction, which is what keeps this from being "refuse everything".
	// Note storage.de and cafe.f: labels made only of hex *letters* are ordinary names,
	// because a number needs a 0x prefix or a leading digit — a rule keyed on "contains
	// hex characters" would have broken these.
	allowed := []string{
		"127.0.0.1",               // a valid IP is still a valid IP
		"203.0.113.9",             //
		"::1",                     // IPv6 literal
		"storage.example.invalid", //
		"storage.de",              // all-hex-letter TLD
		"cafe.f",                  // all-hex-letter labels
		"1storage.example",        // starts with a digit, not a number
		"123.example",             // a numeric label next to a name
	}
	for _, host := range allowed {
		t.Run("allowed/"+host, func(t *testing.T) {
			bracketed := host
			if strings.Contains(host, ":") {
				bracketed = "[" + host + "]"
			}
			_, err := assertSafeTransferURL("download_url", "https://"+bracketed+"/obj", true)
			if err != nil && err.Code == "UNSAFE_PRESIGNED_URL" &&
				strings.Contains(err.Message, "numeric host") {
				t.Errorf("%q is not a number and must be treated as a name: %v", host, err)
			}
		})
	}
}
