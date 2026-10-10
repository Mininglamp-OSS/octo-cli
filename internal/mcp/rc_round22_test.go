package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound22MCPWriteMakesOneAttemptAndReportsUnknown(t *testing.T) {
	for _, failure := range []string{"gateway-502", "connection-dropped"} {
		t.Run(failure, func(t *testing.T) {
			var attempts atomic.Int32
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts.Add(1) // simulate a committed write whose response is lost
				if failure == "connection-dropped" {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.WriteHeader(http.StatusBadGateway)
			}))
			defer backend.Close()
			s := newTestServer(t)
			s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
			result := callTool(t, s, "call_op", map[string]any{
				"operation_id": "message.send",
				"arguments": map[string]any{"channel_id": "fixture", "channel_type": 2,
					"payload": map[string]any{"type": 1, "content": "once"}},
			})
			if !result.IsError || attempts.Load() != 1 || !strings.Contains(result.Content[0].Text, "RESULT_UNKNOWN") {
				t.Fatalf("possibly committed message must not be retried: attempts=%d result=%s", attempts.Load(), result.Content[0].Text)
			}
		})
	}
}

func TestRound22HTTPUploadRootAdmissionAndPathPrivacy(t *testing.T) {
	for _, root := range []string{"/", "/etc", t.TempDir()} {
		t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
		if _, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}); err == nil {
			t.Fatalf("unsafe or unmarked HTTP upload root accepted: %q", root)
		}
		if _, err := openUpload("fixture.txt", execPolicy{httpMode: true, uploadRoot: root}); err == nil {
			t.Fatalf("upload root admitted on execution: %q", root)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".octo-mcp-upload-root"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
	if _, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}); err != nil {
		t.Fatalf("dedicated staging root rejected: %v", err)
	}
	if _, err := openUpload("missing.txt", execPolicy{httpMode: true, uploadRoot: root}); err == nil || strings.Contains(err.Error(), root) {
		t.Fatalf("filesystem path leaked to HTTP caller: %v", err)
	}
}
