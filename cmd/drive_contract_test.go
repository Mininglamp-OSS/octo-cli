package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// These tests cover the contracts the hand-written drive leaves must honour
// because they own a name the spec also describes. Each one is driven through
// NewRootCmd — the real command tree, real registry, real flag parsing — because
// the failure mode being pinned was precisely that a composite replaced a
// generated leaf and enforced less than the leaf did. A cmd/service-level test
// cannot see that: the composite is not registered there.

// --- B1: the spec enum applies to the hand-written share commands ---

// TestDriveShareEnum_PermissionRejectedWithZeroHTTP asserts an out-of-enum
// --permission fails with ENUM_NOT_ALLOWED / exit 2 and reaches no endpoint, on
// both surfaces that post to drive.share.blob-create.
//
// The generated leaf enforced `["view", "download"]` from
// internal/registry/specs/drive.json. `share blob-create` detaches that leaf and
// registers --permission as a plain string flag, and `share create`'s blob
// branch builds the same body, so both had to be wired back to the spec's
// vocabulary rather than sending whatever they were handed.
func TestDriveShareEnum_PermissionRejectedWithZeroHTTP(t *testing.T) {
	for _, args := range [][]string{
		{"drive", "share", "blob-create", "1", "--permission", "edit"},
		{"drive", "share", "create", "1", "--permission", "edit"},
		{"drive", "share", "blob-create", "1", "--permission", "Download"},
		{"drive", "share", "blob-create", "1", "--permission", ""},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			var called bool
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			}, nil)

			err := env.run(args...)
			if err == nil {
				t.Fatal("expected ENUM_NOT_ALLOWED")
			}
			if called {
				t.Error("an out-of-enum value must be refused with zero HTTP")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			if ee.Code != "ENUM_NOT_ALLOWED" {
				t.Errorf("code: got %q, want ENUM_NOT_ALLOWED", ee.Code)
			}
			if ee.ExitCode() != 2 {
				t.Errorf("exit code: got %d, want 2", ee.ExitCode())
			}
		})
	}
}

// TestDriveShareEnum_AcceptedValuesStillReachTheBackend is the other half: the
// gate must not have narrowed the vocabulary the backend accepts.
func TestDriveShareEnum_AcceptedValuesStillReachTheBackend(t *testing.T) {
	for _, permission := range []string{"view", "download"} {
		t.Run(permission, func(t *testing.T) {
			var gotBody string
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				_, _ = w.Write([]byte(`{"id":"tok-1","file_id":1,"permission":"` + permission + `"}`))
			}, nil)

			if err := env.run("drive", "share", "blob-create", "1", "--permission", permission); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if want := `"permission":"` + permission + `"`; !strings.Contains(gotBody, want) {
				t.Errorf("share body: got %s, want it to contain %s", gotBody, want)
			}
		})
	}
}

// --- B2: the token-kind gate precedes dry-run and local success ---

