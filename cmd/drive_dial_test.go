package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Round-9 P1. The redirect guard classified hosts by *spelling*: isLoopbackHost
// special-cases "localhost" / "*.localhost" and otherwise asks net.ParseIP, so any
// name that is not an IP literal returned false without resolution — and
// assertNumericHostIsAnIP deliberately allowed anything containing a non-digit,
// specifically to keep DNS off the validation path. The guard therefore never
// learned where a name pointed, while cmd/drive.go's docstring states the invariant
// unconditionally ("may not land on a loopback host at all under a remote origin,
// whatever its scheme").
//
// The attack needs no local certificate: register a domain, point its A record at
// 127.0.0.1, obtain an ordinary publicly-trusted certificate for it (the domain is
// legitimately yours), and TLS validates while the connection lands on the caller's
// machine. `download file` writes the fetched bytes to disk, so the primitive is
// request forgery with the response delivered back — aimed at loopback-bound
// services that are unauthenticated *because* they are loopback-bound.
//
// These tests never consult public DNS. Names like 127.0.0.1.nip.io and
// localtest.me are commonly cited for this and are unusable as fixtures: they point
// at loopback only where nothing intercepts them, and a resolver that rewrites the
// answer turns every case below into a silent no-op that still reports PASS. Every
// case here goes through an injected resolver, and the hostnames use the RFC 6761
// .invalid TLD so that if the injection were ever bypassed the lookup would fail
// rather than reach the network.

// stubResolver maps names to addresses deterministically.
func stubResolver(t *testing.T, table map[string][]string) hostResolver {
	t.Helper()
	return stubIPAddrResolver(t, table)
}

// newTestTransferDialer builds the dialer under test with both seams stubbed: the
// resolver answers from table, and the dial step records the address it was handed
// instead of opening a socket, so nothing in this file touches the network.
func newTestTransferDialer(t *testing.T, loopbackAPI bool, table map[string][]string) *transferGuard {
	t.Helper()
	if table == nil {
		table = map[string][]string{}
	}
	g := newTransferGuard("download_url", loopbackAPI, stubResolver(t, table))
	g.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		return nil, fmt.Errorf("stub dial reached %s", addr)
	}
	return g
}

// attemptTransfer drives the guard in the order the transport does: Proxy first
// (which is where the target is classified), then DialContext for the address the
// transport would open. Tests below assert on the whole sequence rather than on
// DialContext alone, because "which hook refuses" is an implementation detail and
// calling only the dialer would miss a target that the selector already rejected.
func attemptTransfer(t *testing.T, g *transferGuard, rawURL string) (dialled []string, err error) {
	t.Helper()
	req, rerr := http.NewRequest(http.MethodGet, rawURL, http.NoBody)
	if rerr != nil {
		t.Fatal(rerr)
	}
	inner := g.dial
	g.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		return inner(ctx, network, addr)
	}
	proxyURL, perr := g.Proxy(req)
	if perr != nil {
		return dialled, perr
	}
	target := req.URL.Host
	if proxyURL != nil {
		target = proxyURL.Host
	}
	if _, _, serr := net.SplitHostPort(target); serr != nil {
		target = net.JoinHostPort(req.URL.Hostname(), "443")
	}
	_, derr := g.DialContext(context.Background(), "tcp", target)
	return dialled, derr
}

