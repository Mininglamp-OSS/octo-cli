package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// These tests cover the six hand-written drive leaves. The properties that
// matter are the ones a spec cannot express: that a caller credential never
// reaches object storage, that a share link from a third party is parsed and not
// fetched, that a failed upload does not leave a pending row, and that a
// download never overwrites or truncates a local file by accident.

// driveTestEnv is a drive command tree wired to a fake drive API and a fake
// object store, with a recorder for what each one received.
type driveTestEnv struct {
	root  *cobra.Command
	tf    *cmdutil.TestFactory
	api   *httptest.Server
	store *httptest.Server

	// storeRequests records every request the object store saw, so a test can
	// assert what was (and was not) sent to it.
	storeRequests []*http.Request
	storeBodies   []string
}

// newDriveTestEnv builds the environment. apiHandler serves the drive API;
// storeHandler serves presigned object-storage URLs.
func newDriveTestEnv(t *testing.T, token string, apiHandler, storeHandler http.HandlerFunc) *driveTestEnv {
	t.Helper()
	env := &driveTestEnv{}

	env.store = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		env.storeRequests = append(env.storeRequests, r.Clone(r.Context()))
		env.storeBodies = append(env.storeBodies, string(body))
		if storeHandler != nil {
			storeHandler(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(env.store.Close)

	env.api = httptest.NewServer(apiHandler)
	t.Cleanup(env.api.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: env.api.URL, BotToken: token, Format: "json"}
	cred := &credential.BotCredential{Token: token, Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	env.tf = tf
	env.root = NewRootCmd(tf.Factory)
	return env
}

func (e *driveTestEnv) run(args ...string) error {
	e.root.SetArgs(args)
	return e.root.Execute()
}

func (e *driveTestEnv) data(t *testing.T) map[string]any {
	t.Helper()
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(e.tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v (%s)", err, e.tf.Out.String())
	}
	return env.Data
}

// --- command tree ---

// TestDrive_CommandCount pins the 45-command surface: 39 generated leaves plus
// the six composites. A drift here means either a spec op was added without a
// decision or a composite stopped registering.
func TestDrive_CommandCount(t *testing.T) {
	tf := cmdutil.NewTestFactory()
	tf.RegistryFunc = registry.MustNew
	root := NewRootCmd(tf.Factory)

	drive := findDriveCmd(root, "drive")
	if drive == nil {
		t.Fatal("missing drive command")
	}
	want := map[string][]string{
		"":            {"browse"},
		"space":       {"create", "list", "ensure-personal", "get", "rename", "delete"},
		"member":      {"list", "add", "set-role", "remove"},
		"folder":      {"create", "list", "rename", "move", "delete"},
		"file":        {"get", "move", "copy", "rename"},
		"blob":        {"create", "get", "list", "delete"},
		"upload":      {"file", "prepare", "confirm", "cancel"},
		"download":    {"file", "url"},
		"doc":         {"mount", "unmount", "list", "candidates"},
		"share":       {"create", "blob-create", "list", "revoke", "access", "download"},
		"invite":      {"create", "list", "revoke", "accept"},
		"im-transfer": {"create"},
	}
	total := 0
	for group, leaves := range want {
		parent := drive
		if group != "" {
			parent = findDriveCmd(drive, group)
			if parent == nil {
				t.Errorf("missing drive group %q", group)
				continue
			}
		}
		for _, leaf := range leaves {
			total++
			if findDriveCmd(parent, leaf) == nil {
				t.Errorf("missing leaf: drive %s %s", group, leaf)
			}
		}
	}
	if total != 45 {
		t.Errorf("expected 45 drive commands, the table lists %d", total)
	}
	// Nothing beyond the table: a stray leaf (e.g. a duplicate generated
	// `share access`) would otherwise pass unnoticed.
	for group, leaves := range want {
		parent := drive
		if group != "" {
			parent = findDriveCmd(drive, group)
		}
		if parent == nil {
			continue
		}
		for _, child := range parent.Commands() {
			if isDriveGroup(child.Name(), want) && group == "" {
				continue
			}
			if !containsStr(leaves, child.Name()) {
				t.Errorf("unexpected leaf: drive %s %s", group, child.Name())
			}
		}
	}
}

func isDriveGroup(name string, groups map[string][]string) bool {
	_, ok := groups[name]
	return ok && name != ""
}

// --- share URL parsing (the SSRF boundary) ---

// TestParseShareURL_Accepts covers the two link shapes, absolute and relative.
func TestParseShareURL_Accepts(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "https://octo.example.com"}
	cases := []struct {
		name, in, kind, token, docID, docSpace string
	}{
		{"absolute blob link", "https://octo.example.com/drive/s/tok-1", shareKindBlob, "tok-1", "", ""},
		{"relative blob link", "/drive/s/tok-1", shareKindBlob, "tok-1", "", ""},
		{"absolute doc link", "https://octo.example.com/d/d_1?sp=space-9", shareKindDoc, "", "d_1", "space-9"},
		{"relative doc link", "/d/d_1?sp=space-9", shareKindDoc, "", "d_1", "space-9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShareURL(cfg, tc.in)
			if err != nil {
				t.Fatalf("parseShareURL(%q): %v", tc.in, err)
			}
			if got.kind != tc.kind {
				t.Errorf("kind: got %q, want %q", got.kind, tc.kind)
			}
			if got.token != tc.token {
				t.Errorf("token: got %q, want %q", got.token, tc.token)
			}
			if got.docID != tc.docID || got.docSpaceID != tc.docSpace {
				t.Errorf("doc: got (%q,%q), want (%q,%q)", got.docID, got.docSpaceID, tc.docID, tc.docSpace)
			}
			// The canonical form is always absolute on the configured origin, so
			// both sides of a share see the same string.
			if !strings.HasPrefix(got.canonical, "https://octo.example.com/") {
				t.Errorf("canonical: got %q, want it on the configured origin", got.canonical)
			}
		})
	}
}

// TestParseShareURL_Rejects is the SSRF test. A share link arrives from another
// person, so it is untrusted input: the CLI must refuse anything that is not a
// share path on the configured origin, and must never be in a position to fetch
// an attacker-chosen host.
func TestParseShareURL_Rejects(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "https://octo.example.com"}
	cases := []struct{ name, in, wantCode string }{
		{"foreign host", "https://evil.example.com/drive/s/tok-1", "INVALID_SHARE_URL"},
		{"host that merely shares a suffix", "https://notocto.example.com/drive/s/tok-1", "INVALID_SHARE_URL"},
		{"host with an added port", "https://octo.example.com:8443/drive/s/tok-1", "INVALID_SHARE_URL"},
		{"embedded credentials", "https://user:pw@octo.example.com/drive/s/tok-1", "INVALID_SHARE_URL"},
		{"file scheme", "file:///etc/passwd", "INVALID_SHARE_URL"},
		{"gopher scheme", "gopher://octo.example.com/drive/s/tok-1", "INVALID_SHARE_URL"},
		{"encoded slash smuggling a segment", "https://octo.example.com/drive/s/tok%2F..%2Fadmin", "INVALID_SHARE_URL"},
		{"extra path segment", "https://octo.example.com/drive/s/tok-1/extra", "INVALID_SHARE_URL"},
		{"empty token", "https://octo.example.com/drive/s/", "INVALID_SHARE_URL"},
		{"unrelated path", "https://octo.example.com/v1/bot/drive/spaces", "INVALID_SHARE_URL"},
		{"empty input", "", "INVALID_SHARE_URL"},
		{"doc link without sp", "https://octo.example.com/d/d_1", "MISSING_DOC_SPACE_ID"},
		{"doc link with an empty sp", "https://octo.example.com/d/d_1?sp=", "MISSING_DOC_SPACE_ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseShareURL(cfg, tc.in)
			if err == nil {
				t.Fatalf("parseShareURL(%q) should have failed, got %+v", tc.in, got)
			}
			if err.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q (%s)", err.Code, tc.wantCode, err.Message)
			}
			if err.ExitCode() != 2 {
				t.Errorf("exit code: got %d, want 2", err.ExitCode())
			}
		})
	}
}

