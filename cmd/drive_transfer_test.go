package cmd

import (
	"net/http"
	"net/http/httptest"
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
// https half of the same steering concern. The initial presigned URL comes from
// the trusted backend and may legitimately name an internal host, but a hop the
// storage host chooses may not point at the caller's own machine.
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