// TestDriveComposites_TokenKindGatedBeforeAnyLocalReturn asserts every
// hand-written composite resolves the credential and applies
// x-octo-allowed-token-kinds before it describes a request (--dry-run) or
// returns a result locally.
//
// The generated leaves route identity before dry-run; the composites returned
// first, so with an unusable credential `--dry-run` described a request that
// could never be sent and `share access <doc-link>` resolved a document target
// outright. An unrecognised token prefix classifies as "unknown", which is
// outside the drive spec's allowed set, so it exercises the gate without
// depending on any particular kind being disallowed.
func TestDriveComposites_TokenKindGatedBeforeAnyLocalReturn(t *testing.T) {
	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "out.bin")

	cases := []struct {
		name   string
		dryRun bool
		args   func(apiURL string) []string
	}{
		{"upload file --dry-run", true, func(string) []string {
			return []string{"drive", "upload", "file", src, "--space-id", "s1"}
		}},
		{"download file --dry-run", true, func(string) []string {
			return []string{"drive", "download", "file", "1", "-o", dest}
		}},
		{"share create --dry-run", true, func(string) []string {
			return []string{"drive", "share", "create", "1"}
		}},
		{"share blob-create --dry-run", true, func(string) []string {
			return []string{"drive", "share", "blob-create", "1"}
		}},
		{"share access --dry-run", true, func(apiURL string) []string {
			return []string{"drive", "share", "access", apiURL + "/drive/s/tok-1"}
		}},
		{"share download --dry-run", true, func(apiURL string) []string {
			return []string{"drive", "share", "download", apiURL + "/drive/s/tok-1", "-o", dest}
		}},
		// The document branch of `share access` succeeds locally with no request
		// at all, so it is the one path a dry-run test would not have covered.
		{"share access on a document link (local success)", false, func(apiURL string) []string {
			return []string{"drive", "share", "access", apiURL + "/d/doc-1?sp=space-1"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			env := newDriveTestEnv(t, "zz_unrecognised", func(w http.ResponseWriter, r *http.Request) {
				called = true
			}, nil)
			env.tf.Globals.DryRun = tc.dryRun

			err := env.run(tc.args(env.api.URL)...)
			if err == nil {
				t.Fatal("expected TOKEN_KIND_NOT_ALLOWED")
			}
			if called {
				t.Error("no request may be sent when the credential kind is refused")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			if ee.Code != "TOKEN_KIND_NOT_ALLOWED" {
				t.Errorf("code: got %q (%s), want TOKEN_KIND_NOT_ALLOWED", ee.Code, ee.Message)
			}
			if ee.ExitCode() != 2 {
				t.Errorf("exit code: got %d, want 2", ee.ExitCode())
			}
			if strings.Contains(env.tf.Out.String(), `"dry_run"`) {
				t.Errorf("a refused credential must not produce a dry-run description: %s", env.tf.Out.String())
			}
		})
	}
}

// TestDriveComposites_DryRunReportsTheResolvedMount is the positive companion:
// now that the mount is resolved before the description is built, the dry-run
// output names the real path instead of a "{mount}" placeholder the caller would
// have to substitute by hand.
func TestDriveComposites_DryRunReportsTheResolvedMount(t *testing.T) {
	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"bot credential", "bf_bot", "/v1/bot/drive/shares"},
		{"user api key", "uk_person", "/v1/user/drive/shares"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, tc.token, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			}, nil)
			env.tf.Globals.DryRun = true

			if err := env.run("drive", "share", "blob-create", "1"); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := env.data(t)["path"]; got != tc.want {
				t.Errorf("path: got %v, want %q", got, tc.want)
			}
		})
	}
}

// --- transfer redirects are re-validated per hop ---

// TestDriveTransfer_UnsafeRedirectFailsClosed asserts assertSafeTransferURL is
// re-run on every redirect target rather than only on the presigned URL the
// backend returned. Without CheckRedirect, Go's default policy followed up to ten
// hops with no re-check, so an accepted https (or loopback-http) URL could
// redirect to plain http on a non-loopback host and the body was still written to
// disk — the stated https-or-loopback rule held for the first hop only.
func TestDriveTransfer_UnsafeRedirectFailsClosed(t *testing.T) {
	// A non-loopback http target. Nothing listens on it and nothing needs to:
	// the redirect must be refused before any connection is attempted.
	const unsafe = "http://storage.example.invalid/obj"

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, unsafe, http.StatusFound)
	})

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := env.run("drive", "download", "file", "1", "-o", dest)
	if err == nil {
		t.Fatal("expected the unsafe redirect to fail the transfer")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
		t.Errorf("error: got %v, want UNSAFE_PRESIGNED_URL", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("nothing may be written when a redirect hop is refused")
	}
	assertNoPartialFiles(t, dest)
}

