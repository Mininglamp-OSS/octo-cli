package cmd

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Round-17 review findings on the two hand-written paths that accept a flag and then
// decide, per node type or per status code, whether it means anything.

// TestDriveShareCreate_DocBranchRefusesBlobOnlyFlags is round-17 P1-B.
//
// prepareBlobShare loads --password / --password-file and builds the full share body
// *before* the node lookup, and the document branch then returns emitDocShareTarget,
// which makes no request at all. So shareBody — password included — was silently
// discarded: `drive share create <doc-mount-id> --password-file pw` exited 0 with a
// share_url, and the operator handed over a link believing it was password-gated. It
// was not, and nothing in the envelope said so.
//
// The help text does say "(blob shares only)", but help text is not a runtime control:
// once the caller has actually supplied the flag, the only safe answers are refuse or
// warn, and this PR's own stated principle — the neighbouring MISSING_DOC_SPACE_ID path
// "fails closed rather than substituting" — picks refuse. Silently dropping a
// credential-class parameter is the defect class this branch has spent sixteen rounds
// closing.
//
// The decision is on whether the caller *set* the flag, not on the value: --expires-in-seconds 0
// means "use the backend default" and is a different statement from not passing it at all.
func TestDriveShareCreate_DocBranchRefusesBlobOnlyFlags(t *testing.T) {
	const docEntry = `{"id":5,"type":"doc","ref_id":"d_1","doc_space_id":"space-9","space_id":"shared:drive-1","name":"Spec"}`

	for _, tc := range []struct {
		name  string
		args  []string
		names []string
	}{
		{"password", []string{"--password", "hunter2hunter2"}, []string{"--password"}},
		{"password-file", nil, []string{"--password-file"}}, // args filled in below
		{"expires-in-seconds", []string{"--expires-in-seconds", "3600"}, []string{"--expires-in-seconds"}},
		{"expires-in-seconds explicitly zero", []string{"--expires-in-seconds", "0"}, []string{"--expires-in-seconds"}},
		{"password and expires together", []string{"--password", "hunter2hunter2", "--expires-in-seconds", "60"},
			[]string{"--password", "--expires-in-seconds"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := tc.args
			if tc.name == "password-file" {
				pw := filepath.Join(t.TempDir(), "pw.txt")
				if err := os.WriteFile(pw, []byte("hunter2hunter2\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = []string{"--password-file", pw}
			}

			var shareRequests int
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/shares") {
					shareRequests++
				}
				_, _ = w.Write([]byte(docEntry))
			}, nil)

			err := env.run(append([]string{"drive", "share", "create", "5"}, args...)...)
			if err == nil {
				t.Fatalf("a document node must refuse %v instead of dropping it; output was:\n%s",
					tc.names, env.tf.Out.String())
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "SHARE_FLAG_NOT_APPLICABLE" {
				t.Fatalf("error = %v, want code SHARE_FLAG_NOT_APPLICABLE", err)
			}
			for _, want := range tc.names {
				if !strings.Contains(ee.Message, want) {
					t.Errorf("the refusal must name %s: %s", want, ee.Message)
				}
			}
			// No share_url may be emitted, and no share may be created.
			if out := env.tf.Out.String(); strings.Contains(out, "share_url") {
				t.Errorf("a share_url was emitted for a refused call:\n%s", out)
			}
			if shareRequests != 0 {
				t.Errorf("the share endpoint was called %d times on a refused call", shareRequests)
			}
			// The refusal must not echo the password itself.
			if strings.Contains(ee.Message+ee.Hint, "hunter2hunter2") {
				t.Errorf("the refusal echoed the password: %s / %s", ee.Message, ee.Hint)
			}
		})
	}
}

// TestDriveShareCreate_DocBranchStillWorksWithoutBlobOnlyFlags is the allow direction:
// the guard must key on the caller having set a flag, not on the node being a document.
func TestDriveShareCreate_DocBranchStillWorksWithoutBlobOnlyFlags(t *testing.T) {
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":5,"type":"doc","ref_id":"d_1","doc_space_id":"space-9","space_id":"shared:drive-1","name":"Spec"}`))
	}, nil)
	if err := env.run("drive", "share", "create", "5"); err != nil {
		t.Fatalf("a plain document share must still work: %v", err)
	}
	data := env.data(t)
	if data["kind"] != shareKindDoc {
		t.Errorf("kind = %v, want %s", data["kind"], shareKindDoc)
	}
	if data["share_url"] == "" || data["share_url"] == nil {
		t.Error("a document share must still return a share_url")
	}
}