// --- share access / download ---

// TestDriveShareAccess_Blob checks the whole-URL entry point: the token is
// parsed out locally and the configured API is called with the caller's
// credential attached. There is no anonymous share path.
func TestDriveShareAccess_Blob(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	env := newDriveTestEnv(t, "uk_person", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"permission":"download","file_id":18446744073709551615,"file_name":"c.pdf","file_size":42,"content_type":"application/pdf"}`))
	}, nil)

	shareURL := env.api.URL + "/drive/s/tok-1"
	if err := env.run("drive", "share", "access", shareURL, "--password", "pw"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The user-API-key mount, because the credential is a uk_ token.
	if gotPath != "/v1/user/drive/shares/tok-1/access" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotAuth != "Bearer uk_person" {
		t.Errorf("Authorization: got %q — share access must authenticate", gotAuth)
	}
	if gotBody != `{"password":"pw"}` {
		t.Errorf("body: got %s", gotBody)
	}
	data := env.data(t)
	if data["kind"] != shareKindBlob {
		t.Errorf("kind: got %v", data["kind"])
	}
	if data["downloadable"] != true {
		t.Errorf("downloadable: got %v, want true for a download share", data["downloadable"])
	}
	if data["drive_file_id"] != "18446744073709551615" {
		t.Errorf("drive_file_id: got %v, want the lossless decimal string", data["drive_file_id"])
	}
}

// TestDriveShareAccess_Doc confirms a document link resolves locally: it carries
// its own target and grants nothing, so there is nothing to ask the backend.
func TestDriveShareAccess_Doc(t *testing.T) {
	var called bool
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		called = true
	}, nil)

	if err := env.run("drive", "share", "access", env.api.URL+"/d/d_1?sp=space-9"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called {
		t.Error("a document link must resolve locally, with no backend call")
	}
	data := env.data(t)
	if data["kind"] != shareKindDoc {
		t.Errorf("kind: got %v", data["kind"])
	}
	if data["downloadable"] != false {
		t.Error("a document is never downloadable")
	}
	if data["doc_space_id"] != "space-9" {
		t.Errorf("doc_space_id: got %v, want the sp parameter", data["doc_space_id"])
	}
}

// TestDriveShareDownload_DocRejectedLocally pins the fail-fast: a document link
// must be refused before any request goes out, with the code the spec names.
func TestDriveShareDownload_DocRejectedLocally(t *testing.T) {
	var called bool
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		called = true
	}, nil)

	dest := filepath.Join(t.TempDir(), "out.bin")
	err := env.run("drive", "share", "download", env.api.URL+"/d/d_1?sp=space-9", "-o", dest)
	if err == nil {
		t.Fatal("expected NOT_DOWNLOADABLE")
	}
	if called {
		t.Error("no request may be sent for a document link")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "NOT_DOWNLOADABLE" {
		t.Fatalf("code: got %v, want NOT_DOWNLOADABLE", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("nothing must be written for a document link")
	}
}

// TestDriveShareDownload_WritesFile is the happy path: token → authenticated
// download endpoint → object store → atomic local write, and the object store
// must not see the caller's credential.
func TestDriveShareDownload_WritesFile(t *testing.T) {
	const payload = "shared-bytes"
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj","filename":"c.pdf"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	})

	dest := filepath.Join(t.TempDir(), "out.pdf")
	if err := env.run("drive", "share", "download", env.api.URL+"/drive/s/tok-1", "-o", dest); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read %q: %v", dest, err)
	}
	if string(got) != payload {
		t.Errorf("file contents: got %q, want %q", got, payload)
	}
	assertStoreGotNoCredential(t, env)
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Error("the .part file must be renamed away on success")
	}
	data := env.data(t)
	if data["filename"] != "c.pdf" {
		t.Errorf("filename: got %v, want the backend's suggestion", data["filename"])
	}
	if data["sha256"] == "" || data["sha256"] == nil {
		t.Error("sha256 must be reported so the caller can verify the transfer")
	}
}

// --- share create ---

// TestDriveShareCreate_Blob confirms the composite looks the node up, creates a
// token, and returns the link the two sides actually exchange.
func TestDriveShareCreate_Blob(t *testing.T) {
	var sharePost string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/drive/files/77":
			_, _ = w.Write([]byte(`{"id":77,"type":"blob","name":"c.pdf","size":42,"content_type":"application/pdf"}`))
		case "/v1/bot/drive/shares":
			raw, _ := io.ReadAll(r.Body)
			sharePost = string(raw)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"tok-9","file_id":77,"permission":"download"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}, nil)

	if err := env.run("drive", "share", "create", "77"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(sharePost, `"file_id":77`) {
		t.Errorf("share body: got %s, want file_id as a JSON integer", sharePost)
	}
	data := env.data(t)
	if data["kind"] != shareKindBlob {
		t.Errorf("kind: got %v", data["kind"])
	}
	if want := env.api.URL + "/drive/s/tok-9"; data["share_url"] != want {
		t.Errorf("share_url: got %v, want %q", data["share_url"], want)
	}
	if data["share_id"] != "tok-9" {
		t.Errorf("share_id: got %v", data["share_id"])
	}
	if data["downloadable"] != true {
		t.Errorf("downloadable: got %v", data["downloadable"])
	}
	// The emitted link must round-trip through the parser the receiver uses.
	if _, err := parseShareURL(&config.Config{APIBaseURL: env.api.URL}, data["share_url"].(string)); err != nil {
		t.Errorf("the emitted share_url is not accepted by share access: %v", err)
	}
}

// TestDriveShareCreate_Doc pins the document branch, including the fail-closed
// rule: without doc_space_id the link would point at the wrong Octo Space, so
// the command must refuse rather than substitute the drive space id.
func TestDriveShareCreate_Doc(t *testing.T) {
	t.Run("builds a link from doc_space_id", func(t *testing.T) {
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/bot/drive/shares" {
				t.Error("a document must not create a blob share token")
			}
			_, _ = w.Write([]byte(`{"id":5,"type":"doc","ref_id":"d_1","doc_space_id":"space-9","space_id":"shared:drive-1","name":"Spec"}`))
		}, nil)
		if err := env.run("drive", "share", "create", "5"); err != nil {
			t.Fatalf("execute: %v", err)
		}
		data := env.data(t)
		if want := env.api.URL + "/d/d_1?sp=space-9"; data["share_url"] != want {
			t.Errorf("share_url: got %v, want %q", data["share_url"], want)
		}
		if data["downloadable"] != false {
			t.Error("a document link is never downloadable")
		}
		if strings.Contains(data["share_url"].(string), "shared:drive-1") {
			t.Error("the drive space id must never appear in a document link")
		}
	})

	t.Run("fails closed without doc_space_id", func(t *testing.T) {
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":5,"type":"doc","ref_id":"d_1","space_id":"shared:drive-1"}`))
		}, nil)
		err := env.run("drive", "share", "create", "5")
		if err == nil {
			t.Fatal("expected MISSING_DOC_SPACE_ID")
		}
		ee := output.AsExitError(err)
		if ee == nil || ee.Code != "MISSING_DOC_SPACE_ID" {
			t.Fatalf("code: got %v, want MISSING_DOC_SPACE_ID", err)
		}
	})

	t.Run("refuses a folder", func(t *testing.T) {
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"id":5,"type":"folder","name":"docs"}`))
		}, nil)
		err := env.run("drive", "share", "create", "5")
		ee := output.AsExitError(err)
		if ee == nil || ee.Code != "NOT_SHAREABLE" {
			t.Fatalf("code: got %v, want NOT_SHAREABLE", err)
		}
	})
}

// TestDriveShareBlobCreate_PositionalFileID confirms the low-level twin takes
// the file id positionally (as appendix B specifies) and emits the split
// share_id / share_token names.
func TestDriveShareBlobCreate_PositionalFileID(t *testing.T) {
	var gotBody string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"tok-3","file_id":18446744073709551615,"permission":"view","password_set":true}`))
	}, nil)

	if err := env.run("drive", "share", "blob-create", "18446744073709551615", "--permission", "view", "--password", "hunter2"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(gotBody, `"file_id":18446744073709551615`) {
		t.Errorf("body: got %s, want the exact uint64 as a JSON integer", gotBody)
	}
	data := env.data(t)
	if data["share_id"] != "tok-3" || data["share_token"] != "tok-3" {
		t.Errorf("share_id/share_token: got %v / %v", data["share_id"], data["share_token"])
	}
	if data["drive_file_id"] != "18446744073709551615" {
		t.Errorf("drive_file_id: got %v", data["drive_file_id"])
	}
}