// TestDriveTransfer_SafeRedirectIsStillFollowed guards the narrowness of the
// check: storage gateways do redirect, and the doc comment on transferClient
// says those are followed deliberately.
func TestDriveTransfer_SafeRedirectIsStillFollowed(t *testing.T) {
	const payload = "redirected-bytes"
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(final.Close)

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, final.URL+"/obj", http.StatusFound)
	})

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := env.run("drive", "download", "file", "1", "-o", dest); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read %q: %v", dest, err)
	}
	if string(got) != payload {
		t.Errorf("file contents: got %q, want %q", got, payload)
	}
}

// --- the partial file is unpredictable and not a symlink target ---

// TestDriveDownload_PartFileIsNotAPredictablePath asserts the download does not
// write through a pre-created "<target>.part". The old fixed name was opened
// with O_CREATE|O_TRUNC and no O_EXCL, so anyone able to write the destination
// directory could plant a symlink there and have the downloaded bytes truncate
// and overwrite whatever it pointed at — assertWritableTarget Lstats the target,
// never the partial file.
func TestDriveDownload_PartFileIsNotAPredictablePath(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")
	victim := filepath.Join(dir, "victim.txt")
	const victimContents = "do not touch"
	if err := os.WriteFile(victim, []byte(victimContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, dest+".part"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded-bytes"))
	})

	if err := env.run("drive", "download", "file", "1", "-o", dest); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("read %q: %v", victim, err)
	}
	if string(got) != victimContents {
		t.Errorf("the symlink target was overwritten: got %q, want %q", got, victimContents)
	}
	if data, rerr := os.ReadFile(dest); rerr != nil || string(data) != "downloaded-bytes" {
		t.Errorf("destination: got %q / %v, want the downloaded bytes", data, rerr)
	}
}

// TestDriveDownload_PartFileNameIsRandom pins the second half of the same
// change: two downloads to the same destination used to share one
// "<target>.part" and interleave into it. os.CreateTemp gives each transfer its
// own O_EXCL file, so the name in flight is never the predictable one and never
// the same twice.
//
// The handler flushes response headers and then blocks, so the assertions run
// while the partial file is genuinely open.
func TestDriveDownload_PartFileNameIsRandom(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "out.bin")

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		inFlight := make(chan []string, 1)
		release := make(chan struct{})

		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj"}`))
		}, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "5")
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			// The client creates its partial file only after Do returns with the
			// response headers, so wait for it to appear rather than looking once.
			inFlight <- awaitPartialFiles(dir)
			<-release
			_, _ = w.Write([]byte("bytes"))
		})

		done := make(chan error, 1)
		go func() { done <- env.run("drive", "download", "file", "1", "-o", dest, "--overwrite") }()

		for _, name := range <-inFlight {
			seen[name] = true
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatalf("execute: %v", err)
		}
	}
	if len(seen) != 2 {
		t.Errorf("partial file names seen in flight: %v; want one distinct name per transfer", seen)
	}
	for name := range seen {
		if name == "out.bin.part" {
			t.Error("the partial file must not use the predictable <target>.part name")
		}
	}
	assertNoPartialFiles(t, dest)
}

// --- transfer errors do not publish the presigned URL ---

// TestDriveTransfer_ErrorOmitsPresignedURL asserts a transport failure against
// object storage reports the host and the cause but not the URL. A presigned
// URL's signature lives in its query string, which makes the whole URL a
// short-lived bearer credential for that object — and *url.Error.Error() embeds
// it, so formatting the error straight into the envelope published it on stderr
// with no --verbose required.
func TestDriveTransfer_ErrorOmitsPresignedURL(t *testing.T) {
	const signature = "SUPERSECRETSIGNATURE123"

	t.Run("download", func(t *testing.T) {
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			// Point at the store's address and then close it, so the transfer
			// fails at dial time with a *url.Error carrying the whole URL.
			_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj?X-Amz-Signature=` + signature + `"}`))
		}, nil)
		env.store.Close()

		dest := filepath.Join(t.TempDir(), "out.bin")
		err := env.run("drive", "download", "file", "1", "-o", dest)
		if err == nil {
			t.Fatal("expected the download to fail")
		}
		assertNoSignature(t, err, env.tf.Out.String()+env.tf.ErrOut.String(), signature)
	})

	t.Run("upload", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "a.txt")
		if werr := os.WriteFile(src, []byte("hello"), 0o600); werr != nil {
			t.Fatal(werr)
		}
		var env *driveTestEnv
		env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/cancel-upload") {
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = w.Write([]byte(`{"file_id":7,"upload_url":"` + env.store.URL + `/obj?X-Amz-Signature=` + signature + `"}`))
		}, nil)
		env.store.Close()

		err := env.run("drive", "upload", "file", src, "--space-id", "s1")
		if err == nil {
			t.Fatal("expected the upload to fail")
		}
		assertNoSignature(t, err, env.tf.Out.String()+env.tf.ErrOut.String(), signature)
	})
}

