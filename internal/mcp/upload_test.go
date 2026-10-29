package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

func uploadFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root = t.TempDir()
	outside = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{filepath.Join(root, "sub", "f.txt"): "CONFINED-CONTENT", filepath.Join(outside, "f.txt"): "OUTSIDE-SECRET"} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

func swapUploadPath(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Rename(path, path+".kept"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenUpload_NoFollow(t *testing.T) {
	root, outside := uploadFixture(t)
	for _, tc := range []struct{ name, target string }{
		{"outside-file", filepath.Join(outside, "f.txt")},
		{"inside-file", filepath.Join(root, "sub", "f.txt")},
		{"outside-directory", outside},
		{"inside-directory", filepath.Join(root, "sub")},
	} {
		link := filepath.Join(root, tc.name)
		if err := os.Symlink(tc.target, link); err != nil {
			t.Fatal(err)
		}
		name := tc.name
		if strings.Contains(tc.name, "directory") {
			name = filepath.Join(name, "f.txt")
		}
		for _, httpMode := range []bool{false, true} {
			file, err := openUpload(name, execPolicy{httpMode: httpMode, uploadRoot: root})
			if err == nil {
				file.Close()
				t.Fatalf("symlink accepted: %s http=%v", name, httpMode)
			}
		}
	}
}

func TestOpenUpload_StalePathSwapRejected(t *testing.T) {
	for _, kind := range []string{"file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := uploadFixture(t)
			policy := execPolicy{uploadRoot: root}
			if _, err := resolveUploadPath("sub/f.txt", policy); err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				swapUploadPath(t, filepath.Join(root, "sub", "f.txt"), filepath.Join(outside, "f.txt"))
			} else {
				swapUploadPath(t, filepath.Join(root, "sub"), outside)
			}
			file, err := openUpload("sub/f.txt", policy)
			if err == nil {
				file.Close()
				t.Fatal("validated name swapped before open must be rejected")
			}
		})
	}
}