// TestTransferDial_RefusesANameThatResolvesToLoopback is the P1 assertion.
//
// Each host below is spelled so that no string rule could classify it — they are
// ordinary DNS names — and resolves to the local machine only through the injected
// resolver. The guard must refuse on the resolved address.
func TestTransferDial_RefusesANameThatResolvesToLoopback(t *testing.T) {
	table := map[string][]string{
		"storage-a.example.invalid": {"127.0.0.1"},
		"storage-b.example.invalid": {"127.9.9.9"},
		"storage-c.example.invalid": {"::1"},
		"storage-d.example.invalid": {"0.0.0.0"},
		"storage-e.example.invalid": {"::"},
		// A name that resolves to several addresses, only one of which is local:
		// refusing requires checking every answer, not just the first.
		"storage-f.example.invalid": {"203.0.113.10", "127.0.0.1"},
		// Legitimate remote storage, including a private-range internal host,
		// which the design deliberately allows.
		"storage-ok.example.invalid":       {"203.0.113.9"},
		"storage-internal.example.invalid": {"192.168.10.4"},
	}

	refused := []string{
		"storage-a.example.invalid",
		"storage-b.example.invalid",
		"storage-c.example.invalid",
		"storage-d.example.invalid",
		"storage-e.example.invalid",
		"storage-f.example.invalid",
	}
	allowed := []string{
		"storage-ok.example.invalid",
		"storage-internal.example.invalid",
	}

	for _, host := range refused {
		t.Run("refused/"+host, func(t *testing.T) {
			d := newTestTransferDialer(t, false, table)
			dialled, err := attemptTransfer(t, d, "https://"+host+"/obj")
			if err == nil {
				t.Fatalf("%s resolves to the local machine and must be refused", host)
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
				t.Errorf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
			}
			// Nothing may have been dialled: refusing after connecting would still
			// return an error, so "an error came back" is not the property.
			if len(dialled) > 0 {
				t.Errorf("must refuse before connecting, but dialled %v", dialled)
			}
		})
	}
	for _, host := range allowed {
		t.Run("allowed/"+host, func(t *testing.T) {
			d := newTestTransferDialer(t, false, table)
			dialled, err := attemptTransfer(t, d, "https://"+host+"/obj")
			if ee := output.AsExitError(err); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
				t.Errorf("%s is remote storage and must be dialled, got %v", host, err)
			}
			if len(dialled) == 0 {
				t.Errorf("%s is remote storage and should have been dialled", host)
			}
		})
	}
}

// TestTransferDial_LocalOriginStillReachesLoopback is the mirror: under a loopback
// Octo origin, local object storage is the normal development setup and must work.
func TestTransferDial_LocalOriginStillReachesLoopback(t *testing.T) {
	table := map[string][]string{"storage-a.example.invalid": {"127.0.0.1"}}
	d := newTestTransferDialer(t, true, table)
	dialled, err := attemptTransfer(t, d, "https://storage-a.example.invalid/obj")
	if ee := output.AsExitError(err); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
		t.Errorf("a loopback origin must still reach loopback storage: %v", err)
	}
	if len(dialled) == 0 {
		t.Error("a loopback origin must still reach loopback storage, but nothing was dialled")
	}
}

// TestTransferDial_DialsTheAddressItValidated pins the rebinding property: the
// connection must go to an address the guard checked, not to a fresh lookup the
// dialer performs afterwards. Otherwise a resolver answering differently the second
// time defeats the check.
func TestTransferDial_DialsTheAddressItValidated(t *testing.T) {
	const host = "storage-ok.example.invalid"
	table := map[string][]string{host: {"203.0.113.9"}}

	d := newTestTransferDialer(t, false, table)
	dialled, _ := attemptTransfer(t, d, "https://"+host+"/obj")

	if len(dialled) == 0 {
		t.Fatal("nothing was dialled")
	}
	for _, addr := range dialled {
		h, _, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("dialled a malformed address %q: %v", addr, err)
		}
		if net.ParseIP(h) == nil {
			t.Errorf("dialled %q by name, so the address dialled is not the address validated — "+
				"a second lookup can answer differently (DNS rebinding)", addr)
		}
	}
}

// TestTransferDial_UnresolvableNameFailsClosed keeps the failure direction right:
// a name the resolver cannot answer must not be dialled by name as a fallback.
//
// The assertion is "nothing was dialled", not "an error came back". Those are not the
// same, and the difference is not academic: the first version of this test checked
// only err != nil, and stayed green under a mutation that handed the unresolved name
// straight to the dialer — because the dial then failed on its own and produced an
// error that looked like the one being asserted.
func TestTransferDial_UnresolvableNameFailsClosed(t *testing.T) {
	d := newTestTransferDialer(t, false, map[string][]string{})
	dialled, err := attemptTransfer(t, d, "https://nothing-here.example.invalid/obj")
	if err == nil {
		t.Error("an unresolvable host must fail rather than be dialled by name")
	}
	if len(dialled) > 0 {
		t.Errorf("the name could not be resolved, so nothing should have been dialled; dialled %v.\n"+
			"Handing an unresolved name onward means the connection is made against a lookup "+
			"the loopback check never saw.", dialled)
	}
}

