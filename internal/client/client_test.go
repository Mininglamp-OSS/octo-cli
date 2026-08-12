package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func newTestClient(srv *httptest.Server) *Client {
	cfg := &config.Config{APIBaseURL: srv.URL}
	cred := &credential.BotCredential{Token: "app_test", SpaceID: "space-1"}
	c := New(cfg, cred, Options{})
	c.httpClient = srv.Client()
	return c
}

func TestDo_SetsAuthAndSpaceHeaders(t *testing.T) {
	var gotAuth, gotSpace string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSpace = r.Header.Get("X-Space-Id")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	if _, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/test"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer app_test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q", gotSpace)
	}
}

func TestDo_LoopSelectsPublicAPIContract(t *testing.T) {
	t.Parallel()

	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{
		Service: "loop",
		Method:  http.MethodGet,
		Path:    "/fleet/api/v1/workspaces",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAccept != loopAcceptMediaType {
		t.Fatalf("Accept = %q, want %q", gotAccept, loopAcceptMediaType)
	}
}

func TestDo_LoopPreservesExplicitAccept(t *testing.T) {
	t.Parallel()

	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{
		Service: "loop",
		Method:  http.MethodGet,
		Path:    "/fleet/api/v1/workspaces",
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q, want application/json", gotAccept)
	}
}

func TestDo_SuppressSpaceHeader(t *testing.T) {
	var sawSpaceHeader bool
	var gotSpace string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, sawSpaceHeader = r.Header["X-Space-Id"]
		gotSpace = r.Header.Get("X-Space-Id")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// Credential carries a space, but the request opts out of the header.
	c := newTestClient(srv)
	if _, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/test", SuppressSpaceHeader: true}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if sawSpaceHeader {
		t.Errorf("X-Space-Id must be omitted when SuppressSpaceHeader is set; got %q", gotSpace)
	}
}

func TestDo_SetsContentTypeForBody(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{
		Method: "POST", Path: "/t", Body: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
}

func TestDo_NoContentTypeForGET(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, _ = c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if gotCT != "" {
		t.Errorf("Content-Type should be empty, got %q", gotCT)
	}
}

func TestDo_BuildsQueryString(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/t",
		Query: map[string][]string{"a": {"1"}, "b": {"two"}},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(gotQuery, "a=1") || !strings.Contains(gotQuery, "b=two") {
		t.Errorf("query = %q", gotQuery)
	}
}

func TestDo_BackendErrorParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"code":"MATTER_NOT_FOUND","message":"matter not found"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if err == nil {
		t.Fatal("expected error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("error is not *ExitError: %T", err)
	}
	if ee.Code != "MATTER_NOT_FOUND" {
		t.Errorf("code = %q", ee.Code)
	}
	if ee.Type != "api_error" {
		t.Errorf("type = %q", ee.Type)
	}
}

func TestDo_SelectsBackendErrorProtocolByService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BRAND_NEW_CODE","message":"bad thing","hint":"do better"}}`))
	}))
	defer srv.Close()

	for _, tc := range []struct {
		service  string
		wantType string
		wantExit int
	}{
		{service: "loop", wantType: "validation", wantExit: 2},
		{service: "thread", wantType: "api_error", wantExit: 1},
	} {
		t.Run(tc.service, func(t *testing.T) {
			c := newTestClient(srv)
			_, err := c.Do(context.Background(), &Request{Service: tc.service, Method: http.MethodGet, Path: "/error"})
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("Do error = %v, want ExitError", err)
			}
			if ee.Type != tc.wantType || ee.ExitCode() != tc.wantExit || ee.Hint != "do better" {
				t.Fatalf("error = %+v (exit %d), want type=%s exit=%d with server hint",
					ee, ee.ExitCode(), tc.wantType, tc.wantExit)
			}
		})
	}
}

