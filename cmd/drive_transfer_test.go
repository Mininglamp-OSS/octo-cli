package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Round-2 review findings on the object-storage transfer. The first round closed
// the presigned URL's route into our own error envelope; these cover the two ways
// it still reached a third party, and the two ways a redirect could steer the
// transfer somewhere the caller did not intend.

// TestDriveTransfer_RedirectDoesNotForwardTheRefererHeader asserts the presigned
// URL is not handed to the redirect target.
//
// Go fills in Referer from the previous request's full URL, and a presigned URL
// carries its signature in the query string — read access for a GET, write access
// for a PUT. The redirect target addresses its own URL and never presents the
// original signature, so the value is surplus data landing in a third party's
// access log. Redirect following is expected traffic here (storage gateways use
// it), not an edge case.
func TestDriveTransfer_RedirectDoesNotForwardTheRefererHeader(t *testing.T) {
	const signature = "SUPERSECRETSIGNATURE123"

	var secondHop http.Header
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHop = r.Header.Clone()
		_, _ = w.Write([]byte("redirected-bytes"))
	}))
	t.Cleanup(final.Close)

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj?X-Amz-Signature=` + signature + `"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/real-obj", http.StatusFound)
	})

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := env.run("drive", "download", "file", "1", "-o", dest); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if ref := secondHop.Get("Referer"); ref != "" {
		t.Errorf("the redirect target received Referer %q; the presigned signature must not be forwarded", ref)
	}
	// The credential isolation this client exists for must still hold.
	if got := secondHop.Get("Authorization"); got != "" {
		t.Errorf("the redirect target received Authorization %q", got)
	}
	if got := secondHop.Get("X-Space-Id"); got != "" {
		t.Errorf("the redirect target received X-Space-Id %q", got)
	}
	for _, values := range secondHop {
		for _, v := range values {
			if strings.Contains(v, signature) {
				t.Errorf("a forwarded header carried the presigned signature: %q", v)
			}
		}
	}
}

// TestDriveTransfer_LoopbackHTTPRequiresALocalOrigin asserts the plain-http
// exception is gated on the configured Octo origin being loopback.
//
// The exception exists so local development works, and it is also the redirect
// rule — so against a remote origin a cooperating or compromised storage host
// could answer 302 http://127.0.0.1:<port>/… and have the CLI issue the transfer
// against whatever local service listens there, writing the response into the
// caller's --output path.
func TestDriveTransfer_LoopbackHTTPRequiresALocalOrigin(t *testing.T) {
	cases := []struct {
		name      string
		apiOrigin string
		wantErr   bool
	}{
		{"local origin accepts loopback storage", "", false},
		{"remote origin refuses loopback storage", "https://octo.example.invalid", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var env *driveTestEnv
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				// A loopback plain-http presigned URL, which is what a local
				// object store hands out.
				_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
			}, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("bytes"))
			})
			if tc.apiOrigin != "" {
				// Keep the API reachable (the request still has to get out) while
				// the *configured* origin is remote, which is the situation the
				// gate is about.
				retargetAPIOrigin(t, env, tc.apiOrigin)
			}

			dest := filepath.Join(t.TempDir(), "out.bin")
			err := env.run("drive", "download", "file", "1", "-o", dest)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected the loopback presigned URL to be refused")
				}
				if ee := output.AsExitError(err); ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
					t.Errorf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
				}
				if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
					t.Error("nothing may be written when the presigned URL is refused")
				}
				return
			}
			if err != nil {
				t.Fatalf("a local origin must still accept loopback storage: %v", err)
			}
		})
	}
}

// TestDriveTransfer_MalformedPresignedURLIsNotEchoed covers the one transfer path
// where the signature still reached the envelope: url.Parse returns a *url.Error
// whose text quotes the entire input.
func TestDriveTransfer_MalformedPresignedURLIsNotEchoed(t *testing.T) {
	const signature = "SUPERSECRETSIGNATURE123"
	// A control character in the host makes url.Parse fail outright.
	malformed := "http://exa\x7fmple.com/obj?X-Amz-Signature=" + signature

	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + malformed + `"}`))
	}, nil)

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := env.run("drive", "download", "file", "1", "-o", dest)
	if err == nil {
		t.Fatal("expected a malformed presigned URL to be refused")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
		t.Fatalf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
	}
	if strings.Contains(ee.Message, signature) {
		t.Errorf("the parse failure echoed the presigned signature: %s", ee.Message)
	}
	if streams := env.tf.Out.String() + env.tf.ErrOut.String(); strings.Contains(streams, signature) {
		t.Errorf("the emitted output echoed the presigned signature: %s", streams)
	}
}

