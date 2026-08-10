// Package client is the REST transport for the Octo backend. It supports
// per-service URL routing (matters / dmworkim / default), retry with
// exponential backoff + jitter, Retry-After, verbose logging, and --dry-run.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Retry defaults per architecture-design.md §6.2.
const (
	defaultMaxRetries = 3
	defaultBaseDelay  = 500 * time.Millisecond
	defaultMaxDelay   = 10 * time.Second
	defaultTimeout    = 30 * time.Second
)

// Options controls client runtime behaviour. Zero values are sensible defaults.
type Options struct {
	Verbose bool
	DryRun  bool
	NoRetry bool
	Timeout string    // raw flag value, parsed once at client construction
	ErrOut  io.Writer // verbose traces and dry-run output go here
}

// Request is a generic API request. Service selects per-service URL; Path is
// the URL suffix (e.g. "/api/v1/todos/t1"). Body is JSON-encoded if non-nil.
// Query is merged into the URL; headers take precedence over client defaults.
//
// For non-JSON payloads (e.g. multipart uploads) set RawBody + ContentType.
// When RawBody is non-nil, Body is ignored and no JSON marshaling is performed.
type Request struct {
	Service     string
	Method      string
	Path        string
	Query       url.Values
	Body        any
	Headers     map[string]string
	RawBody     []byte
	ContentType string
	// BinaryResponse asks the client to treat 3xx/non-JSON responses as
	// structured metadata envelopes rather than parsing JSON. See file.download.
	BinaryResponse bool
	// OutputPath, when set together with BinaryResponse, makes the client WRITE
	// a 2xx binary body to that file path (instead of only describing it) and
	// return an envelope carrying the saved path + size. Empty preserves the
	// historical describe-only behaviour. A 3xx (redirect-to-URL) response is
	// never written — its URL is surfaced in the envelope as before.
	OutputPath string
	// SuppressSpaceHeader omits the X-Space-Id header even when the active
	// credential carries a SpaceID. It is set for operations whose spec declares
	// x-octo-space-header:false (e.g. the docs bot mount resolves the space
	// server-side). The default (false) preserves the historical behaviour of
	// sending X-Space-Id whenever the credential has a space.
	SuppressSpaceHeader bool
	// SecretValues holds literal values that must never be written to a log:
	// share tokens, invite tokens, and share passwords, declared in the spec via
	// x-octo-secret. Every occurrence is replaced with a mask in --verbose
	// traces and --dry-run output, whether it appears in the URL path or the
	// request body. The values still go on the wire unchanged.
	SecretValues []string
}

// secretMask replaces a redacted value in verbose / dry-run output. It is a
// fixed string so the secret's length is not revealed either.
const secretMask = "***REDACTED***"