// --- share password off argv ---

// TestDriveSharePassword_ReadFromFileAndStdin covers the non-argv route added
// alongside --password, mirroring `auth login --token-file` / --with-token: a
// share password on argv is readable from ps and /proc for the process lifetime.
func TestDriveSharePassword_ReadFromFileAndStdin(t *testing.T) {
	// A password whose escaping used to defeat the mask, and with a trailing
	// space that must survive the file read.
	const password = "p@ss\"w\\ord "

	t.Run("from a file", func(t *testing.T) {
		pwFile := filepath.Join(t.TempDir(), "pw")
		// A trailing newline is what every editor and `echo` writes; it must be
		// stripped, and only it.
		if err := os.WriteFile(pwFile, []byte(password+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var gotBody string
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			_, _ = w.Write([]byte(`{"id":"tok-1","file_id":1,"permission":"download","password_set":true}`))
		}, nil)

		if err := env.run("drive", "share", "blob-create", "1", "--password-file", pwFile); err != nil {
			t.Fatalf("execute: %v", err)
		}
		assertBodyCarriesPassword(t, gotBody, password)
	})

	t.Run("from stdin", func(t *testing.T) {
		var gotBody string
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			gotBody = string(raw)
			_, _ = w.Write([]byte(`{"id":"tok-1","file_id":1,"permission":"download","password_set":true}`))
		}, nil)
		env.tf.In.WriteString(password + "\n")

		if err := env.run("drive", "share", "blob-create", "1", "--password-file", "-"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		assertBodyCarriesPassword(t, gotBody, password)
	})

	t.Run("a missing file is a local error with zero HTTP", func(t *testing.T) {
		var called bool
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			called = true
		}, nil)
		err := env.run("drive", "share", "blob-create", "1", "--password-file", filepath.Join(t.TempDir(), "absent"))
		if err == nil {
			t.Fatal("expected a validation error")
		}
		if called {
			t.Error("an unreadable password file must be caught before any request")
		}
	})
}

// --- doc share URLs the CLI can parse back ---