// TestTransferClient_GuardIsWiredIntoTheTransport is the wiring half: the checks
// above prove the dialer decides correctly, this proves the dialer is the one the
// transfer client actually uses. It drives a real *http.Client — real transport, real
// redirect policy — against a name the injected resolver puts on loopback, so the
// request must fail before any connection.
//
// This replaces an earlier version that swapped a package-level resolver variable.
// That variable was mutable global state, which this repo's own conventions forbid;
// passing the resolver into transferClient keeps the seam without it.
func TestTransferClient_GuardIsWiredIntoTheTransport(t *testing.T) {
	const host = "storage-evil.example.invalid"
	resolve := stubResolver(t, map[string][]string{
		host:                         {"127.0.0.1"},
		"storage-ok.example.invalid": {"203.0.113.9"},
	})

	t.Run("a name on loopback is refused under a remote origin", func(t *testing.T) {
		c := transferClient("url", false, resolve, nil)
		req, err := http.NewRequest(http.MethodGet, "https://"+host+"/obj", http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		resp, derr := c.Do(req)
		if derr == nil {
			_ = resp.Body.Close()
			t.Fatal("the transfer client connected to a name that resolves to the local machine")
		}
		if !strings.Contains(derr.Error(), "resolves to the local machine") {
			t.Errorf("error should come from the dialer guard, got %v", derr)
		}
	})

	t.Run("the guard is present at all", func(t *testing.T) {
		c := transferClient("url", false, resolve, nil)
		tr, ok := c.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("transport is %T, want *http.Transport carrying the dial guard", c.Transport)
		}
		if tr.DialContext == nil {
			t.Error("the transfer client has no DialContext, so nothing checks the resolved address")
		}
	})
}

// Round-9 P2-2. assertPartFileUnchanged does an Lstat and returns; publishDownload
// then calls os.Link / os.Rename on the same path, so a swap that wins that
// one-syscall window still publishes an attacker's symlink — under a success
// envelope whose sha256 describes different bytes.
//
// Closing the window itself needs linkat on the retained descriptor via
// /proc/self/fd/N, which is Linux-only. What is portable is refusing to *report*
// success: after publishing, the file now at the destination is compared with the
// descriptor's own identity, so a swap that won the race becomes an error instead of
// a false success — which converts the harm (an agent reading back "its" file and
// getting the attacker's) into a failed command.
//
// This drives assertPublishedFileMatches directly. Going through publishDownload
// would not isolate it: the pre-publication Lstat catches a swap made before the
// call, so such a test passes with the post-publication check deleted — the same way
// last round's first P1-4 tests passed against a reverted fchmod.
func TestAssertPublishedFileMatches_RejectsAFileWeDidNotWrite(t *testing.T) {
	dir := t.TempDir()
	created := mustCreatePartInfo(t, dir, "downloaded")

	t.Run("a symlink at the destination", func(t *testing.T) {
		victimDir := t.TempDir()
		victim := filepath.Join(victimDir, "victim.txt")
		if err := os.WriteFile(victim, []byte("attacker-chosen"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "out.bin")
		if err := os.Symlink(victim, target); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := assertPublishedFileMatches(target, created); err == nil {
			t.Error("a symlink at the destination must not be reported as a successful publication")
		}
	})

	t.Run("a different regular file at the destination", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "out.bin")
		if err := os.WriteFile(target, []byte("someone else"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := assertPublishedFileMatches(target, created); err == nil {
			t.Error("a destination that is not the file we wrote must not be reported as success")
		}
	})

	t.Run("the file we actually published", func(t *testing.T) {
		dir2 := t.TempDir()
		target := filepath.Join(dir2, "out.bin")
		part, info := mustCreatePartFor(t, dir2, "downloaded", "")
		if err := os.Rename(part, target); err != nil {
			t.Fatal(err)
		}
		if err := assertPublishedFileMatches(target, info); err != nil {
			t.Errorf("the file we published must verify: %v", err)
		}
	})
}

// mustCreatePartInfo returns the identity of a part file that is then removed, so
// it can stand for "the file we wrote" without existing anywhere the test looks.
func mustCreatePartInfo(t *testing.T, dir, contents string) os.FileInfo {
	t.Helper()
	part, info := mustCreatePartFor(t, dir, contents, "")
	_ = os.Remove(part)
	return info
}

// Round-10 P2-3. assertPublishedFileMatches was well tested on its own but its two
// call sites were not pinned: replacing both with `return nil` left the whole cmd
// package green, so a refactor could drop the check the previous round added and no
// test would notice. A function whose behaviour is asserted and whose wiring is not is
// only half covered — and the half that ships is the wiring.
//
// Reaching it through publishDownload needs a seam, because the swap has to happen
// after the pre-publication Lstat and before the link. beforePublish is that seam:
// nil in production, and the test uses it to replace the part file between the two
// steps — exactly the window the check exists to close.
func TestPublishDownload_PinsThePublicationIdentityCheck(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "obj")
	partPath, created := mustCreatePartFor(t, dir, "the bytes we actually downloaded", "")

	err := publishDownload(partPath, target, false, created, func() {
		// Something replaces the part file after it was checked. os.Link acts on
		// whatever the name resolves to now, so without the post-publication
		// comparison the destination holds a file the CLI never wrote while the
		// success envelope describes the one it did.
		//
		// The substitute is renamed in from elsewhere rather than written at the
		// same path, so it is a genuinely different inode on every filesystem.
		// remove-then-create is not portable here: Linux hands the freed inode
		// straight back, which made this case depend on the platform.
		substitute := filepath.Join(dir, "substitute")
		if wErr := os.WriteFile(substitute, []byte("substituted"), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		if rErr := os.Rename(substitute, partPath); rErr != nil {
			t.Fatal(rErr)
		}
	})

	if err == nil {
		t.Fatal("the part file was replaced between the check and the publication, which must not be reported as success")
	}
	if err.Code != "PARTIAL_FILE_REPLACED" {
		t.Errorf("code = %q, want PARTIAL_FILE_REPLACED (got %v)", err.Code, err)
	}
}

// TestPublishDownload_HappyPathStillPublishes is the allow direction, so the seam
// above cannot be satisfied by refusing everything.
func TestPublishDownload_HappyPathStillPublishes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "obj")
	partPath, created := mustCreatePartFor(t, dir, "payload", "")

	if err := publishDownload(partPath, target, false, created, nil); err != nil {
		t.Fatalf("an untouched part file must publish: %v", err)
	}
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(got) != "payload" {
		t.Errorf("target contents = %q, want %q", got, "payload")
	}
}

