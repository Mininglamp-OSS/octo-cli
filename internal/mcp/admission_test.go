package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

const strictPolicy = `{"version":1,"context_mode":"strict","override":"strict","stage_default":"compatible","source":"bot","generation":"7","bot_revision":"6"}`
const compatiblePolicy = `{"version":1,"context_mode":"compatible","override":null,"stage_default":"compatible","source":"stage_default","generation":"7","bot_revision":null}`
const sourceMeta = `{"octo":{"version":1,"im":{"sender":"human_U","channel":"bot_A","channel_type":1,"space_id":"B","message_id":"2112815112610910209"}}}`

func secureTestDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("MCP private policy and request storage require POSIX")
	}
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	dir, e := os.MkdirTemp(wd, ".request-test-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
func testAdmission(t *testing.T) *Admission {
	t.Helper()
	a, e := NewAdmission(secureTestDir(t), nil)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := a.Close(); e != nil {
			t.Log(e)
		}
	})
	return a
}
func fixtureVerifier(t *testing.T, business http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/auth/verify-bot" {
			if business != nil {
				business(w, r)
			} else {
				http.Error(w, "unexpected business request", 500)
			}
			return
		}
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "bad fixture", 400)
			return
		}
		token, _ := body["bot_token"].(string)
		if !strings.HasPrefix(token, "bf_") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		uid := "bot_A"
		policy := strictPolicy
		if strings.Contains(token, "compatible") {
			uid = "bot_B"
			policy = compatiblePolicy
		}
		w.Header().Set("Content-Type", "application/json")
		if body["include_mcp_policy"] == true {
			fmt.Fprintf(w, `{"bot_uid":%q,"mcp_policy":%s}`, uid, policy)
		} else {
			fmt.Fprintf(w, `{"bot_uid":%q}`, uid)
		}
	}))
}
func request(t *testing.T, tool, args, meta string) rpcRequest {
	t.Helper()
	if args == "" {
		args = "{}"
	}
	params := `{"name":` + strconvQuote(tool) + `,"arguments":` + args
	if meta != "" {
		params += `,"_meta":` + meta
	}
	params += "}"
	var req rpcRequest
	if e := decodeRPCRequest([]byte(`{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":`+params+`}`), &req); e != nil {
		t.Fatal(e)
	}
	return req
}
func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }
func allLogRecords(t *testing.T, l *RequestLog) []RequestRecord {
	t.Helper()
	entries, e := l.entries()
	if e != nil {
		t.Fatal(e)
	}
	var out []RequestRecord
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".open") && !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, e := logOpenAt(l.dir, entry.Name(), false)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(f)
		_ = f.Close()
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
			if len(line) == 0 {
				continue
			}
			var r RequestRecord
			if e = json.Unmarshal(line, &r); e != nil {
				t.Fatal(e)
			}
			out = append(out, r)
		}
	}
	return out
}

func TestAdmissionStrictEveryToolAndCompatibleMissing(t *testing.T) {
	be := fixtureVerifier(t, nil)
	defer be.Close()
	s := newTestServer(t).WithFacade(facadeBoth).WithAdmission(testAdmission(t))
	s.factoryFn = fakeBackendFactory(be.URL, "bf_strict")
	for _, tool := range []string{"search_ops", "describe_op", "get_skill", "call_op", "execute", "get_secret_download_url"} {
		r, _ := s.Dispatch(context.Background(), request(t, tool, "{}", ""))
		if r.Error == nil || r.Error.Message != "CONTEXT_REQUIRED" {
			t.Fatalf("%s: %+v", tool, r)
		}
	}
	if len(allLogRecords(t, s.admission.log)) != 0 {
		t.Fatal("rejected calls were stored")
	}
	s.factoryFn = fakeBackendFactory(be.URL, "bf_compatible")
	r, _ := s.Dispatch(context.Background(), request(t, "search_ops", `{"domain":"message","query":"do not store"}`, ""))
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	records := allLogRecords(t, s.admission.log)
	if len(records) != 1 || records[0].DeclaredSource != nil || records[0].BotUID != "bot_B" || records[0].RPCID.Lexeme != "9007199254740993" || string(records[0].RequestedArguments["domain"]) != `"message"` {
		t.Fatalf("records: %+v", records)
	}
}