// TestDriveUpload_SendsTheStatdDescriptorNotThePath asserts the bytes uploaded
// are the ones whose size was signed.
//
// The size is fixed by a stat before the prepare round-trip and the PUT happens
// after it, so in a directory another process can write, re-opening by path would
// send a replacement file's bytes under the old file's signed Content-Length. The
// fix is to keep the descriptor; this test replaces the path between prepare and
// PUT and asserts the original contents still go out.
func TestDriveUpload_SendsTheStatdDescriptorNotThePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.txt")
	const original = "original-bytes"
	if err := os.WriteFile(src, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			// Swap the file at the path after the size has been signed. The
			// replacement is a different length, so a path re-open would also
			// mismatch the signed Content-Length.
			if err := os.Remove(src); err != nil {
				t.Errorf("remove: %v", err)
			}
			if err := os.WriteFile(src, []byte("ATTACKER-SUPPLIED-REPLACEMENT-BYTES"), 0o600); err != nil {
				t.Errorf("replace: %v", err)
			}
			_, _ = w.Write([]byte(`{"file_id":7,"upload_url":"` + env.store.URL + `/obj"}`))
		case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
			_, _ = w.Write([]byte(`{"id":7,"parent_id":0,"name":"a.txt"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}, nil)

	if err := env.run("drive", "upload", "file", src, "--space-id", "s1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(env.storeBodies) != 1 {
		t.Fatalf("expected exactly one object PUT, got %d", len(env.storeBodies))
	}
	if env.storeBodies[0] != original {
		t.Errorf("uploaded bytes: got %q, want the contents that were stat'd (%q)",
			env.storeBodies[0], original)
	}
}

// retargetAPIOrigin points the Factory's configured API base URL at apiOrigin
// while leaving the client dialling the live test server, so a test can exercise
// a decision that reads the *configured* origin without needing that origin to
// exist.
func retargetAPIOrigin(t *testing.T, env *driveTestEnv, apiOrigin string) {
	t.Helper()
	cfg := &config.Config{APIBaseURL: apiOrigin, BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", Source: "test"}
	live := &config.Config{APIBaseURL: env.api.URL, BotToken: "bf_bot", Format: "json"}

	tf := cmdutil.NewTestFactory()
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(live, cred, client.Options{ErrOut: tf.ErrOut}))
	tf.RegistryFunc = registry.MustNew
	env.tf = tf
	env.root = NewRootCmd(tf.Factory)
}

// TestDriveUpload_MethodChangingRedirectFailsAndCancels is the upload half of
// redirect handling, and the one that could lose data silently.
//
// Go rewrites a PUT into a bodiless GET on 301/302/303. With redirects followed
// and no method guard, a storage host answering 2xx after such a hop made
// putObject return nil — so the composite went on to call confirm-upload with the
// local file size, producing a confirmed drive row pointing at an object that was
// never written, reported to the caller as ok:true with a byte count. 307/308
// preserve the method and fail on their own because an *os.File body cannot be
// replayed, so before the fix the safe status codes errored and the unsafe ones
// succeeded quietly.
//
// The upload must fail, and the pending row must be cancelled.
func TestDriveUpload_MethodChangingRedirectFailsAndCancels(t *testing.T) {
	for _, code := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "a.txt")
			if err := os.WriteFile(src, []byte("payload-bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			// The endpoint a redirect would land on. It must never receive the
			// transfer; if it does, it answers 2xx, which is exactly the silent
			// success being guarded against.
			var finalHits int
			final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				finalHits++
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(final.Close)

			var cancelled, confirmed bool
			var env *driveTestEnv
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
					_, _ = w.Write([]byte(`{"file_id":7,"upload_url":"` + env.store.URL + `/obj"}`))
				case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
					cancelled = true
					_, _ = w.Write([]byte(`{}`))
				case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
					confirmed = true
					_, _ = w.Write([]byte(`{"id":7,"parent_id":0,"name":"a.txt"}`))
				default:
					_, _ = w.Write([]byte(`{}`))
				}
			}, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, final.URL+"/real-obj", code)
			})

			err := env.run("drive", "upload", "file", src, "--space-id", "s1")
			if err == nil {
				t.Fatal("a redirected upload must fail rather than report success")
			}
			if confirmed {
				t.Error("confirm-upload must not run: no bytes reached object storage")
			}
			if !cancelled {
				t.Error("the pending row must be cancelled when the upload fails")
			}
			if finalHits != 0 {
				t.Errorf("the redirect target received %d request(s); the transfer must not follow an upload redirect", finalHits)
			}
			if out := env.tf.Out.String(); strings.Contains(out, `"ok":true`) {
				t.Errorf("a failed upload must not emit a success envelope: %s", out)
			}
		})
	}
}

// TestDriveTransfer_RedirectToLoopbackRefusedUnderARemoteOrigin covers the
// end-to-end path: a storage host that redirects at the caller's own machine gets
// nothing written.
//
// Its redirect target is a plain-http httptest server, so the pre-existing
// https-or-loopback-http rule already refuses it — the loopback-hop rule added for
// the https case is covered by TestTransferClient_LoopbackHopSpellingsAreAllRefused
// and the table in TestTransferClient_CheckRedirectPolicy, which drive the policy
// directly because an https loopback hop would need a TLS server whose certificate
// the transfer client rejects first.
//
// Deliberately not a private-range block: a self-hosted deployment can reasonably
// run object storage on an internal https host, and refusing those would break it.
func TestDriveTransfer_RedirectToLoopbackRefusedUnderARemoteOrigin(t *testing.T) {
	var finalHits int
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHits++
		_, _ = w.Write([]byte("should-not-be-read"))
	}))
	t.Cleanup(final.Close)

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/real-obj", http.StatusFound)
	})
	retargetAPIOrigin(t, env, "https://octo.example.invalid")

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := env.run("drive", "download", "file", "1", "-o", dest)
	if err == nil {
		t.Fatal("expected the loopback redirect to be refused under a remote origin")
	}
	if ee := output.AsExitError(err); ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
		t.Errorf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("nothing may be written when a redirect hop is refused")
	}
}

// TestTransferClient_CheckRedirectPolicy exercises the redirect policy directly.
//
// Two of its rules cannot be reached through a live httptest server: refusing a
// same-method redirect on a body-carrying request is shadowed by Go's own
// inability to replay an *os.File (which fails first, with a less useful error),
// and refusing an https hop onto a loopback host would need a TLS test server
// whose self-signed certificate the transfer client rejects earlier. Driving the
// closure is what gives those two rules a failing test rather than a comment.
func TestTransferClient_CheckRedirectPolicy(t *testing.T) {
	hop := func(method, rawURL string) *http.Request {
		req, err := http.NewRequest(method, rawURL, http.NoBody)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, rawURL, err)
		}
		return req
	}

	cases := []struct {
		name        string
		loopbackAPI bool
		next        *http.Request
		via         []*http.Request
		wantErr     bool
		wantCode    string
	}{
		{
			name:    "a GET redirected to a GET on a safe host is followed",
			next:    hop(http.MethodGet, "https://storage.example.com/real-obj"),
			via:     []*http.Request{hop(http.MethodGet, "https://storage.example.com/obj")},
			wantErr: false,
		},
		{
			name:     "a PUT rewritten into a GET is refused",
			next:     hop(http.MethodGet, "https://storage.example.com/real-obj"),
			via:      []*http.Request{hop(http.MethodPut, "https://storage.example.com/obj")},
			wantErr:  true,
			wantCode: "UNSAFE_PRESIGNED_URL",
		},
		{
			// 307/308 preserve the method, so the method-equality rule alone would
			// let this through and leave the failure to Go's rewind error.
			name:     "a PUT redirected as a PUT is refused",
			next:     hop(http.MethodPut, "https://storage.example.com/real-obj"),
			via:      []*http.Request{hop(http.MethodPut, "https://storage.example.com/obj")},
			wantErr:  true,
			wantCode: "UNSAFE_PRESIGNED_URL",
		},
		{
			name:     "an https hop onto loopback is refused under a remote origin",
			next:     hop(http.MethodGet, "https://127.0.0.1:9443/real-obj"),
			via:      []*http.Request{hop(http.MethodGet, "https://storage.example.com/obj")},
			wantErr:  true,
			wantCode: "UNSAFE_PRESIGNED_URL",
		},
		{
			name:        "an https hop onto loopback is allowed under a local origin",
			loopbackAPI: true,
			next:        hop(http.MethodGet, "https://127.0.0.1:9443/real-obj"),
			via:         []*http.Request{hop(http.MethodGet, "https://127.0.0.1:9443/obj")},
			wantErr:     false,
		},
		{
			name:     "a hop onto a non-loopback plain-http host is refused",
			next:     hop(http.MethodGet, "http://storage.example.com/real-obj"),
			via:      []*http.Request{hop(http.MethodGet, "https://storage.example.com/obj")},
			wantErr:  true,
			wantCode: "UNSAFE_PRESIGNED_URL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := transferClient("url", tc.loopbackAPI).CheckRedirect
			if policy == nil {
				t.Fatal("the transfer client must set a redirect policy")
			}
			err := policy(tc.next, tc.via)
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("hop should be followed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("hop should be refused")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Code != tc.wantCode {
				t.Errorf("error: got %v, want code %s", err, tc.wantCode)
			}
		})
	}

	t.Run("the hop cap is restated", func(t *testing.T) {
		policy := transferClient("url", false).CheckRedirect
		via := make([]*http.Request, maxTransferRedirects)
		for i := range via {
			via[i] = hop(http.MethodGet, "https://storage.example.com/obj")
		}
		if err := policy(hop(http.MethodGet, "https://storage.example.com/next"), via); err == nil {
			t.Errorf("a chain of %d hops must be stopped", maxTransferRedirects)
		}
	})

	t.Run("Referer is dropped before the hop is judged", func(t *testing.T) {
		policy := transferClient("url", false).CheckRedirect
		next := hop(http.MethodGet, "https://storage.example.com/real-obj")
		next.Header.Set("Referer", "https://storage.example.com/obj?X-Amz-Signature=SUPERSECRET")
		_ = policy(next, []*http.Request{hop(http.MethodGet, "https://storage.example.com/obj")})
		if got := next.Header.Get("Referer"); got != "" {
			t.Errorf("Referer survived the policy: %q", got)
		}
	})
}

// Round-4 review: the loopback rules were spelling-sensitive, and the two
// download paths in this CLI disagreed about the mode of the file they publish.

// TestIsLoopbackHost_SpellingVariants pins the normalisation. url.Parse does not
// lower-case a host and does not strip the root dot, while a resolver treats all
// of these alike — so before this, a hop onto "LOCALHOST" was followed under a
// remote origin, and a developer whose configured origin read "LOCALHOST" was
// refused plain-http object storage. The same helper gates both directions, so a
// missed spelling was simultaneously too permissive and too strict.
func TestIsLoopbackHost_SpellingVariants(t *testing.T) {
	loopback := []string{
		"localhost", "LOCALHOST", "LocalHost", "localhost.", "LOCALHOST.",
		"127.0.0.1", "127.1.2.3", "::1", "0:0:0:0:0:0:0:1",
		"api.localhost", "API.LOCALHOST.",
	}
	for _, host := range loopback {
		if !isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = false, want true", host)
		}
	}
	remote := []string{
		"storage.example.com", "STORAGE.EXAMPLE.COM", "192.168.0.9", "10.0.0.1",
		"8.8.8.8", "localhostx", "notlocalhost", "",
	}
	for _, host := range remote {
		if isLoopbackHost(host) {
			t.Errorf("isLoopbackHost(%q) = true, want false", host)
		}
	}
}

// TestAssertNumericHostIsAnIP covers the spelling net.ParseIP refuses but a
// resolver accepts. A zero-padded dotted quad would otherwise be treated as a
// name and slip past whichever loopback rule was being applied; resolving it here
// would put a DNS lookup on a validation path, so it is refused as malformed.
func TestAssertNumericHostIsAnIP(t *testing.T) {
	refused := []string{
		"http://127.000.000.001/obj",
		"http://127.0.0.01/obj",
		"https://1.2.3.4.5/obj",
		"https://999.999.999.999/obj",
		"https://0177.0.0.1/obj",
	}
	for _, raw := range refused {
		u, perr := url.Parse(raw)
		if perr != nil {
			t.Fatalf("parse %q: %v", raw, perr)
		}
		if err := assertNumericHostIsAnIP("url", u); err == nil {
			t.Errorf("%q: a numeric host that is not a valid IP must be refused", raw)
		}
	}
	allowed := []string{
		"https://127.0.0.1/obj", "https://192.168.0.9/obj", "https://storage.example.com/obj",
		"https://s3.eu-west-1.amazonaws.com/obj", "https://[::1]:9000/obj", "https://host2.example/obj",
	}
	for _, raw := range allowed {
		u, perr := url.Parse(raw)
		if perr != nil {
			t.Fatalf("parse %q: %v", raw, perr)
		}
		if err := assertNumericHostIsAnIP("url", u); err != nil {
			t.Errorf("%q: must be allowed, got %v", raw, err)
		}
	}
}

// TestTransferClient_LoopbackHopSpellingsAreAllRefused is the policy-level
// consequence: every spelling of the local machine is refused as a redirect target
// under a remote origin, not just the three the first version happened to catch.
func TestTransferClient_LoopbackHopSpellingsAreAllRefused(t *testing.T) {
	policy := transferClient("url", false).CheckRedirect
	previous, err := http.NewRequest(http.MethodGet, "https://storage.example.com/obj", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"https://127.0.0.1:9443/x", "https://localhost:9443/x", "https://[::1]:9443/x",
		"https://LOCALHOST:9443/x", "https://LocalHost:9443/x", "https://localhost.:9443/x",
		"https://127.000.000.001:9443/x",
	} {
		next, nerr := http.NewRequest(http.MethodGet, target, http.NoBody)
		if nerr != nil {
			t.Fatalf("build %q: %v", target, nerr)
		}
		if perr := policy(next, []*http.Request{previous}); perr == nil {
			t.Errorf("%s: a hop onto the local machine must be refused under a remote origin", target)
		}
	}
}

// TestDriveDownload_FileModeIsExplicit pins the publication mode. os.CreateTemp
// makes a 0600 file and nothing chmod'd it, so a fresh download landed at 0600
// while the binary --output path in internal/client produces 0644 — and
// --overwrite silently tightened a destination the caller may have deliberately
// made group-readable.
func TestDriveDownload_FileModeIsExplicit(t *testing.T) {
	t.Run("a fresh destination is private", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "out.bin")
		runOneDownload(t, dest, false)
		assertMode(t, dest, 0o600)
	})

	t.Run("overwrite keeps the existing mode", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "out.bin")
		if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dest, 0o644); err != nil {
			t.Fatal(err)
		}
		runOneDownload(t, dest, true)
		assertMode(t, dest, 0o644)
	})
}

// TestDriveDownload_NoOverwritePublishesAtomically asserts --overwrite=false is a
// guarantee rather than a narrowed window: the publication is a hard link, which
// fails with EEXIST atomically, so a file that appears between the pre-check and
// the publication is not clobbered.
func TestDriveDownload_NoOverwritePublishesAtomically(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	const existing = "must-survive"

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		// Create the destination after the pre-check has already passed, which is
		// the window a rename would have clobbered.
		if err := os.WriteFile(dest, []byte(existing), 0o600); err != nil {
			t.Errorf("plant: %v", err)
		}
		_, _ = w.Write([]byte("downloaded-bytes"))
	})

	err := env.run("drive", "download", "file", "1", "-o", dest)
	if err == nil {
		t.Fatal("a destination that appeared mid-transfer must not be replaced without --overwrite")
	}
	got, rerr := os.ReadFile(dest)
	if rerr != nil {
		t.Fatalf("read %q: %v", dest, rerr)
	}
	if string(got) != existing {
		t.Errorf("destination contents: got %q, want the file that was already there (%q)", got, existing)
	}
	assertNoPartialFiles(t, dest)
}

func runOneDownload(t *testing.T, dest string, overwrite bool) {
	t.Helper()
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded-bytes"))
	})
	args := []string{"drive", "download", "file", "1", "-o", dest}
	if overwrite {
		args = append(args, "--overwrite")
	}
	if err := env.run(args...); err != nil {
		t.Fatalf("execute: %v", err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %q: got %#o, want %#o", path, got, want)
	}
}

// TestDriveUpload_CancelSurvivesAnInterruptedContext asserts the best-effort
// cleanup actually runs on its most common trigger.
//
// withCancel used to send cancel-upload on the command context, which is
// cancelled by SIGINT/SIGTERM — so a caller interrupting an upload triggered the
// cleanup and, by the same act, killed the channel the cleanup needed. The pending
// row survived. The cleanup context is now detached from cancellation with its own
// short bound.
func TestDriveUpload_CancelSurvivesAnInterruptedContext(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("payload-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Cancelled as soon as the object PUT is under way, standing in for the signal
	// a caller sends mid-upload.
	ctx, cancel := context.WithCancel(context.Background())

	var cancelled bool
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/prepare-upload"):
			_, _ = w.Write([]byte(`{"file_id":7,"upload_url":"` + env.store.URL + `/obj"}`))
		case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
			cancelled = true
			_, _ = w.Write([]byte(`{}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		// Interrupt the run, then fail the transfer the way a cancelled request
		// would.
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	})

	env.root.SetArgs([]string{"drive", "upload", "file", src, "--space-id", "s1"})
	if err := env.root.ExecuteContext(ctx); err == nil {
		t.Fatal("the interrupted upload must fail")
	}
	if !cancelled {
		t.Error("cancel-upload must still reach the backend after the command context is cancelled")
	}
	if out := env.tf.ErrOut.String() + env.tf.Out.String(); strings.Contains(out, "cancel_failed") {
		t.Errorf("the cleanup reported failure on a cancelled context: %s", out)
	}
}

