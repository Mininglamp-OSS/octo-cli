package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRCPageAllMustValidate(t *testing.T) {
	reg := registry.MustNew()
	d, _ := reg.GetOperation("message.send")
	for _, v := range []any{true, "true", 1, nil} {
		if _, err := buildArgv(d, map[string]any{"page_all": v}, execPolicy{}); err == nil {
			t.Fatalf("page_all=%v silently ignored", v)
		}
	}
	paged := &registry.OperationDetail{OperationInfo: registry.OperationInfo{ID: "fixture.list", Service: "fixture", Method: "GET", Path: "/list"}, Pagination: &registry.PaginationInfo{}}
	if _, err := buildArgv(paged, map[string]any{"page_all": true}, execPolicy{}); err != nil {
		t.Fatal(err)
	}
}
func TestRCUndeclaredBodyRefused(t *testing.T) {
	d, _ := registry.MustNew().GetOperation("message.sync")
	if _, err := buildArgv(d, map[string]any{"on_behalf_of": "victim"}, execPolicy{}); err == nil {
		t.Fatal("undeclared identity forwarded")
	}
}
func TestRCScalarArrayRefused(t *testing.T) {
	d, _ := registry.MustNew().GetOperation("plugin.list")
	if _, err := buildArgv(d, map[string]any{"tag": "a,b"}, execPolicy{}); err == nil {
		t.Fatal("scalar slice argument reinterpreted")
	}
}
func TestRCContextHeadersDefaultOff(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost", http.NoBody)
	r.Header.Set(headerChannelID, "attacker")
	if h.connectionServer(r).trusted.ChannelID != "" {
		t.Fatal("untrusted context header accepted")
	}
}
func TestRCHardlinkRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(outside, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "linked.txt")); err != nil {
		t.Skip(err)
	}
	f, err := openUpload("linked.txt", execPolicy{uploadRoot: root})
	if f != nil {
		_ = f.Close()
	}
	if err == nil {
		t.Fatal("multiply-linked file accepted")
	}
	regular := filepath.Join(root, "regular.txt")
	if err := os.WriteFile(regular, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = openUpload("regular.txt", execPolicy{uploadRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}
func TestRCDisabledCallGate(t *testing.T) {
	s := newTestServer(t)
	called := false
	s.factoryFn = func(TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
		called = true
		panic("disabled operation constructed")
	}
	result := s.callOp(t.Context(), "matter.list", map[string]any{})
	if !result.IsError || called || !strings.Contains(result.Content[0].Text, "disabled service") {
		t.Fatal(result)
	}
}

func TestRCExpandedScopesReachWire(t *testing.T) {
	tc := TrustedContext{ChannelID: "channel", ChannelType: "2", ThreadID: "thread", WorkspaceID: "workspace", PrincipalSpaceID: "principal"}
	cases := []struct {
		id                          string
		args                        map[string]any
		path, header, bodyKey, want string
	}{
		{"group.get", map[string]any{"group_no": "attacker"}, "/channel/", "", "", ""},
		{"task.list", map[string]any{"X-Workspace-ID": "attacker"}, "", "X-Workspace-ID", "", "workspace"},
		{"drive.im-transfer.create", map[string]any{"im_group_no": "attacker", "im_channel_type": 1, "im_msg_id": "message", "target_space_id": "shared:resource"}, "", "", "im_group_no", "channel"},
		{"docs.members.remove", map[string]any{"docId": "doc", "uid": "user", "principalSpaceId": "attacker"}, "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			be := &backend{reply: `{"data":{}}`}
			backend := be.server(t)
			s := newTestServer(t)
			s.trusted = tc
			s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
			res := s.callOp(t.Context(), c.id, c.args)
			if res.IsError {
				t.Fatal(res.Content[0].Text)
			}
			if c.path != "" && !strings.Contains(be.path, "channel") {
				t.Fatal(be.path)
			}
			if c.header != "" && be.header.Get(c.header) != c.want {
				t.Fatalf("header %s=%q want %q", c.header, be.header.Get(c.header), c.want)
			}
			if c.bodyKey != "" && fmt.Sprint(be.body[c.bodyKey]) != c.want {
				t.Fatal(be.body)
			}
			if c.id == "docs.members.remove" && be.query.Get("principalSpaceId") != "principal" {
				t.Fatal(be.query)
			}
			if c.id == "drive.im-transfer.create" && be.body["target_space_id"] != "shared:resource" {
				t.Fatal("Drive resource must remain caller-owned")
			}
		})
	}
}
func TestRCMetadataAndCancellation(t *testing.T) {
	s := newTestServer(t).WithVersion("v-test")
	if s.handleInitialize(nil)["serverInfo"].(map[string]any)["version"] != "v-test" {
		t.Fatal("artifact version ignored")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if candidates := nearestOperations(s.reg, "message.snd", ctx); len(candidates) != 0 {
		t.Fatal(candidates)
	}
	op := &registry.OperationDetail{Pagination: &registry.PaginationInfo{}}
	obj := map[string]any{}
	addMultipartBinding(obj, op)
	if obj["mcp_arguments"].(map[string]any)["page_all"] == nil {
		t.Fatal(obj)
	}
}
func TestRCEnvelopeValidation(t *testing.T) {
	s := newTestServer(t)
	for _, req := range []rpcRequest{{Method: "ping", ID: json.RawMessage("1")}, {Jsonrpc: "2.0", Method: "ping", ID: json.RawMessage("true")}, {Jsonrpc: "2.0", ID: json.RawMessage("1")}} {
		response, has := s.Dispatch(t.Context(), req)
		if !has || response.Error == nil || response.Error.Code != codeInvalidRequest {
			t.Fatalf("invalid envelope accepted: %+v", response)
		}
	}
}

func TestRCScopeAliasesAudited(t *testing.T) {
	for _, name := range []string{"group_no", "X-Workspace-ID", "principalSpaceId", "owner_channel_id", "octo_space_id", "im_channel_type", "target_space_id"} {
		if !isScopeField(name) {
			t.Fatalf("scope spelling invisible: %s", name)
		}
	}
}
func TestRCOptInHeadersAndOperatorPrecedence(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{WorkspaceID: "operator"}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.WithContextHeaders(true)
	r := httptest.NewRequest("POST", "http://localhost", http.NoBody)
	r.Header.Set("X-Workspace-ID", "attacker")
	r.Header.Set("X-Octo-Thread-Id", "thread")
	got := h.connectionServer(r).trusted
	if got.WorkspaceID != "operator" || got.ThreadID != "thread" {
		t.Fatal(got)
	}
}

func TestRCConcurrentHTTPRequestsHaveIsolatedContext(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.WithContextHeaders(true)
	var group sync.WaitGroup
	for i := 0; i < 32; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			r := httptest.NewRequest("POST", "http://localhost", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
			want := fmt.Sprintf("workspace-%d", i)
			r.Header.Set("X-Workspace-ID", want)
			r.Header.Set("Authorization", "Bearer bf_fixture")
			if got := h.connectionServer(r).trusted.WorkspaceID; got != want {
				t.Errorf("context leak: %s want %s", got, want)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("response %d", w.Code)
			}
		}(i)
	}
	group.Wait()
}

func TestRCOpenSchemaBusinessFields(t *testing.T) {
	d, _ := registry.MustNew().GetOperation("project.create")
	if _, err := buildArgv(d, map[string]any{"name": "project"}, execPolicy{}); err != nil {
		t.Fatalf("open schema business field refused: %v", err)
	}
	for _, key := range []string{"on_behalf_of", "ON_BEHALF_OF", "space_id", "workspace_id", "profile"} {
		if _, err := buildArgv(d, map[string]any{key: "attacker"}, execPolicy{}); err == nil {
			t.Fatalf("open schema identity accepted: %s", key)
		}
	}
}
func TestRCFullRangeUint64String(t *testing.T) {
	d, _ := registry.MustNew().GetOperation("drive.file.move")
	argv, err := buildArgv(d, map[string]any{"file_id": "1", "parent_id": "18446744073709551615"}, execPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), `"parent_id":18446744073709551615`) {
		t.Fatalf("uint64 string not converted: %v", argv)
	}
}
func TestRCThreadParentAndShortID(t *testing.T) {
	tc := TrustedContext{ChannelID: "group____short", ChannelType: "5"}
	args := map[string]any{"group_no": "attacker", "short_id": "attacker"}
	tc.apply("thread.get", args)
	if args["group_no"] != "group" {
		t.Fatalf("thread parent incorrectly forced: %v", args)
	}
	if args["short_id"] != "short" {
		t.Fatalf("thread short id not confined: %v", args)
	}
}
func TestRCEnvelopeNumbersRemainExact(t *testing.T) {
	raw := []byte(`{"ok":true,"data":{"cursor":9007199254740993}}`)
	got := spliceForcedArguments(raw, map[string]bool{"channel_id": true})
	if !bytes.Contains(got, []byte("9007199254740993")) {
		t.Fatalf("numeric envelope rounded: %s", got)
	}
}

func TestRCArrayForScalarRefused(t *testing.T) {
	d, _ := registry.MustNew().GetOperation("docs.sheet.get")
	for _, v := range []any{[]any{}, []any{"one", "two"}} {
		if _, err := buildArgv(d, map[string]any{"docId": "doc", "sheetId": v}, execPolicy{}); err == nil {
			t.Fatal("array scalar silently accepted")
		}
	}
}

func TestRCHTTPIdentitySource(t *testing.T) {
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	be := &backend{reply: `{"data":{}}`}
	srv := be.server(t)
	t.Setenv("OCTO_API_BASE_URL", srv.URL)
	s := newTestServer(t)
	s.httpMode = true
	s.credentialToken = "bf_fixture"
	res := s.callOp(t.Context(), "message.send", map[string]any{"channel_id": "group", "channel_type": 2, "payload": map[string]any{"text": "hello"}})
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	var env struct {
		Identity map[string]any `json:"identity"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &env); err != nil {
		t.Fatal(err)
	}
	if env.Identity["source"] != "mcp:connection" {
		t.Fatalf("HTTP credential provenance lost: %v", env.Identity)
	}
}