// --- upload ---

// TestDriveUploadFile_HappyPath walks prepare → PUT → confirm and asserts the
// two properties a spec cannot: the PUT echoes the signed headers and exact
// length, and it carries none of the caller's credentials.
func TestDriveUploadFile_HappyPath(t *testing.T) {
	const payload = "hello-drive"
	var confirmPath, confirmBody string
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/bot/drive/files/prepare-upload":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"file_id":18446744073709551615,"status":"pending","upload_url":"` +
				env.store.URL + `/put","object_path":"k/1","content_type":"text/plain","content_disposition":"attachment; filename=\"a.txt\"","max_file_size":11}`))
		case strings.HasSuffix(r.URL.Path, "/confirm-upload"):
			confirmPath = r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			confirmBody = string(raw)
			_, _ = w.Write([]byte(`{"id":18446744073709551615,"parent_id":0,"status":"confirmed","name":"a.txt"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}, nil)

	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if len(env.storeRequests) != 1 {
		t.Fatalf("object store saw %d requests, want 1", len(env.storeRequests))
	}
	put := env.storeRequests[0]
	if put.Method != http.MethodPut {
		t.Errorf("store method: got %s, want PUT", put.Method)
	}
	if env.storeBodies[0] != payload {
		t.Errorf("store body: got %q, want %q", env.storeBodies[0], payload)
	}
	if put.ContentLength != int64(len(payload)) {
		t.Errorf("Content-Length: got %d, want %d", put.ContentLength, len(payload))
	}
	if got := put.Header.Get("Content-Type"); got != "text/plain" {
		t.Errorf("Content-Type: got %q, want the signed value echoed back", got)
	}
	if got := put.Header.Get("Content-Disposition"); got != `attachment; filename="a.txt"` {
		t.Errorf("Content-Disposition: got %q, want the signed value echoed back", got)
	}
	assertStoreGotNoCredential(t, env)

	// The confirm must address the pending row by its exact uint64 id.
	if confirmPath != "/v1/bot/drive/files/18446744073709551615/confirm-upload" {
		t.Errorf("confirm path: got %q", confirmPath)
	}
	if !strings.Contains(confirmBody, `"actual_size":11`) {
		t.Errorf("confirm body: got %s", confirmBody)
	}
	if data := env.data(t); data["id"] != "18446744073709551615" {
		t.Errorf("data.id: got %v, want the lossless decimal string", data["id"])
	}
}

