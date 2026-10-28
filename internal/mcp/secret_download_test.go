package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func isolatedSecretDownloads(t *testing.T) *SecretDownloads {
	t.Helper()
	path, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("isolated Redis requires redis-server")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	_, port, _ := net.SplitHostPort(addr)
	cmd := exec.Command(path, "--bind", "127.0.0.1", "--port", port, "--save", "", "--appendonly", "no", "--dir", t.TempDir())
	if cmd.Start() != nil {
		t.Fatal("start isolated Redis")
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	d := newSecretDownloads(func(k string) string {
		switch k {
		case "OCTO_MCP_SECRET_REDIS_URL":
			return "redis://" + addr
		case "OCTO_MCP_SECRET_PUBLIC_ORIGIN":
			return "https://downloads.example"
		}
		return ""
	})
	if d == nil {
		t.Fatal("fixture config rejected")
	}
	t.Cleanup(d.Close)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.redis.Ping(t.Context()).Err() == nil {
			return d
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("isolated Redis not ready")
	return nil
}

func TestSecretConfig(t *testing.T) {
	for _, c := range []struct {
		key, value string
		ok         bool
	}{
		{"", "", true}, {"OCTO_MCP_SECRET_TTL", "601s", true}, {"OCTO_MCP_SECRET_TTL", "9223372036854775807ns", true},
		{"OCTO_MCP_SECRET_TTL", "0s", false}, {"OCTO_MCP_SECRET_TTL", "-1s", false}, {"OCTO_MCP_SECRET_TTL", "1ns", false}, {"OCTO_MCP_SECRET_TTL", "9223372036854775808ns", false},
		{"OCTO_MCP_SECRET_PUBLIC_ORIGIN", "http://public.example", false}, {"OCTO_MCP_SECRET_PUBLIC_ORIGIN", "https://user:pass@example.org", false}, {"OCTO_MCP_SECRET_PUBLIC_ORIGIN", "https://example.org/path", false},
		{"OCTO_MCP_SECRET_PUBLIC_ORIGIN", "https://example.org?token=x", false}, {"OCTO_MCP_SECRET_PUBLIC_ORIGIN", "http://127.0.0.1:1234", true},
		{"OCTO_MCP_SECRET_REDIS_URL", "rediss://user:password@example.org:6380/2", true}, {"OCTO_MCP_SECRET_REDIS_URL", "redis://example.org?skip_verify=true", false},
		{"OCTO_MCP_SECRET_REDIS_URL", "redis://example.org/-1", false}, {"OCTO_MCP_SECRET_REDIS_PREFIX", "*", false},
	} {
		t.Run(c.key+"/"+c.value, func(t *testing.T) {
			env := map[string]string{"OCTO_MCP_SECRET_REDIS_URL": "redis://127.0.0.1:1", "OCTO_MCP_SECRET_PUBLIC_ORIGIN": "https://downloads.example"}
			if c.key != "" {
				env[c.key] = c.value
			}
			d := newSecretDownloads(func(k string) string { return env[k] })
			if (d != nil) != c.ok {
				t.Fatal("config acceptance mismatch")
			}
			defer d.Close()
			if d != nil && c.key == "" && d.ttl != 300*time.Second {
				t.Fatal("default TTL changed")
			}
			if d != nil && strings.HasPrefix(env["OCTO_MCP_SECRET_REDIS_URL"], "rediss:") {
				cfg := d.redis.Options().TLSConfig
				if cfg == nil || cfg.InsecureSkipVerify {
					t.Fatal("TLS verification disabled")
				}
			}
		})
	}
}

func TestSecretStrictInputsAndDryRun(t *testing.T) {
	s := newTestServer(t)
	for _, raw := range []string{`{}`, `{"query":null}`, `{"query":"x","dry_run":null}`, `{"Query":"x"}`, `{"query":"x","ttl":1}`, `{"query":"x","dry_run":true,"dry_run":false}`, `{"query":"x","dry_run":"true"}`, `{"query":"x","\u0064ry_run":true,"dry_run":false}`, `{"query":"x","host":"evil"}`, `{"query":"x","path":"/x"}`, `{"query":"` + strings.Repeat("x", 129) + `"}`} {
		if text := s.secretDownloadTool(t.Context(), json.RawMessage(raw)).Content[0].Text; !strings.Contains(text, "validation_error") {
			t.Fatal("malformed input accepted")
		}
	}
	for _, global := range []bool{false, true} {
		s.baseGlobals.DryRun = global
		raw := `{"query":"alias","dry_run":true}`
		if global {
			raw = `{"query":"alias"}`
		}
		if r := s.secretDownloadTool(t.Context(), json.RawMessage(raw)); r.IsError || !strings.Contains(r.Content[0].Text, `"status": "dry_run"`) {
			t.Fatal("dry run needs config/credentials")
		}
	}
	s.baseGlobals.DryRun = false
	if !strings.Contains(s.secretDownloadTool(t.Context(), json.RawMessage(`{"query":"alias"}`)).Content[0].Text, "unavailable") {
		t.Fatal("unconfigured feature not closed")
	}
}

func TestSecretFailuresAndBounds(t *testing.T) {
	d := isolatedSecretDownloads(t)
	var hits atomic.Int32
	var mode atomic.Int32
	const canary = "private-upstream-canary"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch mode.Load() {
		case 1:
			w.WriteHeader(500)
			_, _ = io.WriteString(w, canary)
		case 2:
			w.Header().Set("Location", "https://invalid.example/"+canary)
			w.WriteHeader(302)
		case 3:
			_, _ = io.WriteString(w, strings.Repeat("x", maxResolveBytes+1))
		case 4:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"value": strings.Repeat("x", maxSecretBytes+1)}})
		case 5:
			_, _ = io.WriteString(w, `{"data":{"value":null}}`)
		case 6:
			_, _ = io.WriteString(w, `{"data":{"value":""}}`)
		case 7:
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"error":{"details":{"candidates":[]}}}`)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"value": canary}})
		}
	}))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	s := newTestServer(t)
	s.httpMode = true
	s.credentialToken = "bf_fixture"
	s.secretDownloads = d
	for _, m := range []int32{1, 2, 3, 4, 5, 6, 7} {
		mode.Store(m)
		r := s.secretDownloadTool(t.Context(), json.RawMessage(`{"query":"alias"}`))
		if !r.IsError || strings.Contains(r.Content[0].Text, canary) || strings.Contains(r.Content[0].Text, "download_url") {
			t.Fatal("unsafe upstream failure")
		}
	}
	mode.Store(0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := hits.Load()
	r := s.secretDownloadTool(ctx, json.RawMessage(`{"query":"alias"}`))
	if !r.IsError || hits.Load() != before {
		t.Fatal("cancelled issuance sent work")
	}
	// No ambient credential fallback and no unsupported bearer request.
	for _, token := range []string{"", "uk_no", "octo_loop_no"} {
		s.credentialToken = token
		before = hits.Load()
		r = s.secretDownloadTool(t.Context(), json.RawMessage(`{"query":"alias"}`))
		if !strings.Contains(r.Content[0].Text, "auth_error") || hits.Load() != before {
			t.Fatal("credential fallback")
		}
	}
	s.credentialToken = "bf_fixture"
	for range cap(d.slots) {
		d.slots <- struct{}{}
	}
	before = hits.Load()
	r = s.secretDownloadTool(t.Context(), json.RawMessage(`{"query":"alias"}`))
	if !strings.Contains(r.Content[0].Text, "busy") || hits.Load() != before {
		t.Fatal("concurrency not bounded")
	}
	for range cap(d.slots) {
		<-d.slots
	}
	_ = d.redis.Close()
	r = s.secretDownloadTool(t.Context(), json.RawMessage(`{"query":"alias"}`))
	if !r.IsError || strings.Contains(r.Content[0].Text, canary) {
		t.Fatal("Redis failure not sanitized")
	}
}

func TestSecretRedisLifecycle(t *testing.T) {
	d := isolatedSecretDownloads(t)
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	hash := sha256.Sum256([]byte(token))
	key := d.prefix + hex.EncodeToString(hash[:])
	canary := []byte("private-exact\x00\n")
	expires, err := d.redis.Eval(t.Context(), secretIssueScript, []string{key}, base64.StdEncoding.EncodeToString(canary), 60000).Int64()
	if err != nil || expires <= 0 {
		t.Fatal("atomic issuance failed")
	}
	if ttl := d.redis.PTTL(t.Context(), key).Val(); ttl <= 0 || ttl > time.Minute {
		t.Fatal("record lacks bounded TTL")
	}
	keys := d.redis.Keys(t.Context(), d.prefix+"*").Val()
	if len(keys) != 1 || strings.Contains(keys[0], token) {
		t.Fatal("capability not hashed")
	}
	if raw := d.redis.Get(t.Context(), key).Val(); strings.Contains(raw, "bf_") || strings.Contains(raw, token) {
		t.Fatal("record contains credential")
	}
	get := func(headers map[string]string, method string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://hostile.invalid"+secretDownloadPath+token, http.NoBody)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		d.serveDownload(w, r)
		return w
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := get(nil, "GET")
			if w.Code != 200 {
				t.Error("unexpected concurrency outcome")
			}
			if w.Code == 200 && !bytes.Equal(w.Body.Bytes(), canary) {
				t.Error("exact bytes differ")
			}
		}()
	}
	wg.Wait()
	for _, h := range []map[string]string{{"Range": "bytes=0-1"}, {"Purpose": "prefetch"}, {"Sec-Purpose": "prefetch"}, {"X-Purpose": "preview"}} {
		if get(h, "GET").Code != 404 {
			t.Fatal("range/prefetch not refused")
		}
	}
	if w := get(nil, "HEAD"); w.Code != 405 || w.Body.Len() != 0 {
		t.Fatal("HEAD must not fetch bytes")
	}
	if w := get(map[string]string{"Authorization": "Bearer unused", "Host": "evil", "Forwarded": "host=evil"}, "GET"); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), canary) {
		t.Fatal("capability download depends on host/bearer")
	}
	// An aborted client does not consume the reusable grant.
	r := httptest.NewRequest("GET", secretDownloadPath+token, http.NoBody)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	d.serveDownload(httptest.NewRecorder(), r.WithContext(ctx))
	if get(nil, "GET").Code != 200 {
		t.Fatal("abort consumed grant")
	}
	// Redis wall time + positive physical TTL cannot admit an expired record.
	if err = d.redis.Set(t.Context(), key, `{"expires":1,"payload":"cHJpdmF0ZQ=="}`, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if get(nil, "GET").Code != 404 {
		t.Fatal("logical expiry relied on physical eviction")
	}
	for _, bad := range []string{`not-json`, strings.Repeat("x", maxSecretRecordBytes+1)} {
		if d.redis.Set(t.Context(), key, bad, time.Minute).Err() != nil {
			t.Fatal("fixture write")
		}
		if get(nil, "GET").Code != 404 {
			t.Fatal("invalid/oversized Redis record admitted")
		}
	}
	_ = d.redis.Del(t.Context(), key).Err()
	if get(nil, "GET").Code != 404 {
		t.Fatal("evicted record admitted")
	}
	_, err = d.redis.Eval(t.Context(), secretIssueScript, []string{key}, base64.StdEncoding.EncodeToString(canary), 20).Int64()
	if err != nil {
		t.Fatal("short TTL issuance")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && d.redis.Exists(t.Context(), key).Val() != 0 {
		time.Sleep(time.Millisecond)
	}
	if d.redis.Exists(t.Context(), key).Val() != 0 || get(nil, "GET").Code != 404 {
		t.Fatal("Redis did not expire record")
	}
	_ = d.redis.Set(t.Context(), key, `{"expires":9999999999999,"payload":"cHJpdmF0ZQ=="}`, 0).Err()
	if get(nil, "GET").Code != 404 {
		t.Fatal("immortal record admitted")
	}
}

// Keep the exact comparison boundary deterministic without changing the Redis
// clock or exposing a client-controlled expiry. Only the script's TIME call is
// replaced in this test; the production admission expression is unchanged.
func TestSecretExpiryBoundary(t *testing.T) {
	d := isolatedSecretDownloads(t)
	key := d.prefix + "boundary"
	for _, c := range []struct {
		now     int
		allowed bool
	}{{999, true}, {1000, false}, {1001, false}} {
		_ = d.redis.Set(t.Context(), key, `{"expires":1000,"payload":"c2FmZQ=="}`, time.Minute).Err()
		script := strings.Replace(secretReadScript, `local now = redis.call('TIME')`, `local now = {0, `+strconvForTest(c.now*1000)+`}`, 1)
		_, err := d.redis.Eval(t.Context(), script, []string{key}, maxSecretRecordBytes).Text()
		if (err == nil) != c.allowed || (err != nil && err != redis.Nil) {
			t.Fatal("before/at/after expiry admission mismatch")
		}
	}
}

func strconvForTest(n int) string { b, _ := json.Marshal(n); return string(b) }
