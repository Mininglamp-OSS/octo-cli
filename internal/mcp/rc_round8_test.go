package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRound8HTMLMountMustMatchTrustedBinding(t *testing.T) {
	for _, tc := range []TrustedContext{
		{ThreadID: "T-OP"},
		{ChannelID: "G-OP", ChannelType: "2"},
		{ChannelID: "G-OP____T-OP", ChannelType: "5"},
	} {
		for _, op := range []string{"html.publish", "html.draft.create"} {
			for _, mount := range []string{"", "space", "group", "thread"} {
				err := tc.validateOperationBinding(op, map[string]any{"mount_type": mount})
				allowed := mount == "group" && tc.value(fieldGroupID) != "" || mount == "thread" && tc.value(fieldThreadID) != ""
				if tc.ChannelID != "" {
					expected := "group"
					if tc.ChannelType == "5" {
						expected = "thread"
					}
					allowed = mount == "" || mount == expected
				} else if tc.ThreadID != "" {
					allowed = mount == "" || mount == "thread"
				}
				if (err == nil) != allowed {
					t.Errorf("%+v %s mount=%q: err=%v allowed=%t", tc, op, mount, err, allowed)
				}
			}
		}
	}
}

func TestRound8IncompleteOrConflictingBindingFailsClosed(t *testing.T) {
	for _, tc := range []TrustedContext{
		{ThreadID: "T-OP"}, {ChannelID: "G-OP"}, {ChannelType: "2"},
	} {
		if got := tc.UnforcedChannelFields(); len(got) == 0 {
			t.Errorf("incomplete binding %+v had no diagnostic", tc)
		}
		if err := tc.validateOperationBinding("thread.md-update", map[string]any{"group_no": "G-EVIL", "short_id": "T-EVIL"}); err == nil {
			t.Errorf("incomplete binding %+v allowed a thread write", tc)
		}
	}
	if err := (&TrustedContext{ChannelType: "5", ChannelID: "G1____T1", ThreadID: "T9"}).ValidateBinding(); err == nil {
		t.Fatal("contradictory type-5 and thread flags accepted")
	}
}

func TestRound8HeaderCannotCompleteOperatorPinnedChannel(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{ChannelID: "G-OP"}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.WithContextHeaders(true)
	req := httptest.NewRequest(http.MethodPost, "/", http.NoBody)
	req.Header.Set(headerChannelType, "2")
	if got := h.connectionServer(req).trusted.ChannelType; got != "" {
		t.Fatalf("caller supplied missing type for operator channel: %q", got)
	}
	h.baseTrusted = TrustedContext{ThreadID: "T-OP"}
	req.Header.Set(headerChannelID, "G-EVIL")
	if got := h.connectionServer(req).trusted.ChannelID; got != "" {
		t.Fatalf("caller supplied missing group for operator thread: %q", got)
	}
}

func TestRound8BothFacadesRefuseMismatchedHTMLBeforeBackend(t *testing.T) {
	be := &backend{}
	remote := be.server(t)
	for _, tool := range []string{"call_op", "execute"} {
		s := newTestServer(t).WithFacade(facadeBoth).WithTrustedContext(TrustedContext{ThreadID: "T-OP"})
		s.factoryFn = fakeBackendFactory(remote.URL, "bf_fixture")
		res := callTool(t, s, tool, map[string]any{"operation_id": "html.publish", "arguments": map[string]any{
			"html": "<p>x</p>", "mount_type": "group", "group_no": "G-EVIL",
		}})
		if !res.IsError {
			t.Errorf("%s accepted mismatched mount", tool)
		}
	}
	if be.path != "" {
		t.Fatalf("rejected mount reached backend: %s", be.path)
	}
}