// redactSecrets masks every non-empty SecretValues entry found in s. Values are
// masked longest-first so a secret that contains another as a substring cannot
// leave a fragment behind.
//
// Every encoding the same value can wear on its way into a log line is masked,
// not just the literal: a secret in a URL path arrives percent-encoded, and one
// inside a marshalled JSON body arrives with `"`, `\` and control characters
// escaped. Masking only the literal is how a password containing a quote used to
// survive a `--verbose` trace verbatim — json.Marshal had already rewritten it,
// so the substitution found nothing to replace.
func redactSecrets(s string, secrets []string) string {
	if s == "" || len(secrets) == 0 {
		return s
	}
	ordered := make([]string, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			ordered = append(ordered, v)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, v := range ordered {
		for _, form := range secretForms(v) {
			s = replaceSecretForm(s, form)
		}
	}
	return s
}

// shortSecretRunes is the length below which a secret is masked only where it
// appears as a whole token rather than anywhere at all.
//
// Substring masking is right for anything long enough that a match means
// something. For a very short value it inverts: a one-character password is a
// substring of almost every message, so masking every occurrence rewrites text
// that has nothing to do with the secret — including a backend's own error code —
// while protecting a value that carries almost no secrecy to begin with. Eight is
// chosen so a token-length value keeps unconditional masking and a
// keyboard-mashed password does not shred the response it appears in.
const shortSecretRunes = 8

// replaceSecretForm masks one encoded spelling of a secret in s.
//
// A long form is replaced everywhere. A short one is replaced only where it is
// delimited by non-alphanumeric characters (or the ends of the string), so a
// genuine echo — `"password":"abc123"`, or `bad password abc123 for share` — is
// still caught while the letters of an unrelated word are not.
func replaceSecretForm(s, form string) string {
	if form == "" {
		return s
	}
	if len([]rune(form)) >= shortSecretRunes {
		return strings.ReplaceAll(s, form, secretMask)
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], form) && isTokenBoundary(s, i, i+len(form)) {
			b.WriteString(secretMask)
			i += len(form)
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// isTokenBoundary reports whether s[start:end] is delimited by something other
// than an alphanumeric character on both sides, which is what distinguishes a
// value echoed back from the same letters occurring inside an unrelated word.
func isTokenBoundary(s string, start, end int) bool {
	return !isWordByte(s, start-1) && !isWordByte(s, end)
}

func isWordByte(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return false
	}
	c := s[i]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// secretForms lists the encodings of v that could appear in text destined for a
// log: the literal, the two percent-encoded forms, and the JSON-string body
// (without its surrounding quotes).
func secretForms(v string) []string {
	forms := []string{v}
	for _, enc := range []string{url.PathEscape(v), url.QueryEscape(v), jsonStringBody(v)} {
		if enc != v && enc != "" {
			forms = append(forms, enc)
		}
	}
	return forms
}

// jsonStringBody returns v as json.Marshal would render it inside a JSON
// document, minus the surrounding quotes. Returns "" when v needs no escaping,
// so the caller skips a duplicate of the literal form.
func jsonStringBody(v string) string {
	buf, err := json.Marshal(v)
	if err != nil || len(buf) < 2 {
		return ""
	}
	return string(buf[1 : len(buf)-1])
}

// redactBodyForLog renders req's JSON body for a verbose trace or a dry-run
// description with every declared secret masked.
//
// Where the body is a Go value the client marshalled itself, the masking is
// structural: the value is walked and matching leaf strings are replaced *before*
// marshalling, so the mask lands on the value the caller actually passed rather
// than on whatever escaped form it took in the output. A RawBody (multipart) has
// no structure to walk, so it falls back to text substitution over the encoded
// forms. Either way the bytes on the wire are untouched — only the log copy is.
func redactBodyForLog(req *Request, marshalled []byte) string {
	if len(req.SecretValues) == 0 {
		return string(marshalled)
	}
	if req.Body != nil && len(req.RawBody) == 0 {
		if buf, err := json.Marshal(redactBodyValue(req.Body, req.SecretValues)); err == nil {
			return string(buf)
		}
	}
	return redactSecrets(string(marshalled), req.SecretValues)
}

// redactResponseBody masks secrets in a backend error body before it is parsed.
//
// The masking is structural for the same reason the request side is: a textual
// substitution over the encoded bytes is both too broad and too narrow. Too
// broad, because it is unanchored — a one-character or punctuation password
// rewrites the body's own syntax, and ParseBackendError then cannot read the
// backend's `code`, so an agent branching on it silently degrades to the generic
// status code and loses `detail`. Too narrow, because the encoded spelling is
// only guessable for a Go producer: a backend that writes `\/` for a slash
// defeats a substitution built from json.Marshal's spelling.
//
// Parsing first and walking the decoded values fixes both. Object keys are left
// alone — they carry the response contract, never a secret — which is what keeps
// the body parseable. A body that is not JSON has no structure to walk and falls
// back to text substitution.
func redactResponseBody(b []byte, secrets []string) []byte {
	if len(b) == 0 || len(secrets) == 0 {
		return b
	}
	var parsed any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err == nil {
		if buf, merr := json.Marshal(redactBodyValue(parsed, secrets)); merr == nil {
			return buf
		}
	}
	return []byte(redactSecrets(string(b), secrets))
}

// redactBodyValue deep-copies v with every string leaf run through
// redactSecrets. json.Number is a distinct type from string, so a uint64 id is
// copied through untouched and keeps marshalling as a bare JSON integer.
//
// The default branch marshals and text-redacts rather than passing the value
// through: a body shape this function does not walk structurally (a
// map[string]string, say — cmd/service/aliases.go already builds one) would
// otherwise have its secrets silently unmasked, with no test failing. Failing
// safe by construction is worth the marshal on a path that only runs for
// secret-bearing requests.
func redactBodyValue(v any, secrets []string) any {
	switch t := v.(type) {
	case string:
		return redactSecrets(t, secrets)
	case json.Number:
		return t
	case bool, nil, int, int64, float64:
		return v
	case map[string]any:
		out := make(map[string]any, len(t))
		for key, val := range t {
			out[key] = redactBodyValue(val, secrets)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = redactBodyValue(val, secrets)
		}
		return out
	case []string:
		out := make([]string, len(t))
		for i, val := range t {
			out[i] = redactSecrets(val, secrets)
		}
		return out
	}
	// Unknown shape: render it, mask the text, and hand back the masked JSON so
	// the value is never emitted unredacted.
	buf, err := json.Marshal(v)
	if err != nil {
		return secretMask
	}
	return json.RawMessage(redactSecrets(string(buf), secrets))
}

// Client is the REST client. Created via New; invoked by command layer via Do.
//
// Tests should control retry timing by setting Options.NoRetry=true (to
// bypass the retry loop entirely) or by keeping the test context bounded so
// the select in doWithRetry exits via ctx.Done(). There is no test-only
// clock hook on Client; the retry scheduling is intentionally minimal.
type Client struct {
	cfg        *config.Config
	cred       *credential.BotCredential
	httpClient *http.Client
	options    Options
}

// New constructs a Client. Timeout is parsed here; invalid values fall back to
// the default and emit a verbose warning (not a hard error — a bad flag value
// shouldn't fail commands that wouldn't otherwise need HTTP).
func New(cfg *config.Config, cred *credential.BotCredential, opts Options) *Client {
	timeout := defaultTimeout
	if opts.Timeout != "" {
		if d, err := time.ParseDuration(opts.Timeout); err == nil {
			timeout = d
		} else if opts.ErrOut != nil {
			fmt.Fprintf(opts.ErrOut, "warning: invalid --timeout %q; using %s\n", opts.Timeout, defaultTimeout) //nolint:errcheck // stderr warning
		}
	}
	return &Client{
		cfg:  cfg,
		cred: cred,
		httpClient: &http.Client{
			Timeout: timeout,
			// Don't auto-follow — file.download returns 302 to a presigned URL
			// and we want to surface that URL in the envelope, not fetch it.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		options: opts,
	}
}

// Do performs req against the service URL, applying auth, retry, and dry-run.
// Returns the raw response body on 2xx; an *output.ExitError on non-2xx or
// transport failure.
func (c *Client) Do(ctx context.Context, req *Request) ([]byte, error) {
	if req.Service == "" {
		req.Service = "default"
	}

	// Route the message-search family by chat-id scope and token kind
	// (no channel_id → cross-session global; uk_ → /v1/user; app_ → error).
	// Non-search paths pass through unchanged.
	routedPath, err := routeSearchPath(c.cred, req.Path, req.Body)
	if err != nil {
		return nil, err
	}
	req.Path = routedPath

	base := c.cfg.ServiceURL(req.Service)
	if base == "" {
		return nil, output.ErrValidation(
			fmt.Sprintf("no base URL configured for service %q", req.Service),
			fmt.Sprintf("set %s", config.EnvAPIBaseURL),
		)
	}

	u, err := buildURL(base, req.Path, req.Query)
	if err != nil {
		return nil, output.ErrValidation(err.Error(), "check path and query parameters")
	}

	var bodyBytes []byte
	contentType := ""
	if len(req.RawBody) > 0 {
		bodyBytes = req.RawBody
		contentType = req.ContentType
	} else if req.Body != nil {
		bodyBytes, err = json.Marshal(req.Body)
		if err != nil {
			return nil, output.ErrWithHint("internal", "MARSHAL_FAILED", fmt.Sprintf("marshal request body: %v", err), "")
		}
		contentType = "application/json"
	}

	if c.options.DryRun {
		return c.renderDryRun(req, u, bodyBytes)
	}

	return c.doWithRetry(ctx, req, u, bodyBytes, contentType)
}

// doWithRetry runs the HTTP request, retrying transient errors with backoff.
func (c *Client) doWithRetry(ctx context.Context, req *Request, urlStr string, body []byte, contentType string) ([]byte, error) {
	maxRetries := defaultMaxRetries
	if c.options.NoRetry {
		maxRetries = 0
	}

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(attempt)
			// Respect Retry-After if the last response supplied one. It is
			// attached to the *ExitError via Detail? No — we pass it in via
			// lastRetryAfter below.
			if d, ok := extractRetryAfterFromErr(lastErr); ok {
				delay = d
			}
			c.verbosef("retry #%d after %s (last error: %v)", attempt, delay, lastErr)
			select {
			case <-ctx.Done():
				return nil, output.ErrNetwork(ctx.Err().Error(), "request cancelled")
			case <-time.After(delay):
			}
		}

		body, err := c.attempt(ctx, req, urlStr, body, contentType)
		if err == nil {
			return body, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// attempt executes one HTTP round-trip and interprets the response.
func (c *Client) attempt(ctx context.Context, req *Request, urlStr string, body []byte, contentType string) ([]byte, error) { //nolint:gocyclo // HTTP attempt handles auth, headers, dry-run, binary, retries in one flow
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, urlStr, reader)
	if err != nil {
		return nil, output.ErrNetwork(redactSecrets(err.Error(), req.SecretValues), "invalid request")
	}

	if c.cred != nil && c.cred.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !req.SuppressSpaceHeader {
		httpReq.Header.Set("X-Space-Id", c.cred.SpaceID)
	}
	if body != nil && contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}

	c.verbosef("%s %s", req.Method, redactSecrets(urlStr, req.SecretValues))
	if c.options.Verbose && len(body) > 0 {
		c.verbosef("request body: %s", truncate(redactBodyForLog(req, body), 1024))
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// *url.Error.Error() embeds the whole request URL, so an x-octo-secret
		// path parameter (invite / share token) would otherwise reach the default
		// stderr error envelope — no --verbose needed to leak it.
		msg := redactSecrets(err.Error(), req.SecretValues)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, output.ErrNetwork(msg, "request timed out or was cancelled")
		}
		return nil, &retryableErr{
			ExitError: output.ErrNetwork(msg, "transport error; will retry"),
		}
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on HTTP response body

	// The whole response body is buffered here. Every downstream branch needs
	// it fully in memory anyway — JSON parsing, backend-error parsing, and the
	// binary describe envelope (which reports size = len(body)) all consume the
	// complete payload. Board PNG/SVG exports are bounded and small, so a size
	// cap / streaming-to-temp path would add complexity without a real payoff
	// today; deferred until an operation returns genuinely large bodies.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, output.ErrNetwork(fmt.Sprintf("read response: %v", err), "")
	}

	c.verbosef("← %d (%d bytes)", resp.StatusCode, len(respBody))

	// Redirects (3xx) are not followed automatically — surface the Location
	// header as a JSON envelope when the caller opted into binary/redirect
	// handling (file.download). Any other endpoint returning 3xx is treated
	// as an unexpected error.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		if req.BinaryResponse {
			loc := resp.Header.Get("Location")
			env := map[string]any{
				"url":    loc,
				"status": resp.StatusCode,
			}
			if ct := resp.Header.Get("Content-Type"); ct != "" {
				env["content_type"] = ct
			}
			return json.Marshal(env)
		}
		return nil, output.ErrAPI(
			fmt.Sprintf("HTTP_%d", resp.StatusCode),
			fmt.Sprintf("unexpected redirect to %q", redactSecrets(resp.Header.Get("Location"), req.SecretValues)),
			"",
		)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Binary/redirect opt-in: don't try to parse as JSON, just describe.
		if req.BinaryResponse {
			env := map[string]any{
				"status":       resp.StatusCode,
				"content_type": resp.Header.Get("Content-Type"),
				"size":         len(respBody),
			}
			// When the caller asked for a destination, WRITE the bytes to disk
			// (docs.scene.export --output). A write failure is a hard, non-
			// retryable error — the request succeeded, so retrying would only
			// re-download; the problem is local (bad path/permissions).
			//
			// The write is atomic: bytes go to a temp file in the destination
			// directory and are renamed into place only after a fully successful
			// write. A mid-write failure (disk full, I/O error, cancellation)
			// therefore never leaves a truncated/empty file, and never clobbers
			// an existing good copy at outputPath — the rename is all-or-nothing.
			if req.OutputPath != "" {
				if err := writeFileAtomic(req.OutputPath, respBody, 0o644); err != nil {
					return nil, output.ErrValidation(
						fmt.Sprintf("write output %q: %v", req.OutputPath, err),
						"check the path is writable and the directory exists",
					)
				}
				env["output"] = req.OutputPath
			}
			return json.Marshal(env)
		}
		return respBody, nil
	}

	// A backend error body can echo the value it was given, and a not-found
	// message naming the requested id is the most natural thing for a backend to
	// write — which for drive.share.access / drive.share.download /
	// drive.invite.accept means the id *is* the secret. ParseBackendError copies
	// the backend's message into the envelope and the whole body into Detail, so
	// the response path is redacted here as the transport path already is. This is
	// not a --verbose surface: it is the structured error on stderr, emitted
	// unconditionally. The mask contains no quote or backslash, so Detail stays
	// valid JSON.
	ee := output.ParseBackendError(resp.StatusCode, redactResponseBody(respBody, req.SecretValues))

	if isRetryableStatus(resp.StatusCode) {
		re := &retryableErr{ExitError: ee}
		if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
			re.retryAfter = ra
		}
		return nil, re
	}
	return nil, ee
}