func TestDo_RetryOn503ThenSucceed(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(503)
			w.Write([]byte(`{"error":{"code":"UPSTREAM_UNAVAILABLE","message":"try later"}}`))
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	body, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(string(body), `"ok":1`) {
		t.Errorf("body = %s", body)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestDo_NoRetryFlag(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(503)
		w.Write([]byte(`{"error":{"code":"UPSTREAM_UNAVAILABLE","message":"x"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.options.NoRetry = true

	_, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("calls = %d, want 1 (NoRetry)", calls)
	}
}

func TestDo_NoRetryOn400(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR","message":"bad"}}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if err == nil {
		t.Fatal("expected error")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("400 should not retry, calls = %d", calls)
	}
}

func TestDo_DryRunDoesNotCallServer(t *testing.T) {
	var called int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&called, 1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	c.options.DryRun = true

	body, err := c.Do(context.Background(), &Request{
		Method: "POST", Path: "/x", Body: map[string]string{"title": "hi"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Error("dry-run should not hit the server")
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["dry_run"] != true {
		t.Errorf("dry_run flag missing: %v", out)
	}
	if out["method"] != "POST" {
		t.Errorf("method = %v", out["method"])
	}
	// token must be redacted
	hdrs, _ := out["headers"].(map[string]any)
	if auth, _ := hdrs["Authorization"].(string); !strings.Contains(auth, "***") {
		t.Errorf("token not redacted: %v", auth)
	}
}

func TestDo_VerboseWritesToErrOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var errBuf bytes.Buffer
	c := newTestClient(srv)
	c.options.Verbose = true
	c.options.ErrOut = &errBuf

	_, _ = c.Do(context.Background(), &Request{Method: "GET", Path: "/t"})
	if !strings.Contains(errBuf.String(), "GET") {
		t.Errorf("verbose trace missing: %q", errBuf.String())
	}
}

func TestDo_RedactsWriteOnlyJSONFields(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		receivedBody = string(raw)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var errBuf bytes.Buffer
	c := newTestClient(srv)
	c.options.DryRun = true
	c.options.Verbose = true
	c.options.ErrOut = &errBuf
	body, err := c.Do(context.Background(), &Request{
		Method: http.MethodPut,
		Path:   "/secret",
		Body: map[string]any{
			"signing_secret": "SUPERSECRET123456",
			"label":          "safe",
			"big":            json.Number("12345678901234567890"),
			"nested":         map[string]any{"id": json.Number("9007199254740993")},
		},
		SensitiveJSONFields: []string{"signing_secret"},
		Headers:             map[string]string{"authorization": "raw-secret"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if strings.Contains(string(body), "SUPERSECRET") || strings.Contains(string(body), "raw-secret") {
		t.Fatalf("dry-run leaked a secret: %s", body)
	}
	if !strings.Contains(string(body), "[REDACTED]") || !strings.Contains(string(body), "***") {
		t.Fatalf("dry-run did not redact fields and credential: %s", body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var dryRun map[string]any
	if err := dec.Decode(&dryRun); err != nil {
		t.Fatalf("decode dry-run body: %v", err)
	}
	dryRunBody, ok := dryRun["body"].(map[string]any)
	if !ok {
		t.Fatalf("dry-run body = %#v, want object", dryRun["body"])
	}
	if got := dryRunBody["big"]; got != json.Number("12345678901234567890") {
		t.Fatalf("dry-run big number = %v, want lossless value", got)
	}
	nested, ok := dryRunBody["nested"].(map[string]any)
	if !ok || nested["id"] != json.Number("9007199254740993") {
		t.Fatalf("dry-run nested number = %#v, want lossless value", dryRunBody["nested"])
	}

	c.options.DryRun = false
	errBuf.Reset()
	_, err = c.Do(context.Background(), &Request{
		Method:              http.MethodPut,
		Path:                "/secret",
		Body:                map[string]string{"signing_secret": "SUPERSECRET123456", "label": "safe"},
		SensitiveJSONFields: []string{"signing_secret"},
	})
	if err != nil {
		t.Fatalf("verbose Do: %v", err)
	}
	if strings.Contains(errBuf.String(), "SUPERSECRET") || !strings.Contains(errBuf.String(), "[REDACTED]") {
		t.Fatalf("verbose output did not redact secret: %s", errBuf.String())
	}
	if !strings.Contains(receivedBody, "SUPERSECRET123456") {
		t.Fatalf("redaction changed the wire body: %s", receivedBody)
	}
}

func TestDo_ServiceURLRouting(t *testing.T) {
	// With the unified API base URL model, all services route to APIBaseURL.
	var hitService string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitService = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	cfg := &config.Config{APIBaseURL: srv.URL}
	cred := &credential.BotCredential{Token: "app_test"}
	c := New(cfg, cred, Options{})
	c.httpClient = srv.Client()

	_, _ = c.Do(context.Background(), &Request{Service: "matters", Method: "GET", Path: "/api/v1/matters"})
	if hitService != "/api/v1/matters" {
		t.Errorf("matters service should route to gateway, got path %q", hitService)
	}

	_, _ = c.Do(context.Background(), &Request{Service: "dmworkim", Method: "GET", Path: "/v1/bot/info"})
	if hitService != "/v1/bot/info" {
		t.Errorf("dmworkim service should route to gateway, got path %q", hitService)
	}

	_, _ = c.Do(context.Background(), &Request{Method: "GET", Path: "/fallback"})
	if hitService != "/fallback" {
		t.Errorf("default service should route to gateway, got path %q", hitService)
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	if d := parseRetryAfter("5"); d != 5*time.Second {
		t.Errorf("parseRetryAfter(\"5\") = %s", d)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	if d := parseRetryAfter(""); d != 0 {
		t.Errorf("expected 0, got %s", d)
	}
}

// --- binary response path (file.download) ---

// newBinaryClient returns a Client that does NOT follow redirects, so 302s
// surface to Do for the binary envelope path.
func newBinaryClient(srv *httptest.Server) *Client {
	cfg := &config.Config{APIBaseURL: srv.URL}
	cred := &credential.BotCredential{Token: "app_test"}
	c := New(cfg, cred, Options{})
	// srv.Client() follows redirects by default; keep our no-follow policy.
	sc := srv.Client()
	sc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.httpClient = sc
	return c
}

func TestDo_BinaryResponse_RedirectReturnsLocationEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example.com/object?sig=abc")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/dl", BinaryResponse: true,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, body)
	}
	if env["url"] != "https://cdn.example.com/object?sig=abc" {
		t.Errorf("url = %v", env["url"])
	}
	if n, _ := env["status"].(float64); int(n) != http.StatusFound {
		t.Errorf("status = %v", env["status"])
	}
}

func TestDo_BinaryResponse_InlineBodyReturnsMetadata(t *testing.T) {
	payload := bytes.Repeat([]byte{0xff}, 128)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/dl", BinaryResponse: true,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, body)
	}
	if env["content_type"] != "image/png" {
		t.Errorf("content_type = %v", env["content_type"])
	}
	if n, _ := env["status"].(float64); int(n) != 200 {
		t.Errorf("status = %v", env["status"])
	}
	if n, _ := env["size"].(float64); int(n) != len(payload) {
		t.Errorf("size = %v, want %d", env["size"], len(payload))
	}
}

func TestDo_BinaryResponse_OutputPathWritesFile(t *testing.T) {
	payload := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "board.png")
	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/export", BinaryResponse: true, OutputPath: dest,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// The bytes are written verbatim to the destination.
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("written bytes differ: got %d bytes, want %d", len(got), len(payload))
	}
	// The envelope reports the saved path + size.
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, body)
	}
	if env["output"] != dest {
		t.Errorf("output = %v, want %s", env["output"], dest)
	}
	if n, _ := env["size"].(float64); int(n) != len(payload) {
		t.Errorf("size = %v, want %d", env["size"], len(payload))
	}
}

func TestDo_BinaryResponse_OutputPathUnwritableIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write([]byte{1, 2, 3})
	}))
	defer srv.Close()

	// A path under a non-existent directory cannot be created → validation error.
	dest := filepath.Join(t.TempDir(), "no-such-dir", "board.png")
	c := newBinaryClient(srv)
	if _, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/export", BinaryResponse: true, OutputPath: dest,
	}); err == nil {
		t.Fatalf("expected an error writing to an unwritable path")
	}
}

// TestDo_BinaryResponse_OutputPathWritesSVG covers the SVG branch of
// docs.scene.export end-to-end: an image/svg+xml body lands on disk verbatim
// and the envelope echoes the saved path. Only the PNG path was exercised
// before, so the SVG content-type was covered only indirectly.
func TestDo_BinaryResponse_OutputPathWritesSVG(t *testing.T) {
	payload := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10"/></svg>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.WriteHeader(200)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "board.svg")
	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/export", BinaryResponse: true, OutputPath: dest,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("written bytes differ: got %q, want %q", got, payload)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, body)
	}
	if env["output"] != dest {
		t.Errorf("output = %v, want %s", env["output"], dest)
	}
	if env["content_type"] != "image/svg+xml" {
		t.Errorf("content_type = %v, want image/svg+xml", env["content_type"])
	}
}

// TestDo_BinaryResponse_OutputWriteFailurePreservesExistingFile pins the
// atomicity fix: a mid-write failure (here, the destination directory is not
// writable so the atomic temp file cannot be created) must leave an existing
// good copy at the destination untouched rather than clobbering/truncating it,
// and must not leave a stray temp file behind.
func TestDo_BinaryResponse_OutputWriteFailurePreservesExistingFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory write permissions are not enforced")
	}
	newBytes := bytes.Repeat([]byte{0x89, 0x50, 0x4e, 0x47}, 16) // the download that must NOT land
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write(newBytes)
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "board.png")
	existing := []byte("previous good copy")
	if err := os.WriteFile(dest, existing, 0o644); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}
	// Read-only directory: the atomic write cannot create its temp file, so the
	// write fails after the good copy already exists.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }() // restore so t.TempDir cleanup works

	c := newBinaryClient(srv)
	if _, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/export", BinaryResponse: true, OutputPath: dest,
	}); err == nil {
		t.Fatalf("expected a write error, got nil")
	}

	// The pre-existing good copy must survive the failed write verbatim.
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read existing file after failed write: %v", err)
	}
	if !bytes.Equal(got, existing) {
		t.Errorf("existing file was clobbered: got %q, want %q", got, existing)
	}

	// No stray temp file should be left in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "board.png" {
			t.Errorf("unexpected leftover file in dir: %q", e.Name())
		}
	}
}

// TestDo_BinaryResponse_OutputWriteFailureIsNonRetryable pins the PR's central
// invariant: a local write failure is a non-retryable validation error, so a
// successful server request is never re-downloaded just because the disk write
// failed. Asserts the error taxonomy and that the server handler is hit exactly
// once (no retry loop).
func TestDo_BinaryResponse_OutputWriteFailureIsNonRetryable(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		_, _ = w.Write([]byte{1, 2, 3})
	}))
	defer srv.Close()

	// Unwritable destination: parent directory does not exist.
	dest := filepath.Join(t.TempDir(), "no-such-dir", "board.png")
	c := newBinaryClient(srv)
	_, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/export", BinaryResponse: true, OutputPath: dest,
	})
	if err == nil {
		t.Fatal("expected a write error, got nil")
	}
	var ee *output.ExitError
	if !errors.As(err, &ee) || ee.Type != "validation" {
		t.Errorf("expected a validation ExitError, got %T: %v", err, err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("server hit %d times; a local write failure must not trigger a retry (want 1)", got)
	}
}

// TestDo_BinaryResponse_RedirectWithOutputPathWritesNothing pins the documented
// promise on the OutputPath field: a 3xx (redirect-to-URL) response is never
// written to disk even when OutputPath is set — its Location is surfaced in the
// envelope instead.
func TestDo_BinaryResponse_RedirectWithOutputPathWritesNothing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example.com/board.png")
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(302)
		_, _ = w.Write([]byte("this body must never reach disk"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "board.png")
	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), &Request{
		Method: "GET", Path: "/dl", BinaryResponse: true, OutputPath: dest,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("a redirect response must not write a file; os.Stat err = %v", statErr)
	}
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal: %v -- %s", err, body)
	}
	if env["url"] != "https://cdn.example.com/board.png" {
		t.Errorf("url = %v, want the Location header", env["url"])
	}
	if _, ok := env["output"]; ok {
		t.Errorf("redirect envelope must not carry an output key, got %v", env["output"])
	}
}

func TestBackoffDelay_Grows(t *testing.T) {
	d1 := backoffDelay(1)
	d3 := backoffDelay(3)
	if d1 < 250*time.Millisecond || d1 > 500*time.Millisecond {
		t.Errorf("attempt 1 delay out of range: %s", d1)
	}
	if d3 < d1 {
		t.Errorf("expected attempt 3 >= attempt 1: %s vs %s", d3, d1)
	}
	if d3 > defaultMaxDelay {
		t.Errorf("attempt 3 exceeded cap: %s", d3)
	}
}

func TestBackoffDelay_OverflowSafe(t *testing.T) {
	// Extreme attempt values must not overflow or panic.
	for _, attempt := range []int{0, -1, 50, 100, 1000} {
		d := backoffDelay(attempt)
		if d < 0 || d > defaultMaxDelay {
			t.Errorf("attempt %d: delay %s out of [0, %s]", attempt, d, defaultMaxDelay)
		}
	}
}

// --- expanded coverage: parseRetryAfter ---

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(5 * time.Second).UTC().Format(http.TimeFormat)
	d := parseRetryAfter(future)
	if d <= 0 || d > 6*time.Second {
		t.Errorf("parseRetryAfter(date) = %s, want ~5s", d)
	}
}

func TestParseRetryAfter_PastDate(t *testing.T) {
	past := time.Now().Add(-10 * time.Minute).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(past); d != 0 {
		t.Errorf("past dates should yield 0, got %s", d)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	if d := parseRetryAfter("not a time"); d != 0 {
		t.Errorf("garbage should yield 0, got %s", d)
	}
}

func TestParseRetryAfter_NegativeSeconds(t *testing.T) {
	// Negative seconds fall through both strconv and ParseTime → 0.
	if d := parseRetryAfter("-5"); d != 0 {
		t.Errorf("negative seconds should yield 0, got %s", d)
	}
}

// --- expanded coverage: buildURL ---

func TestBuildURL_NoLeadingSlash(t *testing.T) {
	u, err := buildURL("http://h", "path/x", nil)
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	if u != "http://h/path/x" {
		t.Errorf("buildURL = %q", u)
	}
}

func TestBuildURL_NormalizesBoundarySlashes(t *testing.T) {
	u, err := buildURL("https://example.com/", "/fleet/api/v1/tasks", nil)
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	if u != "https://example.com/fleet/api/v1/tasks" {
		t.Errorf("buildURL = %q", u)
	}
}

func TestBuildURL_MergesQuery(t *testing.T) {
	u, err := buildURL("http://h", "/p?existing=1", url.Values{"a": {"2"}})
	if err != nil {
		t.Fatalf("buildURL: %v", err)
	}
	if !strings.Contains(u, "existing=1") || !strings.Contains(u, "a=2") {
		t.Errorf("missing params in %q", u)
	}
}

func TestBuildURL_Invalid(t *testing.T) {
	_, err := buildURL(":::bad://", "/x", nil)
	if err == nil {
		t.Error("expected parse error for malformed base")
	}
}

// --- dry-run integration via Do ---

func TestDo_DryRunReturnsSyntheticBody(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "http://ignored"}
	cred := &credential.BotCredential{Token: "app_drytok"}
	c := New(cfg, cred, Options{DryRun: true})

	body, err := c.Do(context.Background(), &Request{
		Method: "POST", Path: "/t", Body: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	var out map[string]any
	if jerr := json.Unmarshal(body, &out); jerr != nil {
		t.Fatalf("unmarshal: %v\n%s", jerr, body)
	}
	if out["dry_run"] != true {
		t.Errorf("missing dry_run flag: %v", out)
	}
	// Body should round-trip through synthetic envelope.
	rb, _ := out["body"].(map[string]any)
	if rb["k"] != "v" {
		t.Errorf("body payload lost: %v", out["body"])
	}
}

// --- Retry-After header respected ---

func TestDo_RetryAfterRespected(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMITED","message":"wait"}}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":1}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	start := time.Now()
	body, err := c.Do(context.Background(), &Request{Method: "GET", Path: "/r"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if elapsed < 800*time.Millisecond {
		t.Errorf("expected ≥~1s wait from Retry-After, got %s", elapsed)
	}
	if !strings.Contains(string(body), `"ok":1`) {
		t.Errorf("body = %s", body)
	}
}

// --- verbose logging captures request body ---

func TestDo_VerboseLogsRequestBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var errBuf bytes.Buffer
	c := newTestClient(srv)
	c.options.Verbose = true
	c.options.ErrOut = &errBuf

	_, err := c.Do(context.Background(), &Request{
		Method: "POST", Path: "/t", Body: map[string]string{"k": "v"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	out := errBuf.String()
	if !strings.Contains(out, "POST") {
		t.Errorf("verbose missing method: %q", out)
	}
	if !strings.Contains(out, "request body") {
		t.Errorf("verbose missing body trace: %q", out)
	}
	if !strings.Contains(out, "←") {
		t.Errorf("verbose missing response trace: %q", out)
	}
}

// --- unknown service base URL ---

func TestDo_UnknownServiceBaseURL(t *testing.T) {
	cfg := &config.Config{} // no URLs set
	cred := &credential.BotCredential{Token: "t"}
	c := New(cfg, cred, Options{})

	_, err := c.Do(context.Background(), &Request{
		Service: "matters", Method: "GET", Path: "/x",
	})
	if err == nil {
		t.Fatal("expected error for missing base URL")
	}
	ee := output.AsExitError(err)
	if ee == nil || ee.Type != "validation" {
		t.Errorf("expected validation error, got %T %v", err, err)
	}
}

// --- invalid timeout emits a warning but still returns a working client ---

func TestNew_InvalidTimeoutWarns(t *testing.T) {
	var buf bytes.Buffer
	cfg := &config.Config{APIBaseURL: "http://x"}
	c := New(cfg, &credential.BotCredential{Token: "t"}, Options{
		Timeout: "not-a-duration",
		ErrOut:  &buf,
	})
	if c == nil {
		t.Fatal("client nil")
	}
	if !strings.Contains(buf.String(), "invalid --timeout") {
		t.Errorf("expected warning, got %q", buf.String())
	}
}

// --- retryable vs non-retryable status codes ---

func TestIsRetryableStatus(t *testing.T) {
	retryable := []int{429, 502, 503, 504}
	for _, s := range retryable {
		if !isRetryableStatus(s) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	nonRetryable := []int{400, 401, 403, 404, 500}
	for _, s := range nonRetryable {
		if isRetryableStatus(s) {
			t.Errorf("status %d should NOT be retryable", s)
		}
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hi", 10) != "hi" {
		t.Errorf("short string should pass through")
	}
	got := truncate("1234567890abc", 5)
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncate marker missing: %q", got)
	}
}

// --- x-octo-secret redaction ---

// A share token in the URL path and a share password in the body are
// credential-equivalent. Both must go on the wire untouched but must never
// appear in a --verbose trace or --dry-run description, which are the surfaces
// most likely to end up in a log or a ticket.
func TestSecretValues_RedactedInVerboseButSentOnTheWire(t *testing.T) {
	const token = "share-token-abc"
	const password = "hunter2"

	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var trace bytes.Buffer
	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "bf_secret_token"},
		Options{Verbose: true, ErrOut: &trace},
	)
	if _, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/bot/drive/shares/" + token + "/download",
		Body:         map[string]any{"password": password},
		SecretValues: []string{token, password},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	// The wire is untouched: redaction is a logging concern only.
	if !strings.Contains(gotPath, token) {
		t.Errorf("request path lost the token: %q", gotPath)
	}
	if !strings.Contains(gotBody, password) {
		t.Errorf("request body lost the password: %q", gotBody)
	}

	log := trace.String()
	if strings.Contains(log, token) {
		t.Errorf("verbose trace leaked the share token:\n%s", log)
	}
	if strings.Contains(log, password) {
		t.Errorf("verbose trace leaked the share password:\n%s", log)
	}
	if !strings.Contains(log, "REDACTED") {
		t.Errorf("verbose trace should show the masked values:\n%s", log)
	}
}

func TestSecretValues_RedactedInDryRun(t *testing.T) {
	const token = "share-token-abc"
	const password = "hunter2"

	c := New(
		&config.Config{APIBaseURL: "https://octo.test"},
		&credential.BotCredential{Token: "bf_secret_token"},
		Options{DryRun: true, ErrOut: io.Discard},
	)
	body, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/bot/drive/shares/" + token + "/access",
		Body:         map[string]any{"password": password},
		SecretValues: []string{token, password},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	out := string(body)
	if strings.Contains(out, token) || strings.Contains(out, password) {
		t.Errorf("dry-run description leaked a secret:\n%s", out)
	}
	if !strings.Contains(out, "REDACTED") {
		t.Errorf("dry-run description should show masked values:\n%s", out)
	}
	// The bot token itself stays masked by the existing MaskToken path.
	if strings.Contains(out, "bf_secret_token") {
		t.Errorf("dry-run description leaked the bot token:\n%s", out)
	}
}

// A uint64 id above 2^53 must be echoed exactly in --dry-run. A plain unmarshal
// would round it and make the dry run misreport the request.
func TestDryRun_PreservesUint64Precision(t *testing.T) {
	c := New(
		&config.Config{APIBaseURL: "https://octo.test"},
		&credential.BotCredential{Token: "bf_t"},
		Options{DryRun: true, ErrOut: io.Discard},
	)
	body, err := c.Do(context.Background(), &Request{
		Method: http.MethodPost,
		Path:   "/v1/bot/drive/folders",
		Body:   map[string]any{"parent_id": json.Number("18446744073709551615")},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !strings.Contains(string(body), "18446744073709551615") {
		t.Errorf("dry-run body rounded the id:\n%s", body)
	}
}
