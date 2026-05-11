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
	"strconv"
	"strings"
	"time"

	"github.com/dmwork-org/octo-cli/internal/config"
	"github.com/dmwork-org/octo-cli/internal/credential"
	"github.com/dmwork-org/octo-cli/internal/output"
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
			fmt.Fprintf(opts.ErrOut, "warning: invalid --timeout %q; using %s\n", opts.Timeout, defaultTimeout)
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
func (c *Client) Do(ctx context.Context, req Request) ([]byte, error) {
	if req.Service == "" {
		req.Service = "default"
	}
	base := c.cfg.ServiceURL(req.Service)
	if base == "" {
		return nil, output.ErrValidation(
			fmt.Sprintf("no base URL configured for service %q", req.Service),
			fmt.Sprintf("set %s or OCTO_API_URL", serviceEnvHint(req.Service)),
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
		return c.renderDryRun(req.Method, u, req.Headers, bodyBytes)
	}

	return c.doWithRetry(ctx, req.Method, u, req.Headers, bodyBytes, contentType, req.BinaryResponse)
}

// doWithRetry runs the HTTP request, retrying transient errors with backoff.
func (c *Client) doWithRetry(ctx context.Context, method, urlStr string, headers map[string]string, body []byte, contentType string, binary bool) ([]byte, error) {
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

		body, err := c.attempt(ctx, method, urlStr, headers, body, contentType, binary)
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
func (c *Client) attempt(ctx context.Context, method, urlStr string, headers map[string]string, body []byte, contentType string, binary bool) ([]byte, error) {
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
	if c.cred != nil && c.cred.SpaceID != "" {
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
	defer resp.Body.Close()

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
		if binary {
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
		if binary {
			env := map[string]any{
				"status":       resp.StatusCode,
				"content_type": resp.Header.Get("Content-Type"),
				"size":         len(respBody),
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

// renderDryRun writes a human-readable request description and returns a
// synthetic success body containing the same payload for envelope rendering.
func (c *Client) renderDryRun(method, urlStr string, headers map[string]string, body []byte) ([]byte, error) {
	var bodyField any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &bodyField); err != nil {
			bodyField = string(body)
		}
	}
	hdr := map[string]string{}
	if c.cred != nil && c.cred.Token != "" {
		hdr["Authorization"] = "Bearer " + redactToken(c.cred.Token)
	}
	if c.cred != nil && c.cred.SpaceID != "" {
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
	fmt.Fprintf(c.options.ErrOut, "[octo] "+format+"\n", args...)
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

func serviceEnvHint(service string) string {
	switch service {
	case "matters":
		return config.EnvMattersURL
	case "dmworkim":
		return config.EnvDmworkIMURL
	}
	return config.EnvAPIURL
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func redactToken(t string) string {
	if len(t) <= 8 {
		return "****"
	}
	return t[:4] + "****" + t[len(t)-4:]
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