// Round-10 P2-5. Resolving first and dialing addresses ourselves is what makes the
// check and the connection agree, but it gives up the family race net.Dialer performs
// when handed a name (Happy Eyeballs, RFC 6555). A black-holed address ahead of a
// working one would then consume the whole dial budget before the working one is
// tried, so each attempt is capped when there is something to fall back to.
//
// The assertion is on the deadline the attempt carries, not on elapsed time: a timing
// test for a 10-second cap either takes 10 seconds or proves nothing, and "it finished
// quickly" would pass just as well with no cap at all.
func TestTransferDial_CapsEachAttemptOnlyWhenThereIsAFallback(t *testing.T) {
	table := map[string][]string{
		"two.example.invalid": {"203.0.113.10", "203.0.113.11"},
		"one.example.invalid": {"203.0.113.12"},
	}

	// The rule is per attempt, not per name: an attempt is capped while failing it
	// still leaves an address to try. An earlier version of this test asserted that
	// every attempt of a multi-address name carried a deadline, which encoded the
	// off-by-one it was meant to catch — the final attempt has no fallback.
	for _, tc := range []struct {
		host          string
		wantDeadlines []bool
	}{
		{"two.example.invalid", []bool{true, false}},
		{"one.example.invalid", []bool{false}},
	} {
		t.Run(tc.host, func(t *testing.T) {
			var deadlines []bool
			d := newTestTransferDialer(t, false, table)
			d.dial = func(ctx context.Context, _, addr string) (net.Conn, error) {
				_, ok := ctx.Deadline()
				deadlines = append(deadlines, ok)
				return nil, errors.New("stub dial reached " + addr)
			}
			if _, err := attemptTransfer(t, d, "https://"+tc.host+"/obj"); err == nil {
				t.Fatal("the stub dial always fails, so an error is expected")
			}
			if len(deadlines) != len(tc.wantDeadlines) {
				t.Fatalf("attempted %d addresses, want %d", len(deadlines), len(tc.wantDeadlines))
			}
			for i, want := range tc.wantDeadlines {
				if deadlines[i] == want {
					continue
				}
				if want {
					t.Errorf("attempt %d has a fallback after it, so a stall must not hold the whole budget", i)
				} else {
					t.Errorf("attempt %d is the last one; capping it can only turn a slow but working "+
						"connection into a failure", i)
				}
			}
		})
	}
}