// TestDriveUploadFile_CancelsPendingOnFailure is the important failure test: a
// PUT that fails must not leave a pending row behind, and the error must name
// the file id and the cancel outcome so an operator can act.
func TestDriveUploadFile_CancelsPendingOnFailure(t *testing.T) {
	var cancelled string
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/bot/drive/files/prepare-upload":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"file_id":42,"status":"pending","upload_url":"` + env.store.URL + `/put","content_type":"text/plain","max_file_size":5}`))
		case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
			cancelled = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1")
	if err == nil {
		t.Fatal("expected the upload to fail")
	}
	if cancelled != "/v1/bot/drive/files/42/cancel-upload" {
		t.Errorf("cancel path: got %q, want the pending file to be cancelled", cancelled)
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "UPLOAD_FAILED" {
		t.Fatalf("code: got %v, want UPLOAD_FAILED", err)
	}
	if !strings.Contains(string(ee.Detail), `"file_id":"42"`) {
		t.Errorf("detail must carry the pending file id: %s", ee.Detail)
	}
	if !strings.Contains(string(ee.Detail), `"pending_file":"cancelled"`) {
		t.Errorf("detail must report the cancel outcome: %s", ee.Detail)
	}
}

// TestDriveUploadFile_RejectsUnsafeUploadURL confirms a presigned URL on plain
// http to a non-loopback host is refused — and that the pending row is still
// cancelled, because prepare already created it.
func TestDriveUploadFile_RejectsUnsafeUploadURL(t *testing.T) {
	var cancelled bool
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/bot/drive/files/prepare-upload":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"file_id":9,"status":"pending","upload_url":"http://storage.example.com/put","content_type":"text/plain","max_file_size":5}`))
		case strings.HasSuffix(r.URL.Path, "/cancel-upload"):
			cancelled = true
			w.WriteHeader(http.StatusNoContent)
		}
	}, nil)

	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1")
	if err == nil {
		t.Fatal("expected the unsafe URL to be refused")
	}
	if len(env.storeRequests) != 0 {
		t.Error("no bytes may be sent to an unsafe URL")
	}
	if !cancelled {
		t.Error("the pending file must be cancelled after refusing the URL")
	}
}