func TestOpenUpload_DirectorySwapDuringWalk(t *testing.T) {
	root, outside := uploadFixture(t)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := openUploadRoot(resolved)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	swapped := false
	file, err := walkUpload(dir, filepath.Join("sub", "f.txt"), func(parent *os.File, name string, directory bool) (*os.File, error) {
		opened, err := openUploadComponent(parent, name, directory)
		if err == nil && directory && name == "sub" {
			swapUploadPath(t, filepath.Join(root, "sub"), outside)
			swapped = true
		}
		return opened, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if !swapped || string(data) != "CONFINED-CONTENT" {
		t.Fatalf("directory handle redirected: %q", data)
	}
}

func TestOpenUpload_RegularFileAndOptIn(t *testing.T) {
	root, outside := uploadFixture(t)
	for _, p := range []string{"sub", "sub/../sub/f.txt", filepath.Join(outside, "f.txt")} {
		file, err := openUpload(p, execPolicy{uploadRoot: root})
		if err == nil {
			file.Close()
			t.Fatalf("invalid path accepted: %s", p)
		}
	}
	for _, name := range []string{"sub/f.txt", filepath.Join(root, "sub", "f.txt")} {
		file, err := openUpload(name, execPolicy{uploadRoot: root})
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(data) != "CONFINED-CONTENT" {
			t.Fatalf("valid upload: %q %v", data, err)
		}
	}
	file, err := openUpload(filepath.Join(outside, "f.txt"), execPolicy{allowLocalUpload: true})
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if file, err := openUpload(filepath.Join(outside, "f.txt"), execPolicy{httpMode: true, allowLocalUpload: true}); err == nil {
		file.Close()
		t.Fatal("HTTP opt-in must stay fail-closed")
	}
}

func TestUpload_PinnedHandleReachesMultipart(t *testing.T) {
	for _, tool := range []string{"call_op", "execute"} {
		for _, transport := range []string{"stdio", "http"} {
			for _, swap := range []string{"none", "file", "directory", "root"} {
				t.Run(tool+"/"+transport+"/"+swap, func(t *testing.T) {
					root, outside := uploadFixture(t)
					received := make(chan string, 1)
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						mr, err := r.MultipartReader()
						if err != nil {
							http.Error(w, err.Error(), 400)
							return
						}
						part, err := mr.NextPart()
						if err != nil {
							http.Error(w, err.Error(), 400)
							return
						}
						data, err := io.ReadAll(part)
						if err != nil {
							http.Error(w, err.Error(), 400)
							return
						}
						received <- part.FileName() + ":" + string(data)
						w.Header().Set("Content-Type", "application/json")
						w.Write([]byte(`{"file_id":"1"}`))
					}))
					defer backend.Close()
					t.Setenv("OCTO_API_BASE_URL", backend.URL)
					var held *os.File
					build := func(f *cmdutil.Factory) *cobra.Command {
						held = f.MultipartFile
						switch swap {
						case "file":
							swapUploadPath(t, filepath.Join(root, "sub", "f.txt"), filepath.Join(outside, "f.txt"))
						case "directory":
							swapUploadPath(t, filepath.Join(root, "sub"), outside)
						case "root":
							swapUploadPath(t, root, outside)
						}
						return testRoot(f)
					}
					args := map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file_path": "sub/f.txt"}}
					var raw []byte
					if transport == "stdio" {
						s := newTestServer(t).WithFacade(facadeBoth)
						s.build = build
						s.factoryFn = fakeBackendFactory(backend.URL, "bf_test")
						s.WithUploadPolicy(root, false)
						params, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
						request, _ := json.Marshal(rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
						var out bytes.Buffer
						if err := s.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
							t.Fatal(err)
						}
						raw = out.Bytes()
					} else {
						t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
						h, err := NewHTTPHandler(build, TrustedContext{}, cmdutil.GlobalOptions{}, facadeBoth)
						if err != nil {
							t.Fatal(err)
						}
						params, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
						request, _ := json.Marshal(rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
						req := httptest.NewRequest("POST", "/", bytes.NewReader(request))
						req.Header.Set("Authorization", "Bearer bf_test")
						out := httptest.NewRecorder()
						h.ServeHTTP(out, req)
						raw = out.Body.Bytes()
					}
					res := decodeSearchResponse(t, raw)
					if res.IsError {
						t.Fatalf("confined original must remain uploadable: %s", res.Content[0].Text)
					}
					select {
					case got := <-received:
						if got != "f.txt:CONFINED-CONTENT" {
							t.Fatalf("upload leaked/switched bytes: %q", got)
						}
					default:
						t.Fatal("upload did not reach backend")
					}
					if held == nil {
						t.Fatal("no pinned descriptor passed to the engine")
					}
					if _, err := held.Stat(); err == nil {
						t.Fatal("descriptor not released after execution")
					}
				})
			}
		}
	}
}

func TestUpload_NullFilePathCannotReopenName(t *testing.T) {
	root, outside := uploadFixture(t)
	if err := os.Symlink(filepath.Join(outside, "f.txt"), filepath.Join(root, "<nil>")); err != nil {
		t.Fatal(err)
	}
	be := &backend{}
	backend := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_test")
	s.WithUploadPolicy(root, false)
	res := callTool(t, s, "call_op", map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file_path": nil}})
	if !res.IsError || be.path != "" {
		t.Fatalf("null must be omitted and never reopened: %s", res.Content[0].Text)
	}
}

func TestUpload_DescriptorClosedOnExecutionError(t *testing.T) {
	root, _ := uploadFixture(t)
	s := newTestServer(t)
	s.WithUploadPolicy(root, false)
	var held *os.File
	var factory *cmdutil.Factory
	s.build = func(f *cmdutil.Factory) *cobra.Command {
		factory = f
		held = f.MultipartFile
		return &cobra.Command{Use: "probe", RunE: func(_ *cobra.Command, _ []string) error { return errors.New("execution failed") }}
	}
	res := callTool(t, s, "call_op", map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file_path": "sub/f.txt"}})
	if !res.IsError || held == nil {
		t.Fatal("execution should fail after opening")
	}
	if _, err := held.Stat(); err == nil || factory.MultipartFile != nil {
		t.Fatal("failed execution must close and clear its descriptor")
	}
}

func TestOpenUpload_StaleRootSwapRejected(t *testing.T) {
	root, outside := uploadFixture(t)
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("CONFINED-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := execPolicy{uploadRoot: root}
	if _, err := resolveUploadPath("f.txt", policy); err != nil {
		t.Fatal(err)
	}
	swapUploadPath(t, root, outside)
	file, err := openUpload("f.txt", policy)
	if err == nil {
		file.Close()
		t.Fatal("configured root replaced by a symlink must be rejected")
	}
}