func TestAdmissionContextLexemes(t *testing.T) {
	if d, e := parseDeclaredSource([]byte(sourceMeta)); e != nil || d.MessageID != "2112815112610910209" || d.Channel != "bot_A" {
		t.Fatalf("%+v %v", d, e)
	}
	for _, bad := range []string{`null`, `{"octo":null}`, strings.Replace(sourceMeta, `"version":1`, `"version":1.0`, 1), strings.Replace(sourceMeta, `"channel_type":1`, `"channel_type":3`, 1), strings.Replace(sourceMeta, `"message_id":"2112815112610910209"`, `"message_id":2112815112610910209`, 1), strings.Replace(sourceMeta, "2112815112610910209", "9223372036854775808", 1), strings.Replace(sourceMeta, `"sender":"human_U"`, `"sender":"x","sen\u0064er":"y"`, 1), strings.Replace(sourceMeta, "human_U", `\ud800`, 1), strings.Replace(sourceMeta, "human_U", " human_U", 1), strings.Replace(sourceMeta, "human_U", `\u0000`, 1), strings.Replace(sourceMeta, `"octo"`, `"Octo"`, 1), `{"octo":` + strings.Repeat(" ", 4096) + `{}}`} {
		if _, e := parseDeclaredSource([]byte(bad)); e == nil {
			t.Fatalf("accepted malformed context: %.100s", bad)
		}
	}
	for _, id := range []string{`false`, `{}`, `[]`, `"` + strings.Repeat("x", 257) + `"`} {
		if validRPCID([]byte(id)) {
			t.Fatalf("accepted id %s", id)
		}
	}
	for _, id := range []string{`null`, `""`, `1e100`, `9007199254740993`} {
		if !validRPCID([]byte(id)) {
			t.Fatalf("rejected id %s", id)
		}
	}
}

func TestAdmissionPolicyClosedAndNoFallback(t *testing.T) {
	for _, raw := range []string{strictPolicy, compatiblePolicy} {
		if _, e := parsePolicy([]byte(raw)); e != nil {
			t.Fatal(e)
		}
	}
	for _, raw := range []string{`{}`, strings.Replace(strictPolicy, `"version":1`, `"version":1e0`, 1), strings.Replace(strictPolicy, `"generation":"7"`, `"generation":"07"`, 1), strings.Replace(strictPolicy, `"bot_revision":"6"`, `"bot_revision":"8"`, 1), strings.Replace(strictPolicy, `"override":"strict"`, `"override":null`, 1), strings.Replace(strictPolicy, `"source":"bot"`, `"source":"stage_default"`, 1), strings.Replace(strictPolicy, `"version":1`, `"version":1,"extra":true`, 1)} {
		if _, e := parsePolicy([]byte(raw)); e == nil {
			t.Fatal("accepted invalid policy")
		}
	}
	var calls atomic.Int32
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, `{"bot_uid":"bot_A"}`) }))
	defer be.Close()
	s := newTestServer(t).WithAdmission(testAdmission(t))
	s.factoryFn = fakeBackendFactory(be.URL, "bf_test")
	r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", sourceMeta))
	if r.Error == nil || r.Error.Message != "POLICY_UNAVAILABLE" || calls.Load() != 1 {
		t.Fatalf("%+v calls=%d", r, calls.Load())
	}
}

func TestAdmissionDebugSnapshotAndRealAuth(t *testing.T) {
	dir := secureTestDir(t)
	file := filepath.Join(dir, "policy.json")
	raw := `{"version":1,"api_base_url":"https://example.invalid","stage_default":"compatible","bot_policies":{"bot_A":{"context_mode":"strict"}}}`
	if e := os.WriteFile(file, []byte(raw), 0o600); e != nil {
		t.Fatal(e)
	}
	d, e := LoadDebugPolicyFile(file)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, []byte(strings.ReplaceAll(raw, "strict", "compatible")), 0o600); e != nil {
		t.Fatal(e)
	}
	if mode, e := d.resolve("bot_A", "https://example.invalid"); e != nil || mode != "strict" {
		t.Fatalf("%s %v", mode, e)
	}
	if _, e = d.resolve("bot_A", "https://different.invalid"); e == nil {
		t.Fatal("origin drift accepted")
	}
	if e = os.Chmod(file, 0o644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadDebugPolicyFile(file); e == nil {
		t.Fatal("unsafe mode accepted")
	}
	if e = os.Chmod(file, 0o600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(dir, "link")
	if e = os.Symlink(file, link); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadDebugPolicyFile(link); e == nil {
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(dir, "hard")
	if e = os.Link(file, hard); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadDebugPolicyFile(file); e == nil {
		t.Fatal("hardlink accepted")
	}
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "unauthorized", http.StatusUnauthorized) }))
	defer be.Close()
	a := testAdmission(t)
	a.debug = d
	s := newTestServer(t).WithAdmission(a)
	s.factoryFn = fakeBackendFactory(be.URL, "bf_test")
	r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", sourceMeta))
	if r.Error == nil || r.Error.Code != -32001 {
		t.Fatalf("debug bypassed authentication: %+v", r)
	}
}