// TestDriveUploadFile_LocalValidation rejects inputs before any request, so a
// bad invocation never creates a pending row.
func TestDriveUploadFile_LocalValidation(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, path, parentID string }{
		{"missing file", filepath.Join(dir, "nope.txt"), "0"},
		{"directory", dir, "0"},
		{"empty file", empty, "0"},
		{"bad parent id", empty, "not-a-number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			}, nil)
			err := env.run("drive", "upload", "file", tc.path, "--space-id", "s1", "--parent-id", tc.parentID)
			if err == nil {
				t.Fatal("expected a local validation error")
			}
			if called {
				t.Error("no request may be sent when local validation fails")
			}
		})
	}
}

// TestDriveUploadFile_DryRunTouchesNothing pins the dry-run contract: it must
// describe the prepare request and stop, without creating a pending row or
// fetching a presigned URL.
func TestDriveUploadFile_DryRunTouchesNothing(t *testing.T) {
	var called bool
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		called = true
	}, nil)
	env.tf.Globals.DryRun = true

	src := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.run("drive", "upload", "file", src, "--space-id", "shared:s1"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if called {
		t.Error("--dry-run must not reach the API")
	}
	if len(env.storeRequests) != 0 {
		t.Error("--dry-run must not reach object storage")
	}
	data := env.data(t)
	if data["dry_run"] != true {
		t.Errorf("expected a dry-run description, got %v", data)
	}
}

