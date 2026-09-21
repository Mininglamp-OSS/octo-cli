package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- operator GlobalOptions propagation ---

// TestCallGlobals_EveryFieldClassified fails when a new GlobalOptions field is
// added without deciding whether it is propagated into a call_op or reset, so
// the propagation class cannot silently regain a blind spot.
func TestCallGlobals_EveryFieldClassified(t *testing.T) {
	propagated := map[string]bool{"BotID": true, "Profile": true, "Timeout": true, "NoRetry": true, "Space": true}
	reset := map[string]bool{"Format": true, "DryRun": true, "Verbose": true, "JQ": true, "PageAll": true, "PageMax": true}
	tp := reflect.TypeOf(cmdutil.GlobalOptions{})
	for i := 0; i < tp.NumField(); i++ {
		name := tp.Field(i).Name
		if !propagated[name] && !reset[name] {
			t.Errorf("GlobalOptions field %q is unclassified — decide propagate vs reset in applyCallGlobals", name)
		}
	}
}

func TestApplyCallGlobals_PropagatesAndResets(t *testing.T) {
	g := &cmdutil.GlobalOptions{
		Format: "table", JQ: ".x", DryRun: true, Verbose: true,
		PageAll: true, PageMax: 99, Space: "old", BotID: "old", Profile: "old", Timeout: "old", NoRetry: false,
	}
	base := cmdutil.GlobalOptions{BotID: "b", Profile: "p", Timeout: "5s", NoRetry: true}
	applyCallGlobals(g, base, "forced-space")

	if g.BotID != "b" || g.Profile != "p" || g.Timeout != "5s" || !g.NoRetry {
		t.Errorf("operator routing/limit globals not propagated: %+v", g)
	}
	if g.Space != "forced-space" {
		t.Errorf("trusted space must win, got %q", g.Space)
	}
	if g.Format != output.FormatJSON || g.DryRun || g.Verbose || g.JQ != "" || g.PageAll || g.PageMax != 0 {
		t.Errorf("reset fields leaked into a call_op: %+v", g)
	}
}

// TestCallOp_ProfilePropagated_RealMakeFactory proves the operator's --profile
// selector reaches buildCredential on the real makeFactory path: a nonexistent
// profile fails closed with "profile ... not found" rather than being dropped
// (which would silently act as an env credential).
func TestCallOp_ProfilePropagated_RealMakeFactory(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	t.Setenv("OCTO_TOKEN", "bf_env")
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir()) // no stored profiles

	s := newTestServer(t)
	s.baseGlobals = cmdutil.GlobalOptions{Profile: "ghost"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "c1", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if !res.IsError {
		t.Fatalf("a nonexistent --profile must fail closed, got success: %s", res.Content[0].Text)
	}
	if be.path != "" {
		t.Errorf("no request should reach the backend when the selected profile is missing, path=%q", be.path)
	}
	if !strings.Contains(res.Content[0].Text, "ghost") {
		t.Errorf("error should name the missing profile, got %s", res.Content[0].Text)
	}
}

// --- forced-argument audit trail (m2) ---

func TestCallOp_ForcedArgumentsSurfacedInEnvelope(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{ChannelID: "forced-chan"}

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "message.send",
		"arguments":    map[string]any{"channel_id": "attacker", "channel_type": 1, "payload": map[string]any{"text": "x"}},
	})
	if res.IsError {
		t.Fatalf("call_op errored: %s", res.Content[0].Text)
	}
	env := toolText(t, res)
	forced, _ := env["_forced_arguments"].([]any)
	var sawChannel bool
	for _, f := range forced {
		if f == "channel_id" {
			sawChannel = true
		}
	}
	if !sawChannel {
		t.Errorf("_forced_arguments must disclose channel_id, got %v", env["_forced_arguments"])
	}
	if be.body["channel_id"] != "forced-chan" {
		t.Errorf("forced value must reach the backend, got %v", be.body["channel_id"])
	}
}

// --- httpMode wiring seam: end-to-end through NewHTTPHandler -> ServeHTTP ---

func TestHTTP_EndToEnd_NoAuthFailsClosed_RealMakeFactory(t *testing.T) {
	be := &backend{}
	bs := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", bs.URL)
	t.Setenv("OCTO_TOKEN", "bf_ambient") // server env credential must NOT be inherited
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())

	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	// No Authorization header, real makeFactory (no factoryFn override): the
	// httpMode wiring must fail the call closed and never reach the backend.
	_, body := postJSON(t, ts.URL, "", "",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"message.send","arguments":{"channel_id":"c1","channel_type":1,"payload":{"text":"x"}}}}}`)

	var resp rpcResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp.Error != nil {
		t.Fatalf("bad rpc response: %v (%s)", err, body)
	}
	var res toolResult
	_ = json.Unmarshal(resp.Result, &res)
	if !res.IsError {
		t.Fatalf("no-auth HTTP call must fail closed, got %s", res.Content[0].Text)
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &env)
	if e, _ := env["error"].(map[string]any); e == nil || e["type"] != "auth_error" {
		t.Errorf("expected auth_error, got %v", env)
	}
	if be.path != "" {
		t.Errorf("backend must not be reached without a bearer, path=%q", be.path)
	}
}

// --- buildArgv wire-type / null handling ---

func TestBuildArgv_CoercesForcedChannelTypeToInteger(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "message.edit", Service: "message", Method: "POST", Path: "/v1/bot/message/edit"},
		RequestBody: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{
			"channel_type": {Type: "integer"},
			"message_id":   {Type: "string"},
		}},
	}
	// channel_type supplied as the string "1" (as a header-forced value would be).
	argv, err := buildArgv(d, map[string]any{"channel_type": "1", "message_id": "m"}, execPolicy{})
	if err != nil {
		t.Fatalf("buildArgv: %v", err)
	}
	data := dataFlag(argv)
	if !strings.Contains(data, `"channel_type":1`) || strings.Contains(data, `"channel_type":"1"`) {
		t.Errorf("channel_type must be coerced to integer 1, got --data %s", data)
	}
}

func TestBuildArgv_NullQueryArgIsOmitted(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "message.search", Service: "message", Method: "GET", Path: "/v1/bot/message/search"},
		Parameters:    []registry.ParamInfo{{Name: "keyword", In: "query"}},
	}
	// null => omitted (no --keyword= empty flag).
	argv, err := buildArgv(d, map[string]any{"keyword": nil}, execPolicy{})
	if err != nil {
		t.Fatalf("buildArgv: %v", err)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "--keyword") {
			t.Errorf("null query arg must be omitted, got %v", argv)
		}
	}
	// a real value still emits the flag.
	argv2, _ := buildArgv(d, map[string]any{"keyword": "hi"}, execPolicy{})
	if !hasArg(argv2, "--keyword=hi") {
		t.Errorf("non-null query arg must emit its flag, got %v", argv2)
	}
}

func dataFlag(argv []string) string {
	for _, a := range argv {
		if strings.HasPrefix(a, "--data=") {
			return strings.TrimPrefix(a, "--data=")
		}
	}
	return ""
}

// --- HTTP oversized body ---

func TestHTTPTransport_OversizedBodyRejected(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	// A >8 MiB body of valid-JSON prefix + padding must be rejected, not truncated.
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/list","pad":"` + strings.Repeat("a", 9<<20) + `"}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL, strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body = %d, want 413", resp.StatusCode)
	}
}