func TestAdmissionHTTPIsolationAndProtocolAuth(t *testing.T) {
	be := fixtureVerifier(t, nil)
	defer be.Close()
	t.Setenv("OCTO_API_BASE_URL", be.URL)
	t.Setenv("OCTO_BOT_TOKEN", "bf_host_admin")
	h, e := NewHTTPHandlerWithAdmission(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, testAdmission(t), facadeBoth)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	post := func(token, body string, duplicate bool) rpcResponse {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if duplicate {
			r.Header.Add("Authorization", "Bearer bf_other")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var resp rpcResponse
		if e := json.Unmarshal(w.Body.Bytes(), &resp); e != nil {
			t.Fatal(e)
		}
		return resp
	}
	protocol := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	if r := post("", protocol, false); r.Error == nil || r.Error.Code != -32001 {
		t.Fatalf("host auth leaked: %+v", r)
	}
	if r := post("bf_strict", protocol, true); r.Error == nil || r.Error.Code != -32001 {
		t.Fatal("duplicate bearer accepted")
	}
	if r := post("bf_strict", protocol, false); r.Error != nil {
		t.Fatal(r.Error)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := "bf_strict"
			meta := sourceMeta
			if i%2 == 1 {
				token = "bf_compatible"
				meta = ""
			}
			q := request(t, "search_ops", "{}", meta)
			raw, _ := json.Marshal(q)
			resp := post(token, string(raw), false)
			if resp.Error != nil {
				t.Errorf("concurrent request: %+v", resp.Error)
			}
		}(i)
	}
	wg.Wait()
	rows := allLogRecords(t, h.admission.log)
	if len(rows) != 8 {
		t.Fatalf("records=%d", len(rows))
	}
	ids := map[string]bool{}
	for _, r := range rows {
		if ids[r.RequestID] {
			t.Fatal("shared request id")
		}
		ids[r.RequestID] = true
		if r.BotUID == "bot_A" && (r.DeclaredSource == nil || r.DeclaredSource.SpaceID != "B") {
			t.Fatal("source leaked")
		}
		if r.BotUID == "bot_B" && r.DeclaredSource != nil {
			t.Fatal("source leaked")
		}
	}
}

func TestAdmissionDurabilityFailureAndLateAckNeverExecute(t *testing.T) {
	var business atomic.Int32
	be := fixtureVerifier(t, func(w http.ResponseWriter, r *http.Request) { business.Add(1); io.WriteString(w, `{}`) })
	defer be.Close()
	for _, kind := range []string{"sync", "short_write", "cancel", "late_ack"} {
		t.Run(kind, func(t *testing.T) {
			a := testAdmission(t)
			s := newTestServer(t).WithAdmission(a)
			s.factoryFn = fakeBackendFactory(be.URL, "bf_strict")
			started, unblock := make(chan struct{}), make(chan struct{})
			var once sync.Once
			switch kind {
			case "sync":
				a.log.syncFile = func(*os.File) error { return errors.New("synthetic disk failure") }
			case "short_write":
				a.log.writeFile = func(*os.File, []byte) (int, error) { return 0, nil }
			default:
				a.log.syncFile = func(f *os.File) error { once.Do(func() { close(started) }); <-unblock; return f.Sync() }
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				go func() { <-started; cancel() }()
			}
			r, _ := s.Dispatch(ctx, request(t, "call_op", `{"operation_id":"message.send","arguments":{"channel_id":"target","channel_type":2,"payload":{"text":"secret body"}}}`, sourceMeta))
			if kind == "late_ack" || kind == "cancel" {
				close(unblock)
			}
			if r.Error == nil || r.Error.Code != -32004 {
				t.Fatalf("%+v", r)
			}
			if business.Load() != 0 {
				t.Fatal("business executed without durability")
			}
		})
	}
}

func TestAdmissionBotOnlyAndSecretProjection(t *testing.T) {
	for _, raw := range []string{`{"arguments":{"On-Behalf-Of":null}}`, `{"arguments":{"payload":{"__obo_grant":"x"}}}`, `{"arguments":{"actual_sender_uid":"x"}}`, `{"policy-debug":true}`, `{"arguments":{"Authorization":"x"}}`} {
		if e := guardBotOnlyArguments(context.Background(), []byte(raw)); e == nil {
			t.Fatalf("accepted control: %s", raw)
		}
	}
	if e := guardBotOnlyArguments(context.Background(), []byte(`{"arguments":{"payload":{"password":"hidden","pass\u0077ord":"replacement"}}}`)); e == nil {
		t.Fatal("duplicate decoded business key reached secret collection")
	}
	if e := guardBotOnlyArguments(context.Background(), []byte(`{"arguments":{"payload":{"content":"on_behalf_of is plain text"},"sender":"business-filter"}}`)); e != nil {
		t.Fatal(e)
	}
	s := newTestServer(t)
	req := request(t, "execute", `{"operation_id":"message.send","dry_run":true,"arguments":{"channel_id":"bf_secret","channel_type":2,"payload":{"content":"never log body"}}}`, sourceMeta)
	var p toolCallParams
	_ = decodeToolCallParams(req.Params, &p)
	r, e := projectRequest(s, req, p, "bot_A", "bf_secret", nil, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "bf_secret") || strings.Contains(string(b), "never log body") || !strings.Contains(string(b), "***REDACTED***") {
		t.Fatalf("unsafe projection: %s", b)
	}
}
