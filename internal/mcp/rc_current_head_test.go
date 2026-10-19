package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRCHTTPUploadRootMustBeDedicated(t *testing.T) {
	fixture := t.TempDir()
	for _, root := range []string{"/", "/etc", filepath.Join(fixture, "missing")} {
		t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
		if _, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}); err == nil {
			t.Errorf("HTTP server accepted broad or missing upload root %q", root)
		}
		if _, _, err := uploadLocation("fixture.txt", execPolicy{httpMode: true, uploadRoot: root}); err == nil {
			t.Errorf("HTTP upload accepted broad or missing root %q", root)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture, "fixture.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	markUploadRoot(t, fixture)
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", fixture)
	if _, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}); err != nil {
		t.Fatalf("dedicated upload directory refused: %v", err)
	}
	if _, err := openUpload("missing.txt", execPolicy{httpMode: true, uploadRoot: fixture}); err == nil || strings.Contains(err.Error(), fixture) {
		t.Fatalf("filesystem path leaked to HTTP caller: %v", err)
	}
}

func TestRCDuplicateJSONRPCEnvelopeRejected(t *testing.T) {
	for _, raw := range []string{
		`{"jsonrpc":"2.0","id":1,"id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/list"}`,
		`{"jsonrpc":"1.0","method":"ping","jsonrpc":"2.0","id":8}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{},"params":{"name":"execute"}}`,
	} {
		if _, code, err := decodeRPCRequest([]byte(raw)); err == nil || code != codeInvalidRequest {
			t.Errorf("ambiguous envelope accepted: %s (code=%d, err=%v)", raw, code, err)
		}
	}
	if _, code, err := decodeRPCRequest([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil || code != 0 {
		t.Fatalf("ordinary request rejected: code=%d err=%v", code, err)
	}
}

func TestRCDuplicateMethodRejectedOnBothTransports(t *testing.T) {
	raw := `{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/list"}`
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, httptest.NewRequest("POST", "/", strings.NewReader(raw)))
	var httpResult rpcResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &httpResult); err != nil || httpResult.Error == nil || httpResult.Error.Code != codeInvalidRequest {
		t.Fatalf("HTTP duplicate method was not rejected: %s (%v)", recorder.Body.String(), err)
	}
	s := newTestServer(t)
	var stdio bytes.Buffer
	if err := s.ServeStdio(context.Background(), strings.NewReader(raw+"\n"), &stdio); err != nil {
		t.Fatal(err)
	}
	var stdioResult rpcResponse
	if err := json.Unmarshal(stdio.Bytes(), &stdioResult); err != nil || stdioResult.Error == nil || stdioResult.Error.Code != codeInvalidRequest {
		t.Fatalf("stdio duplicate method was not rejected: %s (%v)", stdio.String(), err)
	}
}

func TestRCMalformedNotificationAndIDShape(t *testing.T) {
	s := newTestServer(t)
	if _, has := s.Dispatch(context.Background(), rpcRequest{Method: "notifications/initialized"}); has {
		t.Fatal("notification without id must not emit a response")
	}
	response, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "1.0", ID: json.RawMessage(`{"a":1}`), Method: "ping"})
	if !has || response.Error == nil || string(response.ID) != "null" {
		t.Fatalf("invalid id must not be reflected: %+v", response)
	}
}

func TestRCInitializeRejectsNonObjectParams(t *testing.T) {
	s := newTestServer(t)
	for _, raw := range []string{`"garbage"`, `[1,2,3]`, `{"protocolVersion":{"nested":true}}`} {
		resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "initialize", Params: json.RawMessage(raw)})
		if !has || resp.Error == nil || resp.Error.Code != codeInvalidParams {
			t.Errorf("malformed initialize params %s accepted: %+v", raw, resp)
		}
	}
}

func TestRCForcedChannelWarnsAboutUnresolvedFields(t *testing.T) {
	two := TrustedContext{ChannelID: "group", ChannelType: "2"}
	gaps := strings.Join(two.UnforcedChannelFields(), ",")
	if !strings.Contains(gaps, "thread.md-update.short_id") || !strings.Contains(gaps, "html.publish.thread_id") {
		t.Fatalf("group binding must disclose unforced thread fields: %s", gaps)
	}
	withThread := TrustedContext{ChannelID: "group", ChannelType: "2", ThreadID: "topic"}
	if gaps := withThread.UnforcedChannelFields(); len(gaps) != 0 {
		t.Fatalf("fully pinned group/thread still reports gaps: %v", gaps)
	}
	typeFive := TrustedContext{ChannelID: "group____topic", ChannelType: "5"}
	if gaps := typeFive.UnforcedChannelFields(); len(gaps) != 0 {
		t.Fatalf("type-5 composite still reports gaps: %v", gaps)
	}
	dm := TrustedContext{ChannelID: "person", ChannelType: "1"}
	if gaps := strings.Join(dm.UnforcedChannelFields(), ","); !strings.Contains(gaps, "group.update.group_no") {
		t.Fatalf("DM binding must disclose group gap: %s", gaps)
	}
}

func TestRCFingerprintCoversResultChangingFields(t *testing.T) {
	d := &registry.OperationDetail{}
	baseline := schemaFingerprint(d)
	d.UnwrapRequiredFields = []string{"doc_id"}
	if next := schemaFingerprint(d); next == baseline {
		t.Fatal("required response field changed without schema drift")
	}
	d.UnwrapRequiredFields = nil
	d.LosslessIDFields = []string{"id"}
	if next := schemaFingerprint(d); next == baseline {
		t.Fatal("lossless-ID rule changed without schema drift")
	}
}

func TestRCUnstructuredUnknownWriteRemainsUnknown(t *testing.T) {
	parsed := map[string]any{"error": map[string]any{"type": "api_error", "code": "RESULT_UNKNOWN"}}
	if got := classifyFailure(true, parsed, nil); got != "result_unknown" {
		t.Fatalf("uncertain write misclassified as %q", got)
	}
	if got := classifyFailure(false, parsed, nil); got != "execution_error" {
		t.Fatalf("read failure misclassified as %q", got)
	}
}

func TestRCAlreadyCancelledMultipartNeverTakesFreeSlot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slots := make(chan struct{}, 2)
	for i := 0; i < 50; i++ {
		cleanup, err := prepareMultipartExecution(ctx, nil, nil, execPolicy{uploadSlots: slots})
		if err == nil {
			t.Fatal("cancelled multipart request was admitted")
		}
		cleanup()
		if len(slots) != 0 {
			t.Fatal("cancelled multipart request retained an upload slot")
		}
	}
}