// TestPublishDownload_NoOverwriteIsAtomic drives the publication directly, which
// is the only way to cover the primitive: from a command-level test the file can
// only be planted before the pre-rename re-check, so that check catches it and the
// publication is never reached. Here the target already exists when publish runs,
// which is exactly the state the check-to-publish window leaves behind.
func TestPublishDownload_NoOverwriteIsAtomic(t *testing.T) {
	const existing = "must-survive"
	const fresh = "downloaded"

	t.Run("without overwrite an existing target is not replaced", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "out.bin")
		part := filepath.Join(dir, "out.bin.tmp.part")
		if err := os.WriteFile(target, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(part, []byte(fresh), 0o600); err != nil {
			t.Fatal(err)
		}

		err := publishDownload(part, target, false)
		if err == nil {
			t.Fatal("publishing over an existing target without --overwrite must fail")
		}
		if err.Code != "FILE_EXISTS" {
			t.Errorf("code: got %q, want FILE_EXISTS", err.Code)
		}
		got, rerr := os.ReadFile(target)
		if rerr != nil {
			t.Fatalf("read %q: %v", target, rerr)
		}
		if string(got) != existing {
			t.Errorf("target contents: got %q, want %q — the existing file was replaced", got, existing)
		}
	})

	t.Run("with overwrite the target is replaced", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "out.bin")
		part := filepath.Join(dir, "out.bin.tmp.part")
		if err := os.WriteFile(target, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(part, []byte(fresh), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := publishDownload(part, target, true); err != nil {
			t.Fatalf("publishing with --overwrite must succeed: %v", err)
		}
		got, rerr := os.ReadFile(target)
		if rerr != nil {
			t.Fatalf("read %q: %v", target, rerr)
		}
		if string(got) != fresh {
			t.Errorf("target contents: got %q, want the downloaded bytes %q", got, fresh)
		}
	})

	t.Run("a fresh target is published and the part file removed", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "out.bin")
		part := filepath.Join(dir, "out.bin.tmp.part")
		if err := os.WriteFile(part, []byte(fresh), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := publishDownload(part, target, false); err != nil {
			t.Fatalf("publishing to a fresh target must succeed: %v", err)
		}
		got, rerr := os.ReadFile(target)
		if rerr != nil {
			t.Fatalf("read %q: %v", target, rerr)
		}
		if string(got) != fresh {
			t.Errorf("target contents: got %q, want %q", got, fresh)
		}
		if _, serr := os.Stat(part); !os.IsNotExist(serr) {
			t.Error("the part file must not survive publication")
		}
	})
}