// TestTransferDial_TriesEveryAddressBeforeGivingUp is the other half: capping an
// attempt is only useful if the next address is actually tried.
func TestTransferDial_TriesEveryAddressBeforeGivingUp(t *testing.T) {
	table := map[string][]string{"two.example.invalid": {"203.0.113.10", "203.0.113.11"}}
	d := newTestTransferDialer(t, false, table)
	dialled, _ := attemptTransfer(t, d, "https://two.example.invalid/obj")
	if len(dialled) != 2 {
		t.Errorf("dialled %v, want both addresses attempted", dialled)
	}
}

// Round-10 P2-6. The resolver used to keep only net.IPAddr.IP and drop Zone, so a
// scoped link-local target lost its interface identifier — and fe80::1 without a zone
// is not the same destination as fe80::1%eth0, it is an undiallable one. Not a
// realistic object-storage host, but the loss was silent, and the guard now dials the
// address it resolved rather than the name, which is exactly where the zone has to
// survive to matter.
func TestTransferDial_KeepsTheIPv6Zone(t *testing.T) {
	const host = "scoped.example.invalid"
	d := newTestTransferDialer(t, false, nil)
	d.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("2001:db8::1"), Zone: "eth0"}}, nil
	}

	dialled, _ := attemptTransfer(t, d, "https://"+host+"/obj")
	if len(dialled) == 0 {
		t.Fatal("nothing was dialled")
	}
	if !strings.Contains(dialled[0], "%eth0") {
		t.Errorf("dialled %q, which has lost the zone — a scoped address without its "+
			"interface identifier names a different (undiallable) destination", dialled[0])
	}
}

// TestAssertPublishedFileMatches_RejectsAnInPlaceRewrite is the case CI found and
// macOS hid. os.SameFile compares device and inode, and a filesystem may hand a
// freed inode straight to the next file created in that directory — Linux does — so
// a replacement can be byte-for-byte a different file while SameFile calls it
// identical. Rewriting in place is the same condition made deterministic: the inode
// is unchanged by construction, so only the size and mtime comparison can refuse it.
//
// Without that comparison this test passes on every platform and the check means
// nothing on the one CI runs.
func TestAssertPublishedFileMatches_RejectsAnInPlaceRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "obj")
	if err := os.WriteFile(path, []byte("the bytes we wrote"), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Same path, same inode, different contents and length.
	if werr := os.WriteFile(path, []byte("different bytes entirely, and longer"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if now, serr := os.Lstat(path); serr != nil {
		t.Fatal(serr)
	} else if !os.SameFile(now, created) {
		t.Skip("this filesystem changed the identity on an in-place rewrite, so the condition under test does not arise here")
	}

	if ee := assertPublishedFileMatches(path, created); ee == nil {
		t.Error("the file at the destination is not the one that was written, and os.SameFile cannot tell — " +
			"size and modification time are what distinguish them")
	} else if ee.Code != "PARTIAL_FILE_REPLACED" {
		t.Errorf("code = %q, want PARTIAL_FILE_REPLACED", ee.Code)
	}
}

// TestTransferDial_TheLastAttemptKeepsTheFullBudget is round-11 suggestion 4. The rule
// is "cap an attempt only while failing it still leaves somewhere to go", and passing
// the total address count instead of the number remaining capped the final attempt as
// well — the one with no fallback, which is exactly the case the exemption exists for.
func TestTransferDial_TheLastAttemptKeepsTheFullBudget(t *testing.T) {
	table := map[string][]string{"three.example.invalid": {"203.0.113.10", "203.0.113.11", "203.0.113.12"}}
	var deadlines []bool
	d := newTestTransferDialer(t, false, table)
	d.dial = func(ctx context.Context, _, addr string) (net.Conn, error) {
		_, ok := ctx.Deadline()
		deadlines = append(deadlines, ok)
		return nil, errors.New("stub dial reached " + addr)
	}

	if _, err := attemptTransfer(t, d, "https://three.example.invalid/obj"); err == nil {
		t.Fatal("the stub dial always fails, so an error is expected")
	}
	if len(deadlines) != 3 {
		t.Fatalf("attempted %d addresses, want 3", len(deadlines))
	}
	for i, got := range deadlines[:2] {
		if !got {
			t.Errorf("attempt %d has a fallback after it and should be capped", i)
		}
	}
	if deadlines[2] {
		t.Error("the last attempt has nothing to fall back to, so capping it can only turn a slow " +
			"but working connection into a failure")
	}
}
