package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestAdmissionVerificationFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{"revoked", 401, `{"error":"private details"}`, "BOT_AUTH_INVALID"},
		{"limited", 429, `{"error":"private details"}`, "BOT_VERIFICATION_UNAVAILABLE"},
		{"redirect", 302, `{}`, "BOT_VERIFICATION_UNAVAILABLE"},
		{"malformed", 200, `{`, "BOT_VERIFICATION_UNAVAILABLE"},
		{"missing uid", 200, `{"mcp_policy":` + compatiblePolicy + `}`, "BOT_VERIFICATION_UNAVAILABLE"},
		{"oversized", 200, `{"bot_uid":"bot_A","padding":"` + strings.Repeat("x", 17000) + `"}`, "BOT_VERIFICATION_UNAVAILABLE"},
		{"policy missing", 200, `{"bot_uid":"bot_A"}`, "POLICY_UNAVAILABLE"},
		{"policy unavailable", 400, `{"error":{"code":"err.server.mcp.policy_unavailable","http_status":503,"message":"unavailable","details":{}}}`, "POLICY_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer be.Close()
			s := newTestServer(t)
			s.factoryFn = fakeBackendFactory(be.URL, "bf_test")
			r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", sourceMeta))
			if r.Error == nil || r.Error.Message != tc.reason || calls.Load() != 1 {
				t.Fatalf("reason=%+v attempts=%d", r.Error, calls.Load())
			}
			if len(allLogRecords(t, s.admission.log)) != 0 {
				t.Fatal("failed verification was recorded")
			}
		})
	}
}

func TestAdmissionDebugUsesAuthenticatedUIDWithoutPolicyOptIn(t *testing.T) {
	var verifications atomic.Int32
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		if _, exists := p["include_mcp_policy"]; exists {
			t.Error("debug requested production policy")
		}
		verifications.Add(1)
		io.WriteString(w, `{"bot_uid":"bot_A"}`)
	}))
	defer be.Close()
	s := newTestServer(t)
	s.admission.debug = &DebugPolicy{origin: be.URL, stage: "compatible", bots: map[string]string{"bot_A": "strict", "spoofed": "compatible"}}
	s.factoryFn = fakeBackendFactory(be.URL, "bf_rotated")
	expectAdmissionRPC(t, s, "search_ops", map[string]any{}, -32602)
	r, _ := s.Dispatch(context.Background(), request(t, "search_ops", `{"bot_uid":"spoofed"}`, sourceMeta))
	if r.Error == nil || r.Error.Code != -32005 {
		t.Fatal("caller selected debug identity")
	}
	r, _ = s.Dispatch(context.Background(), request(t, "search_ops", "{}", sourceMeta))
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	rows := allLogRecords(t, s.admission.log)
	if len(rows) != 1 || rows[0].BotUID != "bot_A" || verifications.Load() != 3 {
		t.Fatal("debug did not use fresh authenticated identity")
	}
}

func TestAdmissionSourceAndRequestedTargetRemainSeparate(t *testing.T) {
	var target, space string
	be := fixtureVerifier(t, func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		target, _ = p["channel_id"].(string)
		space = r.Header.Get("X-Space-Id")
		io.WriteString(w, `{}`)
	})
	defer be.Close()
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(be.URL, "bf_strict")
	s.trusted = TrustedContext{SpaceID: "operator_space", ChannelID: "forced_target", ChannelType: "2"}
	meta := strings.Replace(sourceMeta, `"channel_type":1`, `"channel_type":5`, 1)
	meta = strings.Replace(meta, `"channel":"bot_A"`, `"channel":"group_A____2112815112610910208"`, 1)
	meta = strings.Replace(meta, `"space_id":"B"`, `"space_id":"A"`, 1)
	r, _ := s.Dispatch(context.Background(), request(t, "call_op", `{"operation_id":"message.send","arguments":{"channel_id":"requested_target","channel_type":2,"payload":{"content":"private body"}}}`, meta))
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	if target != "forced_target" || space != "operator_space" {
		t.Fatal("source overwrote operator destination or scope")
	}
	rows := allLogRecords(t, s.admission.log)
	if len(rows) != 1 || rows[0].DeclaredSource.SpaceID != "A" || rows[0].DeclaredSource.Channel != "group_A____2112815112610910208" || string(rows[0].RequestedArguments["channel_id"]) != `"requested_target"` {
		t.Fatalf("source/request mutated: %+v", rows)
	}
}

