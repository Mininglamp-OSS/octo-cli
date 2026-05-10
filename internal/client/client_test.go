package client

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmwork-org/octo-cli/internal/config"
	"github.com/dmwork-org/octo-cli/internal/credential"
	"github.com/dmwork-org/octo-cli/internal/output"
)

func newTestClient(srv *httptest.Server) *Client {
	cfg := &config.Config{APIURL: srv.URL}
	cred := &credential.BotCredential{Token: "app_test", SpaceID: "space-1"}
	c := New(cfg, cred, Options{})
	c.httpClient = srv.Client()
	c.retryClock = func(time.Duration) {}
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
	if _, err := c.Do(context.Background(), Request{Method: "GET", Path: "/test"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotAuth != "Bearer app_test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q", gotSpace)
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
	_, err := c.Do(context.Background(), Request{
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
	_, _ = c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
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
	_, err := c.Do(context.Background(), Request{
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
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
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
	body, err := c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
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

	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
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
	_, err := c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
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

	body, err := c.Do(context.Background(), Request{
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
	if auth, _ := hdrs["Authorization"].(string); !strings.Contains(auth, "****") {
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

	_, _ = c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
	if !strings.Contains(errBuf.String(), "GET") {
		t.Errorf("verbose trace missing: %q", errBuf.String())
	}
}

func TestDo_ServiceURLRouting(t *testing.T) {
	var hit string
	mattersSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = "matters"
		w.Write([]byte(`{}`))
	}))
	defer mattersSrv.Close()
	defaultSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = "default"
		w.Write([]byte(`{}`))
	}))
	defer defaultSrv.Close()

	cfg := &config.Config{APIURL: defaultSrv.URL, MattersURL: mattersSrv.URL}
	cred := &credential.BotCredential{Token: "app_test"}
	c := New(cfg, cred, Options{})
	c.httpClient = mattersSrv.Client()

	_, _ = c.Do(context.Background(), Request{Service: "matters", Method: "GET", Path: "/t"})
	if hit != "matters" {
		t.Errorf("expected matters URL, got %q", hit)
	}

	_, _ = c.Do(context.Background(), Request{Method: "GET", Path: "/t"})
	if hit != "default" {
		t.Errorf("expected default URL, got %q", hit)
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
	cfg := &config.Config{APIURL: srv.URL}
	cred := &credential.BotCredential{Token: "app_test"}
	c := New(cfg, cred, Options{})
	// srv.Client() follows redirects by default; keep our no-follow policy.
	sc := srv.Client()
	sc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	c.httpClient = sc
	c.retryClock = func(time.Duration) {}
	return c
}

func TestDo_BinaryResponse_RedirectReturnsLocationEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://cdn.example.com/object?sig=abc")
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	c := newBinaryClient(srv)
	body, err := c.Do(context.Background(), Request{
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
	body, err := c.Do(context.Background(), Request{
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
