package mcp

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// XIN-2438: the metadata-only multipart / raw-body dry run must refuse what the
// real call refuses and must describe the request it would actually send.

func multipartDryRunServer(t *testing.T) *Server {
	t.Helper()
	s := facadeServer(t, facadeTwo)
	s.allowLocalUpload = true
	s.factoryFn = fakeBackendFactory(os.Getenv("OCTO_API_BASE_URL"), "bf_test")
	return s
}

func TestXIN2438MultipartDryRunResolvesIdentity(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	s.httpMode = true
	s.uploadRoot = t.TempDir()
	args := map[string]any{"operation_id": "file.upload", "dry_run": true, "arguments": map[string]any{"file_path": "upload.bin"}}

	// An HTTP connection without a bearer: the real call is an auth_error, so
	// the dry run must not hand back an ok plan.
	expectAdmissionRPC(t, s, "execute", args, -32001)
	// With a bearer, the same plan succeeds.
	s.credentialToken = "bf_test"
	res := callTool(t, s, "execute", args)
	if out := toolText(t, res); res.IsError || out["status"] != "ok" {
		t.Fatalf("dry run with a credential must plan, got %s", res.Content[0].Text)
	}
}

func TestXIN2438MultipartDryRunRejectsEmptyRequiredValues(t *testing.T) {
	s := multipartDryRunServer(t)
	cases := []struct {
		op   string
		args map[string]any
	}{
		{"html.asset.add", map[string]any{"doc_id": "", "file_path": "/tmp/a.png"}},
		{"html.asset.add", map[string]any{"doc_id": nil, "file_path": "/tmp/a.png"}},
		{"html.asset.add", map[string]any{"doc_id": "..", "file_path": "/tmp/a.png"}},
		{"html.asset.add", map[string]any{"doc_id": ".", "file_path": "/tmp/a.png"}},
		{"attachment.upload", map[string]any{"X-Workspace-ID": "", "file_path": "/tmp/a.png"}},
		{"attachment.upload", map[string]any{"X-Workspace-ID": nil, "file_path": "/tmp/a.png"}},
	}
	for _, c := range cases {
		res := callTool(t, s, "execute", map[string]any{"operation_id": c.op, "dry_run": true, "arguments": c.args})
		if out := toolText(t, res); !res.IsError || out["status"] != "validation_error" {
			t.Errorf("%s %v: an empty required value must fail the dry run as it fails the real call, got %s", c.op, c.args, res.Content[0].Text)
		}
	}
	// The legitimate values still plan.
	for _, c := range []struct {
		op   string
		args map[string]any
	}{
		{"html.asset.add", map[string]any{"doc_id": "d1", "file_path": "/tmp/a.png"}},
		{"attachment.upload", map[string]any{"X-Workspace-ID": "11111111-1111-4111-8111-111111111111", "file_path": "/tmp/a.png"}},
	} {
		res := callTool(t, s, "execute", map[string]any{"operation_id": c.op, "dry_run": true, "arguments": c.args})
		if res.IsError {
			t.Errorf("%s %v must plan: %s", c.op, c.args, res.Content[0].Text)
		}
	}
}

func TestXIN2438MultipartDryRunPlacesArgumentsWhereTheyAreSent(t *testing.T) {
	s := multipartDryRunServer(t)
	res := callTool(t, s, "execute", map[string]any{"operation_id": "file.upload", "dry_run": true,
		"arguments": map[string]any{"type": "image", "path": "/folder", "file_path": "/tmp/a.png"}})
	if res.IsError {
		t.Fatalf("dry run failed: %s", res.Content[0].Text)
	}
	env, _ := toolText(t, res)["envelope"].(map[string]any)
	data, _ := env["data"].(map[string]any)
	query, _ := data["query"].(map[string]any)
	if query["type"] != "image" || query["path"] != "/folder" {
		t.Errorf("query parameters must be listed as query, got %v", data)
	}
	if form, _ := data["form_fields"].(map[string]any); len(form) != 0 {
		t.Errorf("query parameters must not be listed as form fields: %v", form)
	}

	res = callTool(t, s, "execute", map[string]any{"operation_id": "attachment.upload", "dry_run": true,
		"arguments": map[string]any{"X-Workspace-ID": "11111111-1111-4111-8111-111111111111", "task_id": "t1", "file_path": "/tmp/a.png"}})
	env, _ = toolText(t, res)["envelope"].(map[string]any)
	data, _ = env["data"].(map[string]any)
	headers, _ := data["headers"].(map[string]any)
	form, _ := data["form_fields"].(map[string]any)
	if headers["X-Workspace-ID"] == nil || form["X-Workspace-ID"] != nil || form["task_id"] != "t1" {
		t.Errorf("headers and form fields must be separated, got %v", data)
	}
}

func TestXIN2438DescribeSummaryNamesFileArgument(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	res := callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": "file.upload", "depth": "summary"})
	out := toolText(t, res)
	if out["file_argument"] != "file_path" || out["multipart"] != true {
		t.Errorf("summary must say how to pass the upload file, got %v", out)
	}
	ce, _ := out["constraint_enforcement"].(map[string]any)
	if note, _ := ce["multipart_note"].(string); !strings.Contains(note, "file_path") {
		t.Errorf("multipart note must name file_path: %q", note)
	}
}

func TestXIN2438GetSkillArgumentsAreClosed(t *testing.T) {
	s := facadeServer(t, facadeTwo)
	for _, args := range []string{
		`{"intent":"describe","operation_id":"message.send","dpeth":"summary"}`,
		`{"intent":"describe","operation_id":"message.send","Depth":"summary"}`,
		`{"intent":"describe","operation_id":"message.send","depth":"summary","depth":"full"}`,
	} {
		params := `{"name":"get_skill","arguments":` + args + `}`
		resp, _ := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(params)})
		if resp.Error == nil || resp.Error.Code != codeInvalidParams {
			t.Errorf("%s must be refused with -32602, got %+v %s", args, resp.Error, resp.Result)
		}
	}
	res := callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": "message.send", "depth": "summary"})
	if res.IsError || toolText(t, res)["depth"] != "summary" {
		t.Errorf("canonical get_skill must work: %s", res.Content[0].Text)
	}
}

func TestXIN2438TruncateRunesBoundsWork(t *testing.T) {
	if got := truncateRunes(strings.Repeat("界", 300), 200); utf8.RuneCountInString(got) != 201 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncation must keep 200 runes plus an ellipsis, got %d runes", utf8.RuneCountInString(got))
	}
	// Exactly n four-byte runes fill the byte prefix; the cut must still show.
	if got := truncateRunes(strings.Repeat("😀", 201), 200); utf8.RuneCountInString(got) != 201 || !strings.HasSuffix(got, "…") {
		t.Errorf("a cut at the byte bound must keep the ellipsis, got %q", got)
	}
	if got := truncateRunes("short", 200); got != "short" {
		t.Errorf("short input must be unchanged, got %q", got)
	}
	// Ranking always walks every operation, so compare against a name at the
	// rank cap: an 8 MiB name must cost about the same, not a copy of itself.
	reg := newTestServer(t).reg
	allocs := func(name string) uint64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		_ = truncateRunes(name, 200)
		_ = nearestOperations(reg, name)
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	base := allocs(strings.Repeat("a", maxRankQueryRunes))
	if huge := allocs(strings.Repeat("a", 8<<20)); huge > base+(1<<20) {
		t.Errorf("an 8 MiB name allocated %d bytes against %d at the cap; it must not expand the whole input", huge, base)
	}
}