// TestDriveShareCreate_BlobBranchStillAppliesTheFlags pins that the guard did not become a
// blanket refusal: on a blob node the same flags must still reach the share request body.
// Review asked for `share create` and `share blob-create` to agree, and they do by
// construction — blob-create always posts the body, so it can never drop these — but the
// blob branch of `share create` is the half that could have regressed here.
func TestDriveShareCreate_BlobBranchStillAppliesTheFlags(t *testing.T) {
	const password = "hunter2hunter2"

	var shareBody string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/shares") {
			raw, _ := io.ReadAll(r.Body)
			shareBody = string(raw)
			_, _ = w.Write([]byte(`{"id":"TOKEN1234567","file_id":5,"permission":"download"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":5,"type":"blob","space_id":"shared:drive-1","name":"c.pdf"}`))
	}, nil)

	if err := env.run("drive", "share", "create", "5", "--password", password, "--expires-in-seconds", "3600"); err != nil {
		t.Fatalf("blob share: %v", err)
	}
	if !strings.Contains(shareBody, password) {
		t.Errorf("the password must still reach the blob share request: %s", shareBody)
	}
	if !strings.Contains(shareBody, "3600") {
		t.Errorf("expires_in_seconds must still reach the blob share request: %s", shareBody)
	}
}

// TestDriveUpload_OnlyAStoredObjectIsConfirmed is round-17 P1-A.
//
// putObject accepted the whole 2xx family, so a presigned PUT answered `202 Accepted`
// (which an async storage front end plausibly does mean) or `204 No Content` returned nil,
// runDriveUploadFile went on to confirm-upload with the local size, and the CLI emitted
// ok:true for a drive row pointing at an object that may never have been written.
//
// The download half of this same command refuses anything but exactly 200, and its comment
// justifies that by asserting "the upload half of this command already refuses the same
// shape from the same untrusted host" — which was not true. There is no post-PUT
// verification of any kind (no ETag check, no size echo, no HEAD), so the status code is
// the only evidence the CLI has that the bytes landed.
//
// 201 is accepted alongside 200 because a storage backend may legitimately answer either
// for a create; 202 and 204 are not evidence of storage, so they are refused with their own
// code — a caller has to be able to tell "storage rejected this" from "storage never
// confirmed the bytes landed", and neither may be reported as a local network fault.
func TestDriveUpload_OnlyAStoredObjectIsConfirmed(t *testing.T) {
	for _, tc := range []struct {
		status     int
		wantOK     bool
		wantCode   string
		wantCancel bool
	}{
		{http.StatusOK, true, "", false},
		{http.StatusCreated, true, "", false},
		{http.StatusAccepted, false, "UPLOAD_NOT_CONFIRMED", true},
		{http.StatusNoContent, false, "UPLOAD_NOT_CONFIRMED", true},
		// The pre-existing branch must keep its own code, so the two states stay
		// distinguishable.
		{http.StatusForbidden, false, "UPLOAD_FAILED", true},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			var confirmed, cancelled bool
			var env *driveTestEnv
			env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/v1/bot/drive/files/prepare-upload":
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"file_id":7,"status":"pending","upload_url":"` +
						env.store.URL + `/put","object_path":"k/1","max_file_size":11}`))
				case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
					confirmed = true
					_, _ = w.Write([]byte(`{"id":7,"parent_id":0,"status":"confirmed","name":"a.txt"}`))
				case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
					cancelled = true
					_, _ = w.Write([]byte(`{}`))
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
			}, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			})

			src := filepath.Join(t.TempDir(), "a.txt")
			if err := os.WriteFile(src, []byte("hello-drive"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1")

			if tc.wantOK {
				if err != nil {
					t.Fatalf("a %d PUT must be accepted as stored: %v", tc.status, err)
				}
				if !confirmed {
					t.Error("a stored object must be confirmed")
				}
				return
			}
			if err == nil {
				t.Fatalf("a %d PUT is not evidence the object was stored, but the upload succeeded", tc.status)
			}
			if confirmed {
				t.Errorf("a %d PUT must not leave a confirmed row behind", tc.status)
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			if ee.Code != tc.wantCode {
				t.Errorf("code = %q, want %q — the caller must be able to tell a storage refusal "+
					"from an unconfirmed store, and neither is a local network fault", ee.Code, tc.wantCode)
			}
			// The cancel fallback must still fire on the new branch, or the refusal
			// trades a bad confirmed row for a stuck pending one.
			if tc.wantCancel && !cancelled {
				t.Errorf("the pending row was not cancelled after a %d PUT", tc.status)
			}
		})
	}
}