// TestDriveShareCreate_DocRefIDMustRoundTrip asserts a backend ref_id that would
// produce a structurally different URL is refused rather than handed out.
// buildDocShareURL concatenates the doc id into the path, while the consuming
// side (assertShareIDSegment, used by `share access`) enforces a strict charset —
// so an unvalidated `?`, `#`, space or slash let this command emit a link its own
// parser then rejected.
func TestDriveShareCreate_DocRefIDMustRoundTrip(t *testing.T) {
	for _, refID := range []string{"doc?x=1", "doc#frag", "doc 1", "doc/sub", ".."} {
		t.Run(refID, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"id":5,"type":"doc","ref_id":"` + refID + `","doc_space_id":"space-1","name":"n"}`))
			}, nil)
			err := env.run("drive", "share", "create", "5")
			if err == nil {
				t.Fatalf("expected a fail-closed error for ref_id %q", refID)
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "INVALID_SHARE_URL" {
				t.Errorf("error: got %v, want INVALID_SHARE_URL", err)
			}
		})
	}
}

// TestDriveShareCreate_BlobShareIDMustRoundTrip is the blob-branch counterpart:
// share.ID is backend data concatenated into the link path, exactly like the
// document branch's ref_id, so the same charset check applies. Without it the
// command could hand out a share_url that its own parseShareURL then refuses.
func TestDriveShareCreate_BlobShareIDMustRoundTrip(t *testing.T) {
	for _, shareID := range []string{"tok?x=1", "tok#frag", "tok 1", "tok/sub", ".."} {
		t.Run(shareID, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/bot/drive/files/77":
					_, _ = w.Write([]byte(`{"id":77,"type":"blob","name":"c.pdf","size":42,"content_type":"application/pdf"}`))
				default:
					body, _ := json.Marshal(map[string]any{
						"id": shareID, "file_id": 77, "permission": "download",
					})
					_, _ = w.Write(body)
				}
			}, nil)

			err := env.run("drive", "share", "create", "77")
			if err == nil {
				t.Fatalf("expected a fail-closed error for share id %q", shareID)
			}
			if ee := output.AsExitError(err); ee == nil || ee.Code != "INVALID_SHARE_URL" {
				t.Errorf("error: got %v, want INVALID_SHARE_URL", err)
			}
		})
	}
}

// TestDriveShareCreate_EmittedLinksRoundTrip is the positive companion for both
// branches: whatever share_url this command emits must be accepted by the parser
// the receiver uses.
func TestDriveShareCreate_EmittedLinksRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		entry   string
		shareID string
	}{
		{"blob", `{"id":77,"type":"blob","name":"c.pdf","size":42,"content_type":"application/pdf"}`, "tok-9"},
		{"blob with a base64url id starting with a dash", `{"id":77,"type":"blob","name":"c.pdf","size":42}`, "-Ab3cD_x"},
		{"doc", `{"id":5,"type":"doc","ref_id":"doc-1","doc_space_id":"space-1","name":"n"}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/bot/drive/shares" {
					_, _ = w.Write([]byte(`{"id":"` + tc.shareID + `","file_id":77,"permission":"download"}`))
					return
				}
				_, _ = w.Write([]byte(tc.entry))
			}, nil)

			if err := env.run("drive", "share", "create", "77"); err != nil {
				t.Fatalf("execute: %v", err)
			}
			shareURL, ok := env.data(t)["share_url"].(string)
			if !ok || shareURL == "" {
				t.Fatalf("no share_url emitted: %v", env.data(t))
			}
			if _, perr := parseShareURL(&config.Config{APIBaseURL: env.api.URL}, shareURL); perr != nil {
				t.Errorf("the emitted share_url %q is not accepted by share access: %v", shareURL, perr)
			}
		})
	}
}

// --- tabular output agrees with JSON about a uint64 id ---