func TestAdmissionStdioCredentialRotationAndProtocolExemptions(t *testing.T) {
	be := fixtureVerifier(t, nil)
	defer be.Close()
	t.Setenv("OCTO_API_BASE_URL", be.URL)
	t.Setenv("OCTO_BOT_TOKEN", "bf_strict")
	s := newTestServer(t)
	var out strings.Builder
	if e := s.ServeStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out); e != nil {
		t.Fatal(e)
	}
	r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", ""))
	if r.Error == nil || r.Error.Message != "CONTEXT_REQUIRED" {
		t.Fatal("strict stdio failed")
	}
	t.Setenv("OCTO_BOT_TOKEN", "bf_compatible")
	r, _ = s.Dispatch(context.Background(), request(t, "search_ops", "{}", ""))
	if r.Error != nil {
		t.Fatal(r.Error)
	}
	rows := allLogRecords(t, s.admission.log)
	if len(rows) != 1 || rows[0].BotUID != "bot_B" {
		t.Fatal("stdio cached old credential")
	}
	for _, method := range []string{"initialize", "ping", "tools/list", "resources/list", "resources/read"} {
		q := rpcRequest{ID: json.RawMessage(`1`), Method: method, Params: json.RawMessage(`{"_meta":{"octo":null}}`)}
		r, _ := s.Dispatch(context.Background(), q)
		if r.Error == nil || r.Error.Code != -32602 {
			t.Fatalf("%s ignored malformed metadata", method)
		}
	}
	for _, method := range []string{"tools/call", "notifications/initialized", "unknown"} {
		q := request(t, "search_ops", "{}", sourceMeta)
		q.Method = method
		q.ID = nil
		if _, has := s.Dispatch(context.Background(), q); has {
			t.Fatal("notification received a response")
		}
	}
}

func TestAdmissionDebugFileSchemaRefusals(t *testing.T) {
	dir := secureTestDir(t)
	path := filepath.Join(dir, "policy.json")
	good := `{"version":1,"api_base_url":"https://example.invalid","stage_default":"compatible","bot_policies":{}}`
	for _, raw := range []string{`null`, strings.Replace(good, `"version":1`, `"version":1.0`, 1), strings.Replace(good, `"version":1`, `"version":1,"ver\u0073ion":1`, 1), strings.Replace(good, `"bot_policies":{}`, `"bot_policies":null`, 1), strings.Replace(good, `"stage_default":"compatible"`, `"stage_default":" strict"`, 1), strings.Replace(good, "https://example.invalid", "https://user:secret@example.invalid", 1), strings.Replace(good, "https://example.invalid", "https://example.invalid/api", 1), good + ` {}`, strings.Replace(good, `"bot_policies":{}`, `"bot_policies":{"\ud800":{"context_mode":"strict"}}`, 1), strings.Repeat(" ", 65537) + good} {
		if e := os.WriteFile(path, []byte(raw), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, e := LoadDebugPolicyFile(path); e == nil {
			t.Fatal("invalid debug file accepted")
		}
	}
}

func TestAdmissionVerificationDeadlineCancellation(t *testing.T) {
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer be.Close()
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(be.URL, "bf_test")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	r, _ := s.Dispatch(ctx, request(t, "search_ops", "{}", sourceMeta))
	if r.Error == nil || r.Error.Code != -32002 || time.Since(started) > time.Second {
		t.Fatal("verification ignored caller deadline")
	}
}

func TestAdmissionHTTPConstructorRequiresStorage(t *testing.T) {
	t.Setenv("OCTO_MCP_REQUEST_LOG_DIR", "")
	if h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}); err == nil {
		h.Close()
		t.Fatal("HTTP constructor accepted missing storage")
	}
	t.Setenv("OCTO_MCP_REQUEST_LOG_DIR", secureTestDir(t))
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.Close()
	select {
	case <-h.admission.log.done:
	default:
		t.Fatal("HTTP close left an owned writer running")
	}
}

