package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- B2: multipart file_path capability boundary ---

func TestResolveUploadPath_StdioPassthrough(t *testing.T) {
	got, err := resolveUploadPath("/anything/goes.txt", execPolicy{httpMode: false})
	if err != nil || got != "/anything/goes.txt" {
		t.Errorf("stdio must pass the path through, got (%q,%v)", got, err)
	}
}

func TestResolveUploadPath_HTTPDefaultDenied(t *testing.T) {
	_, err := resolveUploadPath("/etc/passwd", execPolicy{httpMode: true, uploadRoot: ""})
	if err == nil || !strings.Contains(err.Error(), "disabled for HTTP") {
		t.Errorf("HTTP with no upload root must deny local upload, got %v", err)
	}
}

func TestResolveUploadPath_HTTPContainment(t *testing.T) {
	root := t.TempDir()
	// A legitimate file inside the root succeeds.
	inside := filepath.Join(root, "ok.txt")
	if err := os.WriteFile(inside, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveUploadPath("ok.txt", execPolicy{httpMode: true, uploadRoot: root})
	if err != nil {
		t.Fatalf("file inside the root must be allowed: %v", err)
	}
	if resolvedRoot, _ := filepath.EvalSymlinks(root); !strings.HasPrefix(got, resolvedRoot) {
		t.Errorf("resolved path %q must live under the root %q", got, resolvedRoot)
	}

	// Traversal out of the root is refused.
	if _, err := resolveUploadPath("../escape.txt", execPolicy{httpMode: true, uploadRoot: root}); err == nil {
		t.Errorf("traversal must be refused")
	}
	// An absolute path outside the root is refused.
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveUploadPath(outside, execPolicy{httpMode: true, uploadRoot: root}); err == nil {
		t.Errorf("absolute path outside the root must be refused")
	}
	// A symlink inside the root pointing outside is refused (real target escapes).
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	if _, err := resolveUploadPath("link.txt", execPolicy{httpMode: true, uploadRoot: root}); err == nil {
		t.Errorf("symlink escaping the root must be refused")
	}
}

func TestBuildArgv_MultipartFilePathDeniedOverHTTP(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "file.upload", Service: "file", Method: "POST", Path: "/v1/bot/files"},
		Multipart:     true,
	}
	if _, err := buildArgv(d, map[string]any{"file_path": "/etc/passwd"}, execPolicy{httpMode: true}); err == nil {
		t.Errorf("multipart file_path over HTTP with no upload root must be rejected")
	}
	// stdio still works.
	argv, err := buildArgv(d, map[string]any{"file_path": "/tmp/x"}, execPolicy{})
	if err != nil || !hasPrefix(argv, "--file=") {
		t.Errorf("stdio multipart upload must work, argv=%v err=%v", argv, err)
	}
}

// --- B3: multipart argument keys must not become arbitrary/root flags ---

func multipartOp() *registry.OperationDetail {
	return &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "file.upload", Service: "file", Method: "POST", Path: "/v1/bot/files"},
		Multipart:     true,
		RequestBody: &registry.SchemaInfo{
			Type:       "object",
			Properties: map[string]registry.SchemaInfo{"caption": {Type: "string"}},
		},
	}
}

func TestBuildArgv_MultipartRejectsUndeclaredAndReservedKeys(t *testing.T) {
	d := multipartOp()
	// Identity/scope/output flags must never be injectable through multipart keys.
	for _, k := range []string{"profile", "bot_id", "space", "verbose", "format", "data"} {
		_, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", k: "evil"}, execPolicy{})
		if err == nil {
			t.Errorf("multipart key %q must be rejected, was accepted", k)
		}
	}
	// A non-reserved but UNDECLARED key must also be rejected (isolates the
	// declared-only guard, independent of the reserved-flag guard).
	if _, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", "arbitrary_extra": "x"}, execPolicy{}); err == nil {
		t.Errorf("undeclared multipart key must be rejected")
	}
	// A declared body field still works and rides as its own flag.
	argv, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", "caption": "hello"}, execPolicy{})
	if err != nil {
		t.Fatalf("declared multipart field must be accepted: %v", err)
	}
	if !hasArg(argv, "--caption=hello") {
		t.Errorf("declared field should map to its flag, argv=%v", argv)
	}
}

func TestBuildArgv_NonMultipartBodyGoesToDataNotFlags(t *testing.T) {
	// For a JSON-body op, an unexpected-looking key is carried inside --data
	// (validated by the engine), never turned into a --profile flag.
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "docs.create", Service: "docs", Method: "POST", Path: "/v1/bot/docs"},
		RequestBody:   &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{"title": {Type: "string"}}},
	}
	argv, err := buildArgv(d, map[string]any{"profile": "evil", "title": "t"}, execPolicy{})
	if err != nil {
		t.Fatalf("json-body op should accept body keys via --data: %v", err)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "--profile") {
			t.Errorf("a body key must never become a --profile flag, argv=%v", argv)
		}
	}
	if !hasPrefix(argv, "--data=") {
		t.Errorf("body must be carried as --data, argv=%v", argv)
	}
}

func hasArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}