// TestDriveShareCreate_ReportsWhetherAPasswordWasApplied is the sharper half of round-17
// P1-B: refusing the document branch stops the silent drop, but on the blob branch the
// caller still could not confirm that a password took effect.
//
// `password_set` is the established convention everywhere else in this domain — declared on
// the share object in drive.json, decoded into shareResponse.PasswordSet, and emitted by
// `share list`, `share access --dry-run`, `share download --dry-run` and `share create
// --dry-run`. `share create`'s *success* envelope was the one surface that dropped it, and
// --dry-run cannot stand in because the dry run stops before the lookup that picks the branch.
//
// Reported on both branches, and deliberately not omitempty: "false" is the answer a caller
// needs on a document link, where a password cannot apply at all, and an absent field would
// be indistinguishable from an older binary that never reported it.
func TestDriveShareCreate_ReportsWhetherAPasswordWasApplied(t *testing.T) {
	for _, tc := range []struct {
		name        string
		entry       string
		args        []string
		shareReply  string
		wantSet     bool
		wantPresent bool
	}{
		{
			name:        "blob with a password",
			entry:       `{"id":5,"type":"blob","space_id":"shared:drive-1","name":"c.pdf"}`,
			args:        []string{"--password", "hunter2hunter2"},
			shareReply:  `{"id":"TOKEN1234567","file_id":5,"permission":"download","password_set":true}`,
			wantSet:     true,
			wantPresent: true,
		},
		{
			name:        "blob without a password",
			entry:       `{"id":5,"type":"blob","space_id":"shared:drive-1","name":"c.pdf"}`,
			shareReply:  `{"id":"TOKEN1234567","file_id":5,"permission":"download","password_set":false}`,
			wantSet:     false,
			wantPresent: true,
		},
		{
			// The backend is the authority: if it reports no password despite one being
			// sent, the envelope must say so rather than echo the caller's intent.
			name:        "blob where the backend did not apply the password",
			entry:       `{"id":5,"type":"blob","space_id":"shared:drive-1","name":"c.pdf"}`,
			args:        []string{"--password", "hunter2hunter2"},
			shareReply:  `{"id":"TOKEN1234567","file_id":5,"permission":"download","password_set":false}`,
			wantSet:     false,
			wantPresent: true,
		},
		{
			name:        "document link, where a password cannot apply",
			entry:       `{"id":5,"type":"doc","ref_id":"d_1","doc_space_id":"space-9","space_id":"shared:drive-1","name":"Spec"}`,
			wantSet:     false,
			wantPresent: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/shares") {
					_, _ = w.Write([]byte(tc.shareReply))
					return
				}
				_, _ = w.Write([]byte(tc.entry))
			}, nil)

			if err := env.run(append([]string{"drive", "share", "create", "5"}, tc.args...)...); err != nil {
				t.Fatalf("share create: %v", err)
			}
			data := env.data(t)
			got, present := data["password_set"]
			if !present {
				t.Fatalf("password_set is absent, so a caller cannot confirm whether the share "+
					"is password-gated:\n%s", env.tf.Out.String())
			}
			if present != tc.wantPresent || got != tc.wantSet {
				t.Errorf("password_set = %v, want %v", got, tc.wantSet)
			}
		})
	}
}

// TestDriveTransfer_InitialPresignedURLIsJudgedLikeARedirectHop is round-17 P1-D.
//
// The https arm of assertSafeTransferTarget was empty, while the redirect hop refused exactly
// these spellings — so `https://localhost/obj` was accepted as an initial upload_url or
// download_url and refused as a hop. The comment justifying the proxy-path narrowing
// ("the string pre-filter still rejects every literal local spelling before any of this")
// depended on the check that was not there, which is the same defect shape as the download-side
// comment in P1-A: a note a future reader uses to decide not to look here.
//
// The exploitable path needs a configured proxy and a name that does not resolve locally, so
// the severity is conditional — on the direct path the dial-time address classification closes
// it. The asymmetry is the thing being fixed: an initial URL and a hop are now judged
// identically. Note http.ProxyFromEnvironment's own loopback filter does not cover "localhost."
// or "*.localhost", so it does not stand in for this.
func TestDriveTransfer_InitialPresignedURLIsJudgedLikeARedirectHop(t *testing.T) {
	for _, raw := range []string{
		"https://localhost/obj",
		"https://localhost./obj",
		"https://foo.localhost/obj",
		"https://127.0.0.1/obj",
		"https://[::1]/obj",
		"https://127.0.0.1.:443/obj",
	} {
		t.Run(raw, func(t *testing.T) {
			u, perr := url.Parse(raw)
			if perr != nil {
				t.Fatalf("parse: %v", perr)
			}
			// loopbackAPI=false is the remote-origin deployment, which is the one where a
			// local target is never legitimate.
			err := assertSafeTransferTarget("download_url", u, false)
			if err == nil {
				t.Fatalf("%s was accepted as an initial presigned URL while the identical "+
					"spelling is refused as a redirect hop", raw)
			}
			if err.Code != "UNSAFE_PRESIGNED_URL" {
				t.Errorf("code = %q, want UNSAFE_PRESIGNED_URL", err.Code)
			}
		})
	}

	// The allow direction, and the bound: against a local Octo origin, loopback storage is
	// the documented local-development setup and must keep working on both schemes.
	for _, raw := range []string{"https://localhost/obj", "http://127.0.0.1:9000/obj"} {
		u, _ := url.Parse(raw)
		if err := assertSafeTransferTarget("download_url", u, true); err != nil {
			t.Errorf("%s must still be accepted against a loopback Octo origin: %v", raw, err)
		}
	}
	// And a real remote host is unaffected.
	u, _ := url.Parse("https://storage.example.invalid/obj")
	if err := assertSafeTransferTarget("download_url", u, false); err != nil {
		t.Errorf("a remote https target must be accepted: %v", err)
	}
}
