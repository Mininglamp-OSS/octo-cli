package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- multipart file_path capability boundary (confined on BOTH transports) ---

// TestCallOp_StdioUploadDeniedByDefault is the real-path proof for the P1: on
// the default stdio transport, with no upload root and no opt-in, a model
// file_path is refused before any request reaches the backend.
func TestCallOp_StdioUploadDeniedByDefault(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t) // stdio: httpMode=false, no uploadRoot, no allowLocalUpload
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "file.upload",
		"arguments":    map[string]any{"file_path": "/etc/passwd"},
	})
	if !res.IsError {
		t.Fatalf("stdio file.upload with an arbitrary path must be refused, got %s", res.Content[0].Text)
	}
	if !strings.Contains(res.Content[0].Text, "disabled") {
		t.Errorf("expected the upload-disabled error, got %s", res.Content[0].Text)
	}
	if be.path != "" {
		t.Errorf("no request may reach the backend on a denied upload, path=%q", be.path)
	}
}

// TestCallOp_StdioUploadConfinedAllows is the legitimate direction: a file
// inside OCTO_MCP_UPLOAD_ROOT uploads on stdio and reaches the backend.
func TestCallOp_StdioUploadConfinedAllows(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	be := &backend{reply: `{"file_id":"1"}`}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.WithUploadPolicy(root, false)

	res := callTool(t, s, "call_op", map[string]any{
		"operation_id": "file.upload",
		"arguments":    map[string]any{"file_path": "f.txt"},
	})
	if res.IsError {
		t.Fatalf("a file inside the confined root must upload: %s", res.Content[0].Text)
	}
	if be.path != "/v1/bot/file/upload" {
		t.Errorf("confined upload must reach the backend, path=%q", be.path)
	}
}

func TestResolveUploadPath_DefaultDeniedOnBothTransports(t *testing.T) {
	// No upload root, no opt-in: refused on stdio AND HTTP so a model cannot read
	// an arbitrary local file on either transport.
	for _, httpMode := range []bool{false, true} {
		_, err := resolveUploadPath("/etc/passwd", execPolicy{httpMode: httpMode})
		if err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Errorf("httpMode=%v default-deny expected, got %v", httpMode, err)
		}
	}
}

func TestResolveUploadPath_StdioAllowLocalUploadOptIn(t *testing.T) {
	// The operator opt-in (stdio only) restores unconfined pass-through.
	got, err := resolveUploadPath("/anything/goes.txt", execPolicy{httpMode: false, allowLocalUpload: true})
	if err != nil || got != "/anything/goes.txt" {
		t.Errorf("stdio --allow-local-upload must pass the path through, got (%q,%v)", got, err)
	}
	// The opt-in has NO effect on HTTP: HTTP never allows unconfined uploads.
	if _, err := resolveUploadPath("/etc/passwd", execPolicy{httpMode: true, allowLocalUpload: true}); err == nil {
		t.Errorf("--allow-local-upload must not open HTTP uploads")
	}
}

func TestResolveUploadPath_Containment(t *testing.T) {
	// Confinement is identical on both transports: only the resolved root wins.
	for _, httpMode := range []bool{false, true} {
		root := t.TempDir()
		inside := filepath.Join(root, "ok.txt")
		if err := os.WriteFile(inside, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := resolveUploadPath("ok.txt", execPolicy{httpMode: httpMode, uploadRoot: root})
		if err != nil {
			t.Fatalf("httpMode=%v: file inside the root must be allowed: %v", httpMode, err)
		}
		if resolvedRoot, _ := filepath.EvalSymlinks(root); !strings.HasPrefix(got, resolvedRoot) {
			t.Errorf("httpMode=%v: resolved path %q must live under the root %q", httpMode, got, resolvedRoot)
		}
		// Traversal out of the root is refused.
		if _, err := resolveUploadPath("../escape.txt", execPolicy{httpMode: httpMode, uploadRoot: root}); err == nil {
			t.Errorf("httpMode=%v: traversal must be refused", httpMode)
		}
		// An absolute path outside the root is refused.
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := resolveUploadPath(outside, execPolicy{httpMode: httpMode, uploadRoot: root}); err == nil {
			t.Errorf("httpMode=%v: absolute path outside the root must be refused", httpMode)
		}
		// A symlink inside the root pointing outside is refused (real target escapes).
		link := filepath.Join(root, "link.txt")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("symlink unsupported here: %v", err)
		}
		if _, err := resolveUploadPath("link.txt", execPolicy{httpMode: httpMode, uploadRoot: root}); err == nil {
			t.Errorf("httpMode=%v: symlink escaping the root must be refused", httpMode)
		}
	}
}

func TestBuildArgv_MultipartFilePathConfinement(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "file.upload", Service: "file", Method: "POST", Path: "/v1/bot/files"},
		Multipart:     true,
	}
	// Default-deny on HTTP and stdio alike.
	for _, httpMode := range []bool{false, true} {
		if _, err := buildArgv(d, map[string]any{"file_path": "/etc/passwd"}, execPolicy{httpMode: httpMode}); err == nil {
			t.Errorf("httpMode=%v: multipart file_path with no upload root must be rejected", httpMode)
		}
	}
	// stdio with the operator opt-in works.
	argv, err := buildArgv(d, map[string]any{"file_path": "/tmp/x"}, execPolicy{allowLocalUpload: true})
	if err != nil || !hasPrefix(argv, "--file=") {
		t.Errorf("stdio --allow-local-upload multipart upload must work, argv=%v err=%v", argv, err)
	}
}

// --- multipart argument keys must not become arbitrary/root flags ---

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
		_, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", k: "evil"}, execPolicy{allowLocalUpload: true})
		if err == nil {
			t.Errorf("multipart key %q must be rejected, was accepted", k)
		}
	}
	// A non-reserved but UNDECLARED key must also be rejected (isolates the
	// declared-only guard, independent of the reserved-flag guard).
	if _, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", "arbitrary_extra": "x"}, execPolicy{allowLocalUpload: true}); err == nil {
		t.Errorf("undeclared multipart key must be rejected")
	}
	// A declared body field still works and rides as its own flag.
	argv, err := buildArgv(d, map[string]any{"file_path": "/tmp/f", "caption": "hello"}, execPolicy{allowLocalUpload: true})
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
