package mcp

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestCommandWords(t *testing.T) {
	cases := []struct {
		id      string
		service string
		want    []string
	}{
		{"docs.create", "docs", []string{"docs", "create"}},
		{"message.send", "message", []string{"message", "send"}},
		{"task.comment.create", "loop", []string{"loop", "task", "comment", "create"}},
		{"mcp.probe", "marketplace", []string{"marketplace", "mcp", "probe"}},
		{"drive.im-transfer.create", "drive", []string{"drive", "im-transfer", "create"}},
	}
	for _, c := range cases {
		d := &registry.OperationDetail{OperationInfo: registry.OperationInfo{ID: c.id, Service: c.service}}
		got := commandWords(d)
		if strings.Join(got, " ") != strings.Join(c.want, " ") {
			t.Errorf("commandWords(%s) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestBuildArgv_SplitsParamsByPosition(t *testing.T) {
	// A synthetic op: one path param, one query param (with x-octo-flag), a
	// header param, and a request body. Body fields must go via --data; query /
	// header via their flags; the path param becomes a positional after "--".
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "docs.comments.add", Service: "docs", Method: "POST", Path: "/v1/bot/docs/{doc_id}/comments"},
		Parameters: []registry.ParamInfo{
			{Name: "locale", In: "query"},
			{Name: "If-Match", In: "header", FlagName: "base-version"},
		},
		RequestBody: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{"text": {Type: "string"}}},
	}
	args := map[string]any{
		"doc_id":   "D123",
		"locale":   "en",
		"If-Match": "v7",
		"text":     "hello",
	}
	argv, err := buildArgv(d, args)
	if err != nil {
		t.Fatalf("buildArgv: %v", err)
	}
	joined := strings.Join(argv, " ")
	if !strings.HasPrefix(joined, "docs comments add ") {
		t.Errorf("argv should start with command words, got %q", joined)
	}
	assertHas(t, argv, "--locale=en")
	assertHas(t, argv, "--base-version=v7") // header flag uses x-octo-flag name
	// Body field must be carried as JSON --data, not a bare flag.
	if !hasPrefix(argv, "--data=") {
		t.Errorf("body field must go through --data, argv=%v", argv)
	}
	// Path value must be a positional AFTER a -- separator (so a leading-dash id
	// is never parsed as a flag).
	sep := indexOf(argv, "--")
	if sep < 0 || indexOf(argv, "D123") < sep {
		t.Errorf("path value must appear after the -- separator, argv=%v", argv)
	}
}

func TestBuildArgv_UnexpectedArgumentForBodylessOp(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "event.list", Service: "event", Method: "GET", Path: "/v1/bot/events"},
	}
	_, err := buildArgv(d, map[string]any{"bogus": "x"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("expected unexpected-argument error, got %v", err)
	}
}

func TestBuildArgv_MissingPathArg(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "docs.get", Service: "docs", Method: "GET", Path: "/v1/bot/docs/{doc_id}"},
	}
	_, err := buildArgv(d, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "missing required path argument") {
		t.Errorf("expected missing-path-arg error, got %v", err)
	}
}

func assertHas(t *testing.T, argv []string, want string) {
	t.Helper()
	for _, a := range argv {
		if a == want {
			return
		}
	}
	t.Errorf("argv %v missing %q", argv, want)
}

func hasPrefix(argv []string, prefix string) bool {
	for _, a := range argv {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func indexOf(argv []string, want string) int {
	for i, a := range argv {
		if a == want {
			return i
		}
	}
	return -1
}