// writeFileAtomic writes data to path atomically: it streams the bytes into a
// temp file in the same directory, then renames it over path. Because rename
// within a directory is atomic, path is only ever the old file (untouched) or
// the fully-written new file — never a partial/truncated result, and an
// existing good copy is never clobbered when the write fails midway. On any
// error the temp file is removed so no stray temp files accumulate.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove the temp file unless the rename below hands it off successfully.
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName) //nolint:errcheck // best-effort cleanup
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() //nolint:errcheck // already returning the write error
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close() //nolint:errcheck // already returning the chmod error
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	renamed = true
	return nil
}

// renderDryRun writes a human-readable request description and returns a
// synthetic success body containing the same payload for envelope rendering.
// Spec-declared secrets (share/invite tokens, share passwords) are masked in
// both the URL and the body — a dry run must be safe to paste into a ticket.
func (c *Client) renderDryRun(req *Request, urlStr string, body []byte) ([]byte, error) {
	var bodyField any
	if len(body) > 0 {
		redacted := redactBodyForLog(req, body)
		// UseNumber so a uint64 id in the echoed body is shown at full precision;
		// a plain unmarshal would round it and make --dry-run misreport what the
		// request actually carries.
		dec := json.NewDecoder(strings.NewReader(redacted))
		dec.UseNumber()
		if err := dec.Decode(&bodyField); err != nil {
			bodyField = redacted
		}
	}
	hdr := map[string]string{}
	if c.cred != nil && c.cred.Token != "" {
		hdr["Authorization"] = "Bearer " + credential.MaskToken(c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !req.SuppressSpaceHeader {
		hdr["X-Space-Id"] = c.cred.SpaceID
	}
	for k, v := range req.Headers {
		hdr[k] = redactSecrets(v, req.SecretValues)
	}
	out := map[string]any{
		"dry_run": true,
		"method":  req.Method,
		"url":     redactSecrets(urlStr, req.SecretValues),
		"headers": hdr,
	}
	if bodyField != nil {
		out["body"] = bodyField
	}
	buf, err := json.Marshal(out)
	if err != nil {
		return nil, output.ErrWithHint("internal", "MARSHAL_FAILED", err.Error(), "")
	}
	return buf, nil
}

// --- helpers ---

func (c *Client) verbosef(format string, args ...any) {
	if !c.options.Verbose || c.options.ErrOut == nil {
		return
	}
	fmt.Fprintf(c.options.ErrOut, "[octo] "+format+"\n", args...) //nolint:errcheck // stderr verbose log
}

func buildURL(base, path string, query url.Values) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(base + path)
	if err != nil {
		return "", fmt.Errorf("build url: %w", err)
	}
	if len(query) > 0 {
		q := u.Query()
		for k, vs := range query {
			for _, v := range vs {
				q.Add(k, v)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// --- retry logic ---

// retryableErr wraps an *output.ExitError plus an optional Retry-After.
// Used internally so doWithRetry can distinguish retryable responses from
// terminal ones without re-parsing the exit error.
type retryableErr struct {
	*output.ExitError
	retryAfter time.Duration
}

// Unwrap lets errors.As reach the embedded *ExitError so callers (e.g.
// output.AsExitError) can still get structured info after retries exhaust.
func (r *retryableErr) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.ExitError
}

func isRetryable(err error) bool {
	var re *retryableErr
	return errors.As(err, &re)
}

func extractRetryAfterFromErr(err error) (time.Duration, bool) {
	var re *retryableErr
	if !errors.As(err, &re) {
		return 0, false
	}
	if re.retryAfter > 0 {
		return re.retryAfter, true
	}
	return 0, false
}

func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	}
	return false
}

// backoffDelay computes exponential backoff with full jitter. attempt is 1-based.
// Retry-After is NOT capped by maxDelay (per design §6.2); that handling lives
// in doWithRetry.
func backoffDelay(attempt int) time.Duration {
	// Guard against overflow: if the shift would exceed maxDelay, clamp early.
	// With defaultBaseDelay=500ms the shift overflows time.Duration around
	// attempt=34, but maxRetries=3 makes this a defensive check.
	shift := attempt - 1
	if shift < 0 {
		shift = 0
	}
	exp := defaultBaseDelay
	if shift < 63 {
		exp = defaultBaseDelay << shift // 500ms, 1s, 2s, 4s, ...
	}
	if exp <= 0 || exp > defaultMaxDelay {
		exp = defaultMaxDelay
	}
	jitter := jitterFraction() * float64(exp)
	return time.Duration(jitter)
}

// jitterFraction returns a value in [0.5, 1.0) to avoid thundering-herd
// collapse while still making meaningful progress. Uses crypto/rand — the
// calls are rare enough (≤ 3 retries) that cost is negligible.
func jitterFraction() float64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0.75 // deterministic fallback
	}
	u := binary.BigEndian.Uint64(b[:])
	f := float64(u>>11) / (1 << 53) // uniform [0,1)
	return 0.5 + 0.5*f
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
