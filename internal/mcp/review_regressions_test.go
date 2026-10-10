package mcp

import (
	"encoding/json"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
	"github.com/spf13/pflag"
	"reflect"
)

func TestReviewReservedFlagsReachGuard(t *testing.T) {
	reg := registry.MustNew()
	d, _ := reg.GetOperation("file.upload")
	for _, name := range []string{"file.upload", "html.asset.add"} {
		operation, _ := reg.GetOperation(name)
		_, err := buildArgv(operation, map[string]any{"file": "/etc/passwd"}, execPolicy{httpMode: true})
		if err == nil || !strings.Contains(err.Error(), "reserved flag --file") {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, location := range []string{"query", "header"} {
		detail := &registry.OperationDetail{OperationInfo: registry.OperationInfo{ID: "fixture.get", Path: "/fixture", Method: "GET"}, Parameters: []registry.ParamInfo{{Name: "format", In: location, Type: "string"}}}
		_, err := buildArgv(detail, map[string]any{"format": "table"}, execPolicy{})
		if err == nil || !strings.Contains(err.Error(), "reserved flag --format") {
			t.Fatalf("%s: %v", location, err)
		}
	}
	argv, err := buildArgv(d, map[string]any{"file_path": "fixture.txt"}, execPolicy{uploadRoot: "/tmp/upload"})
	if err != nil || !hasPrefix(argv, "--file=") {
		t.Fatalf("legitimate binding: %v %v", argv, err)
	}
}

func TestReviewMultipartDescribeBinding(t *testing.T) {
	s, err := NewServer(testRoot)
	if err != nil {
		t.Fatal(err)
	}
	result := s.describeOp("file.upload")
	var obj map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &obj); err != nil {
		t.Fatal(err)
	}
	args, ok := obj["mcp_arguments"].(map[string]any)
	if !ok || args["file_path"] == nil {
		t.Fatalf("missing callable binding: %v", obj)
	}
}

func TestReviewAuthBeforeUploadLookup(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	h.uploadRoot = t.TempDir()
	if err := os.WriteFile(filepath.Join(h.uploadRoot, "exists.txt"), []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := h.connectionServer(httptest.NewRequest("POST", "http://localhost", http.NoBody))
	for _, path := range []string{"exists.txt", "missing.txt"} {
		res := s.callOp(t.Context(), "file.upload", map[string]any{"file_path": path})
		if !res.IsError || !strings.Contains(res.Content[0].Text, "UNAUTHORIZED") {
			t.Fatalf("%s: %s", path, res.Content[0].Text)
		}
	}
}

func TestReviewParseErrorHasNullID(t *testing.T) {
	raw, err := json.Marshal(newErrorResponse(nil, codeParseError, "invalid"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"id":null`) {
		t.Fatalf("%s", raw)
	}
}

func TestReviewAuthzTableNamesDeclaredFields(t *testing.T) {
	reg := registry.MustNew()
	for id, fields := range overridableParams {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Fatalf("unknown %s", id)
		}
		declared := map[string]bool{}
		for _, p := range op.Parameters {
			declared[p.Name] = true
		}
		if op.RequestBody != nil {
			for name := range op.RequestBody.Properties {
				declared[name] = true
			}
		}
		for name := range fields {
			if !declared[name] {
				t.Errorf("%s.%s is undeclared", id, name)
			}
		}
	}
}

func TestReviewHTTPRejectsStoredCredentialSelectors(t *testing.T) {
	for _, base := range []cmdutil.GlobalOptions{{Profile: "staging"}, {BotID: "bot-id"}} {
		handler, err := NewHTTPHandler(testRoot, TrustedContext{}, base)
		if err == nil || handler != nil || !strings.Contains(err.Error(), "not supported by MCP HTTP") {
			t.Fatalf("HTTP must reject ignored routing selectors: handler=%v err=%v", handler, err)
		}
	}
	handler, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil || handler == nil {
		t.Fatalf("ordinary HTTP configuration: %v", err)
	}
}

func TestReviewArrayFlagPreservesElements(t *testing.T) {
	want := []string{"a,b", "quoted\"value", "", "line\nbreak"}
	values := make([]any, len(want))
	for i := range want {
		values[i] = want[i]
	}
	argv, err := flagArgs("tag", values)
	if err != nil {
		t.Fatal(err)
	}
	flags := pflag.NewFlagSet("fixture", pflag.ContinueOnError)
	got := flags.StringSlice("tag", nil, "")
	if err := flags.Parse(argv); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("got %#v want %#v argv=%v", *got, want, argv)
	}
}
func TestReviewBinaryExportWithheld(t *testing.T) {
	s := newTestServer(t)
	if !s.describeOp("docs.scene.export").IsError {
		t.Fatal("export must not advertise an unusable result")
	}
	if !s.callOp(t.Context(), "docs.scene.export", map[string]any{}).IsError {
		t.Fatal("export must fail before execution")
	}
	result := s.searchOps(t.Context(), "docs", "scene.export")
	if strings.Contains(result.Content[0].Text, "docs.scene.export") {
		t.Fatalf("export leaked into discovery: %s", result.Content[0].Text)
	}
}

func TestReviewQueryArrayPreservedOnWire(t *testing.T) {
	be := &backend{reply: `{"data":{"items":[]}}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_fixture")
	res := s.callOp(t.Context(), "plugin.list", map[string]any{"scene_code": "default", "plugin_type": "skill", "tag": []any{"a,b", "quoted\"value"}})
	if res.IsError {
		t.Fatal(res.Content[0].Text)
	}
	if got := be.query["tag"]; !reflect.DeepEqual(got, []string{"a,b", "quoted\"value"}) {
		t.Fatalf("wire tags changed: %#v", got)
	}
}

func TestReviewAllInlineBinaryOperationsWithheld(t *testing.T) {
	reg := registry.MustNew()
	s := newTestServer(t)
	for _, op := range reg.EnabledOperations() {
		detail, _ := reg.GetOperation(op.ID)
		if detail.BinaryBody {
			if _, ok := divergentMCPOps()[op.ID]; !ok || !s.describeOp(op.ID).IsError {
				t.Fatalf("inline binary operation advertised: %s", op.ID)
			}
		}
	}
	redirect, _ := reg.GetOperation("file.download")
	if redirect.BinaryBody {
		t.Fatal("fixture no longer returns a redirect")
	}
	if _, blocked := divergentMCPOps()[redirect.ID]; blocked {
		t.Fatal("redirect URL results must remain callable")
	}
}

func TestReviewGlobalDryRunCannotReturnMultipartBytes(t *testing.T) {
	t.Setenv("OCTO_TOKEN", "bf_fixture")
	s := newTestServer(t)
	s.allowLocalUpload = true
	s.baseGlobals.DryRun = true
	res := s.callOp(t.Context(), "file.upload", map[string]any{"file_path": "missing.txt"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "global dry-run") {
		t.Fatal(res.Content[0].Text)
	}
}
func TestReviewOversizedUploadRejectedBeforeAssembly(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(filepath.Join(root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxMCPUploadBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	opened, err := openUpload("large.bin", execPolicy{uploadRoot: root})
	if opened != nil || err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("oversized upload accepted: %v %v", opened, err)
	}
}
