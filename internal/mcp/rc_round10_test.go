package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestRound10ResourceErrorsDoNotReflectUnboundedInput(t *testing.T) {
	s := newTestServer(t)
	large := strings.Repeat("界", 4096)
	for _, uri := range []string{
		"file:///" + large,
		"octo://skills/" + large,
		"octo://skills/" + large + "/SKILL.md",
		"octo://skills/octo-messaging/" + large + ".md",
	} {
		_, err := s.readResource(uri)
		if err == nil || len(err.Error()) > 2048 || strings.Contains(err.Error(), large) {
			t.Fatalf("resource error reflected unbounded input: %v", err)
		}
	}

	method := strings.Repeat("m", maxMCPMessageBytes-512)
	resp, has := s.Dispatch(context.Background(), rpcRequest{Jsonrpc: jsonrpcVersion, ID: json.RawMessage(`1`), Method: method})
	if !has || resp.Error == nil || resp.Error.Code != codeMethodNotFound || len(resp.Error.Message) > 256 {
		t.Fatalf("unknown method response was not bounded: has=%v error=%+v", has, resp.Error)
	}
}

func TestRound10HTTPResourceErrorBoundedAtRequestLimit(t *testing.T) {
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", "")
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"jsonrpc": jsonrpcVersion,
		"id":      1,
		"method":  "resources/read",
		"params":  map[string]any{"uri": "octo://skills/octo-messaging/" + strings.Repeat("x", maxMCPMessageBytes-512)},
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) > maxMCPMessageBytes {
		t.Fatalf("test request exceeds HTTP limit: %d %v", len(encoded), err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/", bytes.NewReader(encoded)))
	var resp rpcResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("unexpected resource error response: status=%d body=%s error=%v", w.Code, truncateRunes(w.Body.String(), 200), err)
	}
	if len(w.Body.Bytes()) > 2048 {
		t.Fatalf("oversized resource error response: %d bytes", len(w.Body.Bytes()))
	}
}

func TestRound10DefaultCredentialDirectoryCannotBeUploadRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OCTO_CONFIG_DIR", "")
	private := filepath.Join(home, ".octo-cli")
	privateChild := filepath.Join(private, "uploads")
	staging := filepath.Join(home, "uploads")
	for _, path := range []string{private, privateChild, staging} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		markUploadRoot(t, path)
	}
	if err := validateHTTPUploadRoot(private); err == nil {
		t.Fatal("default credential directory was accepted as upload root")
	}
	if err := validateHTTPUploadRoot(privateChild); err == nil {
		t.Fatal("directory inside the default credential directory was accepted as upload root")
	}
	if err := validateHTTPUploadRoot(staging); err != nil {
		t.Fatalf("dedicated staging directory was refused: %v", err)
	}
}

func TestRound10FilesystemRootRejectedOnStdio(t *testing.T) {
	root := string(filepath.Separator)
	if _, _, err := uploadLocation("etc/passwd", execPolicy{uploadRoot: root}); err == nil {
		t.Fatal("stdio accepted the filesystem root as an upload root")
	}
}

type round10FailWriter struct{ calls int }

func (w *round10FailWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("stdout closed")
}

func TestRound10StdioStopsAfterWriteFailure(t *testing.T) {
	s := newTestServer(t)
	w := &round10FailWriter{}
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n")
	if err := s.ServeStdio(context.Background(), in, w); err == nil || err.Error() != "stdout closed" {
		t.Fatalf("stdio did not propagate stdout failure: %v", err)
	}
	if w.calls != 1 {
		t.Fatalf("stdio continued after failed response write: %d writes", w.calls)
	}
}

type round10ObservedReader struct {
	*io.PipeReader
	entered chan struct{}
	once    sync.Once
}

func (r *round10ObservedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	return r.PipeReader.Read(p)
}

func TestRound10StdioCancellationUnblocksInput(t *testing.T) {
	s := newTestServer(t)
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	in := &round10ObservedReader{PipeReader: reader, entered: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.ServeStdio(ctx, in, io.Discard) }()
	select {
	case <-in.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("stdio did not begin reading")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stdio cancellation returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stdio did not stop after cancellation")
	}
}