func TestAdmissionConcurrentSourcesAndOrigins(t *testing.T) {
	productionBackend := fixtureVerifier(t, nil)
	defer productionBackend.Close()
	debugBackend := fixtureVerifier(t, nil)
	defer debugBackend.Close()
	production := newTestServer(t)
	production.factoryFn = fakeBackendFactory(productionBackend.URL, "bf_strict")
	debug := newTestServer(t)
	debug.factoryFn = fakeBackendFactory(debugBackend.URL, "bf_strict")
	debug.admission.debug = &DebugPolicy{origin: debugBackend.URL, stage: "strict", bots: map[string]string{"bot_A": "compatible"}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := production
			meta := strings.Replace(sourceMeta, "human_U", fmt.Sprintf("human_%d", i), 1)
			if i%2 == 1 {
				s = debug
				meta = ""
			}
			r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", meta))
			if r.Error != nil {
				t.Errorf("isolated origin/source request: %+v", r.Error)
			}
		}(i)
	}
	wg.Wait()
	for _, s := range []*Server{production, debug} {
		rows := allLogRecords(t, s.admission.log)
		if len(rows) != 8 {
			t.Fatalf("isolated records=%d", len(rows))
		}
		for _, row := range rows {
			if row.BotUID != "bot_A" || (s == production) != (row.DeclaredSource != nil) {
				t.Fatal("policy source or origin leaked across RPCs")
			}
		}
	}
}

func TestAdmissionOptionalToolArguments(t *testing.T) {
	backend := fixtureVerifier(t, nil)
	defer backend.Close()
	for _, token := range []string{"bf_strict", "bf_compatible"} {
		s := newTestServer(t)
		s.factoryFn = fakeBackendFactory(backend.URL, token)
		params := json.RawMessage(`{"name":"search_ops","_meta":` + sourceMeta + `}`)
		r, _ := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
		if r.Error != nil {
			t.Fatalf("omitted optional arguments: %+v", r.Error)
		}
		if rows := allLogRecords(t, s.admission.log); len(rows) != 1 {
			t.Fatal("optional arguments bypassed persistence")
		}
	}
}

func TestAdmissionProductionDriftRotationAndRevocation(t *testing.T) {
	var strict, exception, revoked atomic.Bool
	var verifications atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifications.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["include_mcp_policy"] != true {
			t.Error("production omitted policy opt-in")
		}
		if revoked.Load() && body["bot_token"] == "bf_old" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		policy := compatiblePolicy
		if strict.Load() {
			policy = `{"version":1,"context_mode":"strict","override":null,"stage_default":"strict","source":"stage_default","generation":"8","bot_revision":null}`
		}
		if exception.Load() {
			policy = `{"version":1,"context_mode":"compatible","override":"compatible","stage_default":"strict","source":"bot","generation":"9","bot_revision":"9"}`
		}
		_, _ = io.WriteString(w, `{"bot_uid":"bot_A","mcp_policy":`+policy+`}`)
	}))
	defer backend.Close()
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_old")
	if r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", "")); r.Error != nil {
		t.Fatal(r.Error)
	}
	strict.Store(true)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_new")
	expectAdmissionRPC(t, s, "search_ops", map[string]any{}, -32602)
	if r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", sourceMeta)); r.Error != nil {
		t.Fatal(r.Error)
	}
	exception.Store(true)
	if r, _ := s.Dispatch(context.Background(), request(t, "search_ops", "{}", "")); r.Error != nil {
		t.Fatal("explicit compatible override was lost")
	}
	revoked.Store(true)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_old")
	expectAdmissionRPC(t, s, "search_ops", map[string]any{}, -32001)
	rows := allLogRecords(t, s.admission.log)
	if verifications.Load() != 5 || len(rows) != 3 {
		t.Fatal("identity or policy was cached")
	}
	for _, row := range rows {
		if row.BotUID != "bot_A" {
			t.Fatal("token rotation changed policy identity")
		}
	}
}
