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
