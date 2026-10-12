package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

// --- multipart operations are callable from discovery ---

type uploadRPCConfig struct {
	root             string // OCTO_MCP_UPLOAD_ROOT ("" => unset)
	allowLocalUpload bool   // stdio --allow-local-upload
	noResources      bool   // client did not negotiate resources (stdio only)
}

// uploadRPC sends one tools/call over the real stdio loop or the real HTTP
// handler (with a bearer) and returns the decoded tool result.
func uploadRPC(t *testing.T, transport, backendURL string, cfg uploadRPCConfig, tool string, args map[string]any) toolResult {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": tool, "arguments": args})
	request, _ := json.Marshal(rpcRequest{Jsonrpc: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	if transport == "stdio" {
		s := newTestServer(t)
		s.factoryFn = fakeBackendFactory(backendURL, "bf_test")
		s.WithUploadPolicy(cfg.root, cfg.allowLocalUpload)
		s.clientResources = !cfg.noResources
		var out bytes.Buffer
		if err := s.ServeStdio(context.Background(), bytes.NewReader(append(request, '\n')), &out); err != nil {
			t.Fatal(err)
		}
		return decodeSearchResponse(t, out.Bytes())
	}
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", cfg.root)
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/", bytes.NewReader(request))
	req.Header.Set("Authorization", "Bearer bf_test")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return decodeSearchResponse(t, rec.Body.Bytes())
}

func describeUpload(t *testing.T, transport string, cfg uploadRPCConfig, opID string) map[string]any {
	t.Helper()
	res := uploadRPC(t, transport, "http://127.0.0.1:1", cfg, "describe_op", map[string]any{"operation_id": opID})
	if res.IsError {
		t.Fatalf("describe_op %s failed: %s", opID, res.Content[0].Text)
	}
	return toolText(t, res)
}

func uploadArgOf(t *testing.T, desc map[string]any) map[string]any {
	t.Helper()
	args, ok := desc["mcp_arguments"].(map[string]any)
	if !ok {
		t.Fatalf("multipart describe_op must expose mcp_arguments, got keys %v", keysOf(desc))
	}
	fp, ok := args["file_path"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_arguments must contain file_path, got %v", args)
	}
	return fp
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestDescribeOp_MultipartAdvertisesFilePath: for every multipart operation the
// response advertises file_path (required, accurate upload mode), marks the
// raw binary "file" property server-managed, and no longer lists it as
// required — so following describe_op cannot lead into the reserved-flag
// refusal.
func TestDescribeOp_MultipartAdvertisesFilePath(t *testing.T) {
	for _, opID := range multipartUploadOps {
		for _, transport := range []string{"stdio", "http"} {
			desc := describeUpload(t, transport, uploadRPCConfig{root: t.TempDir()}, opID)
			fp := uploadArgOf(t, desc)
			if fp["required"] != true || fp["type"] != "string" || fp["upload_mode"] != "confined" {
				t.Errorf("%s/%s: file_path must be a required string in confined mode, got %v", opID, transport, fp)
			}
			if d, _ := fp["description"].(string); !strings.Contains(d, "upload root") {
				t.Errorf("%s/%s: confined description must mention the upload root, got %q", opID, transport, d)
			}
			managed, _ := desc["server_managed_arguments"].([]any)
			if !slices.Contains(managed, any("file")) {
				t.Errorf("%s/%s: raw file must be server-managed, got %v", opID, transport, managed)
			}
			rb := desc["request_body"].(map[string]any)
			file := rb["properties"].(map[string]any)["file"].(map[string]any)
			if file["server_managed"] != true || !strings.Contains(file["description"].(string), "file_path") {
				t.Errorf("%s/%s: raw file property must be annotated server-managed, got %v", opID, transport, file)
			}
			if req, _ := rb["required"].([]any); slices.Contains(req, any("file")) {
				t.Errorf("%s/%s: raw file must not be advertised as required, got %v", opID, transport, req)
			}
		}
	}
	// attachment.upload keeps its other, settable body fields.
	desc := describeUpload(t, "stdio", uploadRPCConfig{root: t.TempDir()}, "attachment.upload")
	props := desc["request_body"].(map[string]any)["properties"].(map[string]any)
	for _, k := range []string{"task_id", "comment_id"} {
		if p, _ := props[k].(map[string]any); p == nil || p["server_managed"] != nil {
			t.Errorf("attachment.upload %s must remain a settable body field, got %v", k, props[k])
		}
	}
}

// TestDescribeOp_UploadModeMatchesPolicy: the advertised mode mirrors what
// call_op will enforce on each transport/posture.
func TestDescribeOp_UploadModeMatchesPolicy(t *testing.T) {
	cases := []struct {
		name, transport string
		cfg             uploadRPCConfig
		want            string
	}{
		{"stdio default-deny", "stdio", uploadRPCConfig{}, "disabled"},
		{"stdio opt-in", "stdio", uploadRPCConfig{allowLocalUpload: true}, "unconfined"},
		{"stdio confined", "stdio", uploadRPCConfig{root: t.TempDir()}, "confined"},
		{"http default-deny", "http", uploadRPCConfig{}, "disabled"},
		{"http confined", "http", uploadRPCConfig{root: t.TempDir()}, "confined"},
	}
	for _, tc := range cases {
		fp := uploadArgOf(t, describeUpload(t, tc.transport, tc.cfg, "file.upload"))
		if fp["upload_mode"] != tc.want {
			t.Errorf("%s: upload_mode=%v want %s", tc.name, fp["upload_mode"], tc.want)
		}
		if d, _ := fp["description"].(string); tc.want == "disabled" && !strings.Contains(d, "OCTO_MCP_UPLOAD_ROOT") {
			t.Errorf("%s: disabled description must say how to enable uploads, got %q", tc.name, d)
		}
	}
	// HTTP never honours the stdio opt-in.
	if got := uploadMode(execPolicy{httpMode: true, allowLocalUpload: true}); got != "disabled" {
		t.Errorf("http + allowLocalUpload must advertise disabled, got %s", got)
	}
}

// TestDescribeOp_NonMultipartHasNoUploadArgument keeps the splice scoped.
func TestDescribeOp_NonMultipartHasNoUploadArgument(t *testing.T) {
	desc := toolText(t, callTool(t, newTestServer(t), "describe_op", map[string]any{"operation_id": "message.send"}))
	if _, has := desc["mcp_arguments"]; has {
		t.Errorf("non-multipart operation must not advertise mcp_arguments")
	}
	if _, has := desc["server_managed_arguments"]; has {
		t.Errorf("unforced non-multipart operation must not list server-managed arguments")
	}
}

// TestDescribeOp_MultipartWithoutResources: the degraded (no MCP resources)
// navigation path still carries the upload contract alongside the hint.
func TestDescribeOp_MultipartWithoutResources(t *testing.T) {
	for _, opID := range multipartUploadOps {
		desc := describeUpload(t, "stdio", uploadRPCConfig{root: t.TempDir(), noResources: true}, opID)
		uploadArgOf(t, desc)
		skill, _ := desc["skill"].(map[string]any)
		if skill == nil {
			t.Fatalf("%s: skill navigation missing", opID)
		}
		if h, _ := skill["hint"].(string); !strings.Contains(h, "no MCP resources support") {
			t.Errorf("%s: degraded client must get the readable hint, got %v", opID, skill)
		}
	}
}

// TestUpload_DiscoveryToCall follows only what describe_op advertises —
// mcp_arguments plus required path/header parameters — and proves the call
// delivers the confined file's bytes on both transports for all three ops.
func TestUpload_DiscoveryToCall(t *testing.T) {
	for _, opID := range multipartUploadOps {
		for _, transport := range []string{"stdio", "http"} {
			t.Run(opID+"/"+transport, func(t *testing.T) {
				root := t.TempDir()
				if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("CONFINED-BYTES"), 0o644); err != nil {
					t.Fatal(err)
				}
				received := make(chan string, 1)
				backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mr, err := r.MultipartReader()
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					for {
						part, err := mr.NextPart()
						if err != nil {
							break
						}
						if part.FormName() == "file" {
							data, _ := io.ReadAll(part)
							received <- part.FileName() + ":" + string(data)
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"id":"1"}}`))
				}))
				defer backend.Close()
				t.Setenv("OCTO_API_BASE_URL", backend.URL)
				cfg := uploadRPCConfig{root: root}

				desc := describeUpload(t, transport, cfg, opID)
				args := map[string]any{}
				for name := range desc["mcp_arguments"].(map[string]any) {
					args[name] = "a.txt"
				}
				params, _ := desc["parameters"].([]any)
				for _, p := range params {
					pm := p.(map[string]any)
					if pm["required"] == true {
						args[pm["name"].(string)] = "11111111-1111-1111-1111-111111111111"
					}
				}
				if req, _ := desc["request_body"].(map[string]any)["required"].([]any); len(req) > 0 {
					t.Fatalf("describe_op still advertises required body fields the caller cannot supply: %v", req)
				}

				res := uploadRPC(t, transport, backend.URL, cfg, "call_op", map[string]any{"operation_id": opID, "arguments": args})
				if res.IsError {
					t.Fatalf("call following describe_op must succeed: %s", res.Content[0].Text)
				}
				select {
				case got := <-received:
					if got != "a.txt:CONFINED-BYTES" {
						t.Fatalf("unexpected uploaded part %q", got)
					}
				default:
					t.Fatal("upload did not reach the backend")
				}
			})
		}
	}
}