// --- download ---

// TestDriveDownloadFile_WritesAtomically covers the happy path plus the two
// local-safety rules: an existing destination is refused by default, and no
// .part file survives.
func TestDriveDownloadFile_WritesAtomically(t *testing.T) {
	const payload = "file-bytes"
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj","filename":"a.txt"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	})

	dest := filepath.Join(t.TempDir(), "a.txt")
	if err := env.run("drive", "download", "file", "77", "-o", dest); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != payload {
		t.Errorf("contents: got %q, want %q", got, payload)
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Error("no .part file may survive a successful download")
	}
	assertStoreGotNoCredential(t, env)
}

// TestDriveDownloadFile_RefusesExistingFile pins the default: an existing file
// is never clobbered, and the refusal happens before a signed URL is requested.
func TestDriveDownloadFile_RefusesExistingFile(t *testing.T) {
	var called bool
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		called = true
	}, nil)

	dest := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := env.run("drive", "download", "file", "77", "-o", dest)
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "FILE_EXISTS" {
		t.Fatalf("code: got %v, want FILE_EXISTS", err)
	}
	if called {
		t.Error("the refusal must happen before a signed URL is requested")
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "original" {
		t.Error("the existing file must be untouched")
	}
}

// TestDriveDownloadFile_OverwriteReplaces confirms --overwrite is honoured.
func TestDriveDownloadFile_OverwriteReplaces(t *testing.T) {
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj","filename":"a.txt"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("replacement"))
	})

	dest := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := env.run("drive", "download", "file", "77", "-o", dest, "--overwrite"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "replacement" {
		t.Errorf("contents: got %q, want the downloaded bytes", got)
	}
}

// TestDriveDownloadFile_RemovesPartialOnFailure confirms an object store error
// leaves no half-written file and no .part debris.
func TestDriveDownloadFile_RemovesPartialOnFailure(t *testing.T) {
	var env *driveTestEnv
	env = newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"` + env.store.URL + `/obj","filename":"a.txt"}`))
	}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	dest := filepath.Join(t.TempDir(), "a.txt")
	err := env.run("drive", "download", "file", "77", "-o", dest)
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "DOWNLOAD_FAILED" {
		t.Fatalf("code: got %v, want DOWNLOAD_FAILED", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("no destination file may be left behind")
	}
	if _, statErr := os.Stat(dest + ".part"); !os.IsNotExist(statErr) {
		t.Error("no .part debris may be left behind")
	}
}

// TestDriveDownloadFile_RejectsUnsafeURL keeps the CLI from fetching a URL the
// backend should never have returned.
func TestDriveDownloadFile_RejectsUnsafeURL(t *testing.T) {
	cases := []struct{ name, url string }{
		{"plain http on a public host", "http://storage.example.com/obj"},
		{"file scheme", "file:///etc/passwd"},
		{"embedded credentials", "https://user:pw@storage.example.com/obj"},
		{"relative url", "/obj"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"url":"` + tc.url + `","filename":"a.txt"}`))
			}, nil)
			dest := filepath.Join(t.TempDir(), "a.txt")
			err := env.run("drive", "download", "file", "77", "-o", dest)
			ee := output.AsExitError(err)
			if ee == nil || ee.Code != "UNSAFE_PRESIGNED_URL" {
				t.Fatalf("code: got %v, want UNSAFE_PRESIGNED_URL", err)
			}
			if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
				t.Error("nothing may be written")
			}
		})
	}
}

