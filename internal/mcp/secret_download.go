package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
)

const secretDownloadPath = "/mcp/secret-download/"
const maxSecretBytes = 8192
const maxResolveBytes = 64 << 10
const maxSecretRecordBytes = 16 << 10
const secretWorkTimeout = 10 * time.Second

// SecretDownloads is shared by HTTP connection servers. It deliberately has
// no logging/tracing hooks: resolver bodies and Redis command args are secret.
// A nil/invalid configuration disables only this feature, without a connection.
type SecretDownloads struct {
	redis  *redis.Client
	origin string
	prefix string
	ttl    time.Duration
	slots  chan struct{}
	client *http.Client
}

var secretPrefixPattern = regexp.MustCompile(`^[a-zA-Z0-9:_-]{1,128}$`)
var secretTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// SecretDownloadsFromEnv makes no network requests. Stdio publishers and the
// HTTP download service use the same explicit origin, Redis and private prefix.
// Bad/partial config fails closed, leaving all existing MCP tools available.
func SecretDownloadsFromEnv() *SecretDownloads {
	return newSecretDownloads(os.Getenv)
}

func newSecretDownloads(getenv func(string) string) *SecretDownloads {
	dsn := getenv("OCTO_MCP_SECRET_REDIS_URL")
	origin := getenv("OCTO_MCP_SECRET_PUBLIC_ORIGIN")
	if dsn == "" || !validSecretOrigin(origin) {
		return nil
	}
	opts := secretRedisOptions(dsn)
	if opts == nil {
		return nil
	}
	var err error
	prefix := getenv("OCTO_MCP_SECRET_REDIS_PREFIX")
	if prefix == "" {
		prefix = "octo:mcp:secret-download:"
	}
	if !secretPrefixPattern.MatchString(prefix) {
		return nil
	}
	ttl := 300 * time.Second
	if raw := getenv("OCTO_MCP_SECRET_TTL"); raw != "" {
		ttl, err = time.ParseDuration(raw)
		if err != nil || ttl < time.Millisecond {
			return nil
		}
	}
	// Redis PX uses integral milliseconds; never overstate lifetime in metadata.
	ttl = ttl.Truncate(time.Millisecond)
	opts.MaxRetries = -1
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 2 * time.Second
	opts.WriteTimeout = 2 * time.Second
	opts.ContextTimeoutEnabled = true
	opts.PoolSize = 16
	opts.MaxActiveConns = 16
	opts.PoolTimeout = 2 * time.Second
	opts.Protocol = 2
	if opts.TLSConfig != nil {
		opts.TLSConfig.MinVersion = tls.VersionTLS12
	}
	return &SecretDownloads{redis: redis.NewClient(opts), origin: strings.TrimSuffix(origin, "/"), prefix: prefix, ttl: ttl, slots: make(chan struct{}, 16), client: &http.Client{
		Timeout:       secretWorkTimeout,
		Transport:     &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxConnsPerHost: 16, MaxResponseHeaderBytes: 16 << 10, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func secretRedisOptions(dsn string) *redis.Options {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" {
		return nil
	}
	opts, err := redis.ParseURL(dsn)
	if err != nil || opts.DB < 0 {
		return nil
	}
	return opts
}

func validSecretOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || u.Opaque != "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

// Close releases the feature's pool; it never includes errors in diagnostics.
func (d *SecretDownloads) Close() {
	if d != nil {
		_ = d.redis.Close() //nolint:errcheck // shutdown errors must not expose Redis diagnostics
		d.client.CloseIdleConnections()
	}
}

func (d *SecretDownloads) acquire() bool {
	select {
	case d.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func secretToolDefinition() toolDef {
	return toolDef{Name: "get_secret_download_url", Description: "Resolve an Octo stored-secret alias or ID using the actual caller's bot credential and return a temporary reusable download URL, never the secret value. Bot creator owns the secrets; chat actor/space does not grant access. Ambiguous aliases require explicit user selection then an ID retry. Download directly to a private CLIENT file with an ordinary downloader; never use text-extraction fetch or resources/read to display the key; never read/open/cat the file into model context. Scripts/code may read internally but must never echo or send secrets to tracing, stdout, stderr or error output. The URL is a sensitive temporary credential: do not display or persist it publicly. Host history can retain it; these instructions are not hard isolation. Snapshot at issuance: later rotation/deletion does not revoke it. See octo-shared/secret-download.md for private-file/no-overwrite/partial-cleanup guidance.", InputSchema: rawSchema(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string","minLength":1,"maxLength":128,"description":"stored alias or stable ID (at most 512 UTF-8 bytes)"},"dry_run":{"type":"boolean","default":false}}}`)}
}

func secretFailure(status string) toolResult {
	return jsonToolResult(map[string]any{"status": status}, true)
}

type secretToolArgs struct {
	Query  string `json:"query"`
	DryRun bool   `json:"dry_run"`
}

func parseSecretToolArgs(raw json.RawMessage) (secretToolArgs, bool) {
	var a secretToolArgs
	// Fixed validation errors never echo caller data or decoder diagnostics.
	if len(raw) > 2048 || !isJSONObject(raw) || validateClosedKeys(raw, map[string]bool{"query": true, "dry_run": true}) != nil {
		return a, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return a, false
	}
	for _, v := range fields {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return a, false
		}
	}
	if json.Unmarshal(raw, &a) != nil || strings.TrimSpace(a.Query) == "" || !utf8.ValidString(a.Query) || len(a.Query) > 512 || utf8.RuneCountInString(a.Query) > 128 {
		return a, false
	}
	return a, true
}

func (s *Server) secretDownloadTool(ctx context.Context, raw json.RawMessage) toolResult {
	if s.trusted.OnBehalfOf != "" {
		return jsonToolResult(map[string]string{"error": "OBO_FORBIDDEN"}, true)
	}
	a, ok := parseSecretToolArgs(raw)
	if !ok {
		return secretFailure("validation_error")
	}
	if a.DryRun || s.baseGlobals.DryRun {
		return jsonToolResult(map[string]any{"status": "dry_run", "dry_run": true}, false)
	}
	d := s.secretDownloads
	if d == nil {
		return secretFailure("unavailable")
	}
	if !d.acquire() {
		return secretFailure("busy")
	}
	defer func() { <-d.slots }()
	ctx, cancel := context.WithTimeout(ctx, secretWorkTimeout)
	defer cancel()
	payload, failure := s.resolveSecret(ctx, d, a.Query)
	if payload == nil {
		return failure
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return secretFailure("unavailable")
	}
	token := base64.RawURLEncoding.EncodeToString(nonce[:])
	hash := sha256.Sum256([]byte(token))
	key := d.prefix + hex.EncodeToString(hash[:])
	expires, err := d.redis.Eval(ctx, secretIssueScript, []string{key}, base64.StdEncoding.EncodeToString(payload), d.ttl.Milliseconds()).Int64()
	if err != nil || expires <= 0 {
		return secretFailure("unavailable")
	}
	return jsonToolResult(map[string]any{"status": "ok", "download_url": d.origin + secretDownloadPath + token, "expires_at": time.UnixMilli(expires).UTC().Format(time.RFC3339Nano), "expires_in_seconds": d.ttl.Seconds()}, false)
}

func (s *Server) resolveSecret(ctx context.Context, d *SecretDownloads, query string) ([]byte, toolResult) {
	// Resolve the same ordinary stdio credential selectors, with HTTP's pinned
	// bearer override. No shared client or verbose engine output is involved.
	fn := s.factoryFn
	if fn == nil {
		fn = s.makeFactory
	}
	f, _, _ := fn(s.trusted)
	cred, err := f.CredentialFunc()
	if err != nil || cred == nil || !strings.HasPrefix(cred.Token, "bf_") {
		return nil, secretFailure("auth_error")
	}
	cfg, err := f.ConfigFunc()
	if err != nil || cfg == nil {
		return nil, secretFailure("unavailable")
	}
	base, err := url.Parse(cfg.APIBaseURL)
	if err != nil || !validSecretOrigin(cfg.APIBaseURL) {
		return nil, secretFailure("unavailable")
	}
	base.Path = "/v1/bot/secrets/resolve"
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, secretFailure("validation_error")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, secretFailure("unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, secretFailure("unavailable")
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // bodies are read with strict bounds; close errors never become diagnostics
	return readSecretResponse(resp)
}

func readSecretResponse(resp *http.Response) ([]byte, toolResult) {
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return nil, secretFailure("auth_error")
	}
	if resp.StatusCode == 404 {
		return nil, secretFailure("not_found")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 422 {
		return nil, secretFailure("unavailable")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResolveBytes+1))
	if err != nil || len(data) > maxResolveBytes {
		return nil, secretFailure("unavailable")
	}
	if resp.StatusCode == 422 {
		return nil, secretCandidates(data)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, secretFailure("unavailable")
	}
	if len(envelope.Data) > 0 {
		data = envelope.Data
	}
	var resolved struct {
		Value *string `json:"value"`
	}
	if json.Unmarshal(data, &resolved) != nil || resolved.Value == nil || *resolved.Value == "" || len(*resolved.Value) > maxSecretBytes {
		return nil, secretFailure("unavailable")
	}
	return []byte(*resolved.Value), toolResult{}
}

func secretCandidates(raw []byte) toolResult {
	var envelope struct {
		Error struct {
			Details struct {
				Candidates []struct {
					ID   string `json:"secret_id"`
					Name string `json:"display_name"`
				} `json:"candidates"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return secretFailure("unavailable")
	}
	candidates := envelope.Error.Details.Candidates
	if len(candidates) == 0 || len(candidates) > 20 {
		return secretFailure("unavailable")
	}
	for _, c := range candidates {
		if c.ID == "" || len(c.ID) > 128 || len(c.Name) > 512 {
			return secretFailure("unavailable")
		}
	}
	return jsonToolResult(map[string]any{"status": "ambiguous", "candidates": candidates, "hint": "ask the user to choose explicitly, then retry query with the selected secret_id"}, true)
}

// Redis time is authoritative across replicas. Record + TTL are one atomic SET;
// no plaintext value is ever stored without an expiry, even if issuance aborts.
const secretIssueScript = `
local now = redis.call('TIME')
local expires = now[1]*1000 + math.floor(now[2]/1000) + tonumber(ARGV[2])
local value = cjson.encode({expires=expires, payload=ARGV[1]})
if redis.call('SET', KEYS[1], value, 'PX', ARGV[2], 'NX') then return expires end
return 0`

// Admission checks logical expiry separately from Redis physical reclamation.
// STRLEN caps a corrupt/unexpected record before moving it to the application.
const secretReadScript = `
local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 or redis.call('STRLEN', KEYS[1]) > tonumber(ARGV[1]) then return false end
local value = redis.call('GET', KEYS[1])
if not value then return false end
local record = cjson.decode(value)
local now = redis.call('TIME')
if now[1]*1000 + math.floor(now[2]/1000) >= record.expires then return false end
return record.payload`

func (d *SecretDownloads) serveDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	deny := func() { http.Error(w, "download unavailable", http.StatusNotFound) }
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if d == nil || !d.acquire() {
		deny()
		return
	}
	defer func() { <-d.slots }()
	token, ok := secretDownloadToken(r)
	if !ok {
		deny()
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), secretWorkTimeout)
	defer cancel()
	hash := sha256.Sum256([]byte(token))
	encoded, err := d.redis.Eval(ctx, secretReadScript, []string{d.prefix + hex.EncodeToString(hash[:])}, maxSecretRecordBytes).Text()
	if err != nil || len(encoded) > base64.StdEncoding.EncodedLen(maxSecretBytes) {
		deny()
		return
	}
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(payload) == 0 || len(payload) > maxSecretBytes {
		deny()
		return
	}
	// A client admitted before expiry may finish within this bounded write.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(secretWorkTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		deny()
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="secret.bin"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
	_, _ = w.Write(payload) //nolint:errcheck // disconnects end the transfer; never log bytes, URL or write errors
}

func secretDownloadToken(r *http.Request) (string, bool) {
	token := strings.TrimPrefix(r.URL.Path, secretDownloadPath)
	if !secretTokenPattern.MatchString(token) || r.URL.RawQuery != "" || r.Header.Get("Range") != "" || r.Header.Get("Purpose") != "" || r.Header.Get("Sec-Purpose") != "" || r.Header.Get("X-Purpose") != "" {
		return "", false
	}
	return token, true
}