func TestRound8NestedDuplicateKeysRejected(t *testing.T) {
	if _, err := decodeArgs(json.RawMessage(`{"channel_id":"G1","channel_id":"G2"}`)); err == nil {
		t.Fatal("duplicate operation argument accepted")
	}
	s := newTestServer(t)
	resp := s.handleResourceRead(rpcRequest{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"uri":"octo://skills/octo-docs/SKILL.md","uri":"octo://skills/octo-mail/SKILL.md"}`)})
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("duplicate resources/read URI accepted: %+v", resp)
	}
	if _, _, err := decodeRPCRequest([]byte(`{"jsonrpc":"2.0","id":null,"method":"ping","params":null}`)); err != nil {
		t.Fatalf("valid JSON-RPC null id/params refused: %v", err)
	}
	resp, _ = s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18","protocolVersion":"2024-11-05"}`)})
	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("duplicate initialize version accepted: %+v", resp)
	}
}

func TestRound8UploadRootRequiresDedicatedMarker(t *testing.T) {
	root := t.TempDir()
	if err := validateHTTPUploadRoot(root); err == nil {
		t.Fatal("unmarked root accepted")
	}
	markUploadRoot(t, root)
	if err := validateHTTPUploadRoot(root); err != nil {
		t.Fatalf("dedicated root refused: %v", err)
	}
	for _, broad := range []string{"/", "/home", "/root", "/usr", "/var", "/tmp", os.Getenv("HOME")} {
		if broad != "" && validateHTTPUploadRoot(broad) == nil {
			t.Errorf("broad or private root %q accepted", broad)
		}
	}
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCTO_CONFIG_DIR", private)
	if err := validateHTTPUploadRoot(root); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("root containing credential directory accepted: %v", err)
	}
	if _, _, err := uploadLocation("missing.txt", execPolicy{httpMode: true, dryRun: true, uploadRoot: "/"}); err == nil {
		t.Fatal("dry-run bypassed upload root validation")
	}
}

func TestRound8LocalFailureNeverUnknown(t *testing.T) {
	local := output.ErrWithHint("internal", "MULTIPART_FAILED", "local read failed", "")
	if got := classifyExitFailure(true, local); got != "execution_error" {
		t.Fatalf("local pre-send failure = %s", got)
	}
	remote := output.ErrWithHint("internal", "RESPONSE_NORMALIZE", "bad response", "")
	remote.ResponseReceived = true
	if got := classifyExitFailure(true, remote); got != "result_unknown" {
		t.Fatalf("post-response write failure = %s", got)
	}
	result := classifyExecuteResult(synthErrorEnvelope(remote), false, "message.send", "", true, true, remote)
	if got := toolText(t, result)["status"]; got == "result_unknown" {
		t.Fatal("dry-run reported result_unknown")
	}
}

func TestRound8FingerprintCoversWholeExecutionDetail(t *testing.T) {
	base := &registry.OperationDetail{Parameters: []registry.ParamInfo{{Name: "tags", Items: &registry.SchemaInfo{Type: "string"}}}}
	initial := schemaFingerprint(base)
	mutate := func(change func(*registry.OperationDetail)) {
		t.Helper()
		copyDetail := *base
		copyDetail.Parameters = append([]registry.ParamInfo(nil), base.Parameters...)
		change(&copyDetail)
		if schemaFingerprint(&copyDetail) == initial {
			t.Fatal("execution metadata mutation did not change fingerprint")
		}
	}
	mutate(func(d *registry.OperationDetail) { d.BinaryBody = true })
	mutate(func(d *registry.OperationDetail) { d.BinaryResponse = true })
	mutate(func(d *registry.OperationDetail) { d.StrictResponseSchema = true })
	mutate(func(d *registry.OperationDetail) { d.ResponseFieldAliases = map[string][]string{"id": {"doc_id"}} })
	mutate(func(d *registry.OperationDetail) {
		d.Parameters[0].Items = &registry.SchemaInfo{Type: "string", Default: "x"}
	})
	copyDetail := *base
	copyDetail.Parameters = append([]registry.ParamInfo(nil), base.Parameters...)
	copyDetail.Parameters[0].Items = &registry.SchemaInfo{Type: "string", Description: "different prose"}
	if schemaFingerprint(&copyDetail) != initial {
		t.Fatal("parameter item prose changed fingerprint")
	}
}
