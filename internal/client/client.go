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
		return c.renderDryRun(req.Method, u, req.Headers, bodyBytes, req.SuppressSpaceHeader)
	}

	return c.doWithRetry(ctx, req.Method, u, req.Headers, bodyBytes, contentType, req.BinaryResponse, req.OutputPath, req.SuppressSpaceHeader)
}

// doWithRetry runs the HTTP request, retrying transient errors with backoff.
func (c *Client) doWithRetry(ctx context.Context, method, urlStr string, headers map[string]string, body []byte, contentType string, binaryResp bool, outputPath string, suppressSpaceHeader bool) ([]byte, error) {
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

		body, err := c.attempt(ctx, method, urlStr, headers, body, contentType, binaryResp, outputPath, suppressSpaceHeader)
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
func (c *Client) attempt(ctx context.Context, method, urlStr string, headers map[string]string, body []byte, contentType string, binaryResp bool, outputPath string, suppressSpaceHeader bool) ([]byte, error) { //nolint:gocyclo // HTTP attempt handles auth, headers, dry-run, binary, retries in one flow
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, urlStr, reader)
	if err != nil {
		return nil, output.ErrNetwork(err.Error(), "invalid request")
	}

	if c.cred != nil && c.cred.Token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !suppressSpaceHeader {
		httpReq.Header.Set("X-Space-Id", c.cred.SpaceID)
	}
	if body != nil && contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		httpReq.Header.Set(k, v)
	}

	c.verbosef("%s %s", method, urlStr)
	if c.options.Verbose && len(body) > 0 {
		c.verbosef("request body: %s", truncate(string(body), 1024))
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, output.ErrNetwork(err.Error(), "request timed out or was cancelled")
		}
		return nil, &retryableErr{
			ExitError: output.ErrNetwork(err.Error(), "transport error; will retry"),
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
		if binaryResp {
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
			fmt.Sprintf("unexpected redirect to %q", resp.Header.Get("Location")),
			"",
		)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// Binary/redirect opt-in: don't try to parse as JSON, just describe.
		if binaryResp {
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
			if outputPath != "" {
				if err := writeFileAtomic(outputPath, respBody, 0o644); err != nil {
					return nil, output.ErrValidation(
						fmt.Sprintf("write output %q: %v", outputPath, err),
						"check the path is writable and the directory exists",
					)
				}
				env["output"] = outputPath
			}
			return json.Marshal(env)
		}
		return respBody, nil
	}

	ee := output.ParseBackendError(resp.StatusCode, respBody)

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
func (c *Client) renderDryRun(method, urlStr string, headers map[string]string, body []byte, suppressSpaceHeader bool) ([]byte, error) {
	var bodyField any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &bodyField); err != nil {
			bodyField = string(body)
		}
	}
	hdr := map[string]string{}
	if c.cred != nil && c.cred.Token != "" {
		hdr["Authorization"] = "Bearer " + credential.MaskToken(c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" && !suppressSpaceHeader {
		hdr["X-Space-Id"] = c.cred.SpaceID
	}
	for k, v := range headers {
		hdr[k] = v
	}
	out := map[string]any{
		"dry_run": true,
		"method":  method,
		"url":     urlStr,
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