// TestDriveOutput_TabularFormatsKeepTheIDExact drives the whole stack — real
// command tree, real client, real formatter — because the unit-level guard in
// internal/output cannot see the factory's decode step. A drive response id above
// 2^53 must read the same under --format table / csv / ndjson as under json.
//
// The lossless-id extension converts declared fields to decimal *strings*, so this
// uses a field the spec does not declare, which is the reachable case: any raw
// integer a backend returns.
func TestDriveOutput_TabularFormatsKeepTheIDExact(t *testing.T) {
	const beyondFloat64 = "9007199254740993"

	for _, format := range []string{"json", "table", "csv", "ndjson"} {
		t.Run(format, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"id":1,"type":"blob","name":"c.pdf","size":` + beyondFloat64 + `}`))
			}, nil)
			env.tf.Globals.Format = format

			if err := env.run("drive", "file", "get", "1"); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got := env.tf.Out.String(); !strings.Contains(got, beyondFloat64) {
				t.Errorf("--format %s rounded the value: want %s in\n%s", format, beyondFloat64, got)
			}
		})
	}
}

// --- share links must match the configured origin's scheme ---

// TestParseShareURL_SchemeMustMatchTheOrigin closes the drift between what
// assertSameOrigin documented (strict same-origin) and what it enforced (any
// http(s) scheme with a matching host). The link host is never fetched, so this is
// not an exploit — but the check exists to establish that a link names the
// configured Octo deployment, and a downgraded scheme is a different origin.
func TestParseShareURL_SchemeMustMatchTheOrigin(t *testing.T) {
	cases := []struct {
		name      string
		apiBase   string
		link      string
		wantValid bool
	}{
		{"https origin, https link", "https://octo.example.com", "https://octo.example.com/drive/s/tok", true},
		{"https origin, http link", "https://octo.example.com", "http://octo.example.com/drive/s/tok", false},
		{"http origin, http link", "http://octo.example.com", "http://octo.example.com/drive/s/tok", true},
		{"http origin, https link", "http://octo.example.com", "https://octo.example.com/drive/s/tok", false},
		// A site-relative link has no authority to compare and must keep working.
		{"https origin, relative link", "https://octo.example.com", "/drive/s/tok", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := parseShareURL(&config.Config{APIBaseURL: tc.apiBase}, tc.link)
			if tc.wantValid {
				if err != nil {
					t.Fatalf("expected the link to be accepted: %v", err)
				}
				// The canonical form is always rebuilt from the configured origin.
				if !strings.HasPrefix(parsed.canonical, tc.apiBase) {
					t.Errorf("canonical %q should be rooted at the configured origin %q", parsed.canonical, tc.apiBase)
				}
				return
			}
			if err == nil {
				t.Fatal("expected INVALID_SHARE_URL")
			}
			if err.Code != "INVALID_SHARE_URL" {
				t.Errorf("code: got %q, want INVALID_SHARE_URL", err.Code)
			}
		})
	}
}

// --- helpers ---

// assertNoSignature fails when a presigned signature appears anywhere the caller
// can see: the error envelope, stdout, or stderr.
func assertNoSignature(t *testing.T, err error, streams, signature string) {
	t.Helper()
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(ee.Message, signature) {
		t.Errorf("the error message carries the presigned signature: %s", ee.Message)
	}
	if strings.Contains(string(ee.Detail), signature) {
		t.Errorf("the error detail carries the presigned signature: %s", ee.Detail)
	}
	if strings.Contains(streams, signature) {
		t.Errorf("the emitted output carries the presigned signature: %s", streams)
	}
	// The host is the diagnostic value the URL was carrying; keep it.
	if !strings.Contains(ee.Message, "127.0.0.1") && !strings.Contains(string(ee.Detail), "127.0.0.1") {
		t.Errorf("the error should still name the storage host: %s / %s", ee.Message, ee.Detail)
	}
}

func assertBodyCarriesPassword(t *testing.T, gotBody, password string) {
	t.Helper()
	var decoded map[string]any
	if err := decodeLossless([]byte(gotBody), &decoded); err != nil {
		t.Fatalf("parse share body %q: %v", gotBody, err)
	}
	if decoded["password"] != password {
		t.Errorf("password on the wire: got %q, want %q", decoded["password"], password)
	}
}

// partialFiles lists the in-progress download files in dir.
func partialFiles(t *testing.T, dir string) []string {
	t.Helper()
	if _, err := os.ReadDir(dir); err != nil {
		t.Fatalf("read dir %q: %v", dir, err)
	}
	return listPartialFiles(dir)
}

// listPartialFiles is the t-free form, safe to call from a server goroutine.
func listPartialFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".part") {
			out = append(out, e.Name())
		}
	}
	return out
}

// awaitPartialFiles polls for the partial file a transfer in flight has open.
// The client only creates it once Do has returned with the response headers, so
// a single look right after the flush would race it.
func awaitPartialFiles(dir string) []string {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if names := listPartialFiles(dir); len(names) > 0 {
			return names
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertNoPartialFiles fails when any partial download file survives next to
// target.
func assertNoPartialFiles(t *testing.T, target string) {
	t.Helper()
	if names := partialFiles(t, filepath.Dir(target)); len(names) > 0 {
		t.Errorf("partial files left behind: %v", names)
	}
}
