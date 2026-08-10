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
func stubResolver(t *testing.T, table map[string][]string) func(context.Context, string) ([]net.IP, error) {
	t.Helper()
	return func(_ context.Context, host string) ([]net.IP, error) {
		addrs, ok := table[host]
		if !ok {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
		ips := make([]net.IP, 0, len(addrs))
		for _, a := range addrs {
			ip := net.ParseIP(a)
			if ip == nil {
				t.Fatalf("stub table has a bad address %q for %q", a, host)
			}
			ips = append(ips, ip)
		}
		return ips, nil
	}
}

// newTestTransferDialer builds the dialer under test with both seams stubbed: the
// resolver answers from table, and the dial step records the address it was handed
// instead of opening a socket, so nothing in this file touches the network.
func newTestTransferDialer(t *testing.T, loopbackAPI bool, table map[string][]string) transferDialer {
	t.Helper()
	return transferDialer{
		field:       "download_url",
		loopbackAPI: loopbackAPI,
		resolve:     stubResolver(t, table),
		dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			return nil, fmt.Errorf("stub dial reached %s", addr)
		},
	}
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
			_, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, "443"))
			if err == nil {
				t.Fatalf("%s resolves to the local machine and must be refused", host)
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
				t.Errorf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
			}
			// The stub dial marker must be absent: refusing after connecting would
			// still return an error, so "an error came back" is not the property.
			if strings.Contains(err.Error(), "stub dial reached") {
				t.Error("the dialer must refuse before connecting, but it dialled first")
			}
		})
	}
	for _, host := range allowed {
		t.Run("allowed/"+host, func(t *testing.T) {
			d := newTestTransferDialer(t, false, table)
			_, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, "443"))
			if err == nil {
				return // the stub dial succeeded, which is the pass condition
			}
			if ee := output.AsExitError(err); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
				t.Errorf("%s is remote storage and must be dialled, got %v", host, err)
			}
		})
	}
}

// TestTransferDial_LocalOriginStillReachesLoopback is the mirror: under a loopback
// Octo origin, local object storage is the normal development setup and must work.
func TestTransferDial_LocalOriginStillReachesLoopback(t *testing.T) {
	table := map[string][]string{"storage-a.example.invalid": {"127.0.0.1"}}
	d := newTestTransferDialer(t, true, table)
	if _, err := d.DialContext(context.Background(), "tcp", "storage-a.example.invalid:443"); err != nil {
		if ee := output.AsExitError(err); ee != nil && ee.Code == "UNSAFE_PRESIGNED_URL" {
			t.Errorf("a loopback origin must still reach loopback storage: %v", err)
		}
	}
}

// TestTransferDial_DialsTheAddressItValidated pins the rebinding property: the
// connection must go to an address the guard checked, not to a fresh lookup the
// dialer performs afterwards. Otherwise a resolver answering differently the second
// time defeats the check.
func TestTransferDial_DialsTheAddressItValidated(t *testing.T) {
	const host = "storage-ok.example.invalid"
	table := map[string][]string{host: {"203.0.113.9"}}

	var dialled []string
	d := newTestTransferDialer(t, false, table)
	d.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		return nil, errors.New("stub dial")
	}
	_, _ = d.DialContext(context.Background(), "tcp", net.JoinHostPort(host, "443"))

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
	var dialled []string
	d := newTestTransferDialer(t, false, map[string][]string{})
	d.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialled = append(dialled, addr)
		return nil, errors.New("stub dial")
	}

	_, err := d.DialContext(context.Background(), "tcp", "nothing-here.example.invalid:443")
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
		c := transferClient("url", false, resolve)
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
		c := transferClient("url", false, resolve)
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