// --- helpers ---

// assertStoreGotNoCredential is the M-3 guard: object storage must never see the
// caller's Octo credential or space context. The presigned URL is its own
// authorisation; forwarding a bearer token to a third-party host would leak it.
func assertStoreGotNoCredential(t *testing.T, env *driveTestEnv) {
	t.Helper()
	for i, r := range env.storeRequests {
		for _, header := range []string{"Authorization", "X-Space-Id"} {
			if v := r.Header.Get(header); v != "" {
				t.Errorf("object store request %d carried %s=%q; presigned transfers must send no caller credential", i, header, v)
			}
		}
	}
}

func findDriveCmd(parent *cobra.Command, name string) *cobra.Command {
	if parent == nil {
		return nil
	}
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TestDriveUploadFile_DoesNotBlockOnANonRegularPath is round-11's finding at
// d3b80b7, in code this PR added.
//
// runDriveUploadFile opened the path before asking what kind of file it was, and
// opening a FIFO for reading blocks until a writer appears. So `drive upload file
// <fifo>` hung indefinitely — before the size check, before credential resolution,
// and before the --dry-run branch that promises to touch nothing — which also
// contradicts the command's own contract that a non-regular file is rejected locally.
// Verified by hand at d3b80b7: the command had to be killed.
//
// The assertion is on *completing*, not on the error alone: the whole defect is that
// no error ever arrives, so a test that only checked the code would hang rather than
// fail, and a hanging test reports nothing useful.
func TestDriveUploadFile_DoesNotBlockOnANonRegularPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no mkfifo on windows")
	}
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	// A symlink to the FIFO: rejecting must be decided by what the path resolves to,
	// or the check is bypassed by one indirection.
	link := filepath.Join(dir, "link-to-pipe")
	if err := os.Symlink(fifo, link); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, path string
		dryRun     bool
	}{
		{"fifo", fifo, false},
		{"fifo, dry run", fifo, true},
		{"symlink to fifo", link, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			}, nil)
			args := []string{"drive", "upload", "file", tc.path, "--space-id", "s1"}
			if tc.dryRun {
				args = append(args, "--dry-run")
			}

			done := make(chan error, 1)
			go func() { done <- env.run(args...) }()

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a non-regular file must be rejected locally")
				}
				if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
					t.Errorf("error = %v, want a validation error", err)
				}
				if called {
					t.Error("no request may be sent when local validation fails")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("upload file blocked on a non-regular path: opening a FIFO for reading waits for " +
					"a writer, so the command never reached the check that was supposed to reject it")
			}
		})
	}
}

// TestDriveUploadFile_AcceptsASymlinkToARegularFile is the allow direction of the
// check above, and it is not optional: the reject direction alone is satisfied by
// refusing symlinks outright (os.Lstat would do that), which would break uploading
// through a symlink — something that worked before and has no reason to stop. A guard
// with only its refusing half gets loosened by the next person it blocks.
func TestDriveUploadFile_AcceptsASymlinkToARegularFile(t *testing.T) {
	dir := t.TempDir()
	targetFile := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(targetFile, []byte("some bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link-to-payload")
	if err := os.Symlink(targetFile, link); err != nil {
		t.Fatal(err)
	}

	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("--dry-run must not send a request, got %s %s", r.Method, r.URL.Path)
	}, nil)
	if err := env.run("drive", "upload", "file", link, "--space-id", "s1", "--dry-run"); err != nil {
		t.Errorf("a symlink to a regular file must still be uploadable: %v", err)
	}
}
