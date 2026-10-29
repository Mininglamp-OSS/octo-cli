package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordedRequest is one request the isolated mock backend received.
type recordedRequest struct {
	Method, Path, Query, Auth, ContentType, SpaceHeader, MediaType string
	Body                                                           string
}

// xin2424Backend is an isolated in-process mock of the Octo gateway. It answers
// the XIN-2424 operations the built-binary test calls and records every
// request; nothing leaves the loopback interface.
func xin2424Backend(t *testing.T) (srv *httptest.Server, requests func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, recordedRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"),
			ContentType: r.Header.Get("Content-Type"), SpaceHeader: r.Header.Get("X-Space-Id"),
			MediaType: r.Header.Get("X-Media-Type"), Body: string(raw),
		})
		mu.Unlock()
		switch {
		case r.URL.Path == "/v1/bot/card/profile":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"card_version":2,"features":["progress"]}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/bot/groups/"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"bot token revoked"}`))
		case strings.Contains(r.URL.Path, "/ppt/media/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
		case strings.HasSuffix(r.URL.Path, "/ppt/media"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"data":{"ref":"ppt-media:a1","attachId":"a1","mime":"image/png","sizeBytes":4}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

func toolCall(id int, name string, args map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args}})
	return string(raw)
}

// TestBuiltBinary_XIN2424GetSkillToExecute drives the real binary over stdio
// and HTTP: get_skill search/describe, then execute against the isolated mock
// backend — success, auth failure, malformed and unknown arguments, forced
// context, schema drift, confined raw-body upload, inline binary output, the
// drive share generated leaves, and a no-network dry-run.
func TestBuiltBinary_XIN2424GetSkillToExecute(t *testing.T) {
	bin := buildOctoCLI(t)
	root := t.TempDir()
	inside := filepath.Join(root, "pic.png")
	if err := os.WriteFile(inside, []byte("PNG!"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.svg")
	if err := os.WriteFile(outside, []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	type step struct {
		request string
		want    []string // substrings the tool result text must contain
		reject  []string // substrings it must not contain
	}
	steps := []step{
		{toolCall(1, "get_skill", map[string]any{"intent": "search", "domain": "bot", "query": "card"}), []string{"bot.card-profile"}, nil},
		{toolCall(2, "get_skill", map[string]any{"intent": "describe", "operation_id": "thread.delete"}), []string{`"schema_fingerprint"`, "high-risk-write"}, nil},
		{toolCall(3, "execute", map[string]any{"operation_id": "bot.card-profile"}), []string{`"status": "ok"`, `"card_version": 2`}, nil},
		{toolCall(4, "execute", map[string]any{"operation_id": "thread.delete", "arguments": map[string]any{"group_no": "g1", "short_id": "s1"}}), []string{`"status": "auth_error"`, `"retryable": false`}, nil},
		{toolCall(5, "execute", map[string]any{"operation_id": "bot.resolve-targets", "arguments": map[string]any{"name": "ops", "kind": "everything"}}), []string{`"status": "validation_error"`}, nil},
		{toolCall(6, "execute", map[string]any{"operation_id": "group.mention-pref", "arguments": map[string]any{"group_no": "g1", "bogus": 1}}), []string{`"status": "validation_error"`, "bogus"}, nil},
		{toolCall(7, "execute", map[string]any{"operation_id": "message.card-revisions-clear", "arguments": map[string]any{"channel_id": "model-channel", "channel_type": 2, "message_id": "m1"}}), []string{`"status": "ok"`, "_forced_arguments"}, nil},
		{toolCall(8, "execute", map[string]any{"operation_id": "bot.card-profile", "schema_fingerprint": "stale"}), []string{`"status": "schema_drift"`}, nil},
		{toolCall(9, "execute", map[string]any{"operation_id": "docs.ppt.media.get", "arguments": map[string]any{"docId": "d1", "attachId": "a1"}}), []string{`"content_base64": "iVBORw=="`, `"inline": true`}, nil},
		{toolCall(10, "execute", map[string]any{"operation_id": "docs.ppt.media.upload", "arguments": map[string]any{"docId": "d1", "X-Media-Type": "image/png", "file_path": inside}}), []string{`"status": "ok"`, "ppt-media:a1"}, nil},
		{toolCall(11, "execute", map[string]any{"operation_id": "docs.attachments.svg-upload", "arguments": map[string]any{"docId": "d1", "file_path": outside}}), []string{`"status": "validation_error"`}, nil},
		{toolCall(12, "execute", map[string]any{"operation_id": "drive.share.access", "arguments": map[string]any{"share_token": "tok-secret"}}), []string{`"status": "ok"`}, []string{"tok-secret"}},
		{toolCall(13, "execute", map[string]any{"operation_id": "group.webhook.delete", "dry_run": true, "arguments": map[string]any{"group_no": "g1", "webhook_id": "w1"}}), []string{`"dry_run": true`, "/v1/bot/groups/g1/incoming-webhooks/w1"}, nil},
	}

	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			backend, requests := xin2424Backend(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			args := []string{"mcp", "serve", "--facade", "two", "--force-channel-id", "forced-channel"}
			addr := freeAddr(t)
			if transport == "http" {
				args = append(args, "--http", addr)
			}
			cmd := exec.CommandContext(ctx, bin, args...)
			cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backend.URL, "OCTO_MCP_UPLOAD_ROOT="+root)
			var lines [][]byte
			if transport == "stdio" {
				var in strings.Builder
				for _, s := range steps {
					in.WriteString(s.request + "\n")
				}
				cmd.Stdin = strings.NewReader(in.String())
				out, err := cmd.Output()
				if err != nil {
					t.Fatalf("stdio server: %v", err)
				}
				lines = bytes.Split(bytes.TrimSpace(out), []byte("\n"))
			} else {
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
				waitPostJSON(t, "http://"+addr, `{"jsonrpc":"2.0","id":0,"method":"ping"}`, 10*time.Second)
				for _, s := range steps {
					req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr, strings.NewReader(s.request))
					req.Header.Set("Authorization", "Bearer bf_smoke")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					body, _ := io.ReadAll(resp.Body)
					_ = resp.Body.Close()
					lines = append(lines, bytes.TrimSpace(body))
				}
			}
			if len(lines) != len(steps) {
				t.Fatalf("got %d responses, want %d", len(lines), len(steps))
			}
			for i, line := range lines {
				var resp struct {
					Error  any `json:"error"`
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(line, &resp); err != nil || resp.Error != nil || len(resp.Result.Content) != 1 {
					t.Fatalf("step %d: invalid response: %s", i+1, line)
				}
				text := resp.Result.Content[0].Text
				for _, w := range steps[i].want {
					if !strings.Contains(text, w) {
						t.Errorf("step %d missing %q: %s", i+1, w, text)
					}
				}
				for _, r := range steps[i].reject {
					if strings.Contains(text, r) {
						t.Errorf("step %d must not contain %q: %s", i+1, r, text)
					}
				}
			}

			got := map[string]recordedRequest{}
			for _, r := range requests() {
				got[r.Method+" "+r.Path] = r
				if r.Auth != "Bearer bf_smoke" {
					t.Errorf("%s %s: Authorization = %q, want the connection Bot bearer", r.Method, r.Path, r.Auth)
				}
			}
			for _, key := range []string{
				"GET /v1/bot/card/profile",
				"DELETE /v1/bot/groups/g1/threads/s1",
				"POST /v1/bot/message/card/revisions/clear",
				"GET /v1/bot/docs/d1/ppt/media/a1",
				"POST /v1/bot/docs/d1/ppt/media",
				"POST /v1/bot/drive/shares/tok-secret/access",
			} {
				if _, ok := got[key]; !ok {
					t.Errorf("backend never received %s (got %v)", key, keys(got))
				}
			}
			if r := got["POST /v1/bot/message/card/revisions/clear"]; !strings.Contains(r.Body, `"forced-channel"`) || strings.Contains(r.Body, "model-channel") {
				t.Errorf("forced channel did not win: %s", r.Body)
			}
			if r := got["POST /v1/bot/docs/d1/ppt/media"]; r.Body != "PNG!" || r.ContentType != "application/octet-stream" || r.MediaType != "image/png" {
				t.Errorf("raw upload = %+v", r)
			}
			for key := range got {
				if strings.Contains(key, "incoming-webhooks") || strings.Contains(key, "resolve/targets") ||
					strings.Contains(key, "mention_pref") || strings.Contains(key, "attachments/svg") {
					t.Errorf("a refused or dry-run call reached the backend: %s", key)
				}
			}
			if n := len(requests()); n != 6 {
				t.Errorf("backend requests = %d, want exactly 6", n)
			}
		})
	}
}

func keys(m map[string]recordedRequest) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
