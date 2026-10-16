package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func lifecycleHTTPHandler(t *testing.T) *HTTPHandler {
	t.Helper()
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeBoth)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func lifecycleUploadBody(tool string, padding int) []byte {
	data, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name": tool, "_meta": map[string]any{"fixture_padding": strings.Repeat("x", padding)},
			"arguments": map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file_path": "fixture.txt"}},
		},
	})
	return data
}

func lifecycleRequest(ctx context.Context, endpoint string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer bf_fixture")
	return http.DefaultClient.Do(req)
}

func waitLifecycle(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !ready() {
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestHTTPLifecycleLargeUploadAdmission(t *testing.T) {
	for _, tool := range []string{"call_op", "execute"} {
		t.Run(tool, func(t *testing.T) { testLifecycleLargeUploadAdmission(t, tool) })
	}
}

func testLifecycleLargeUploadAdmission(t *testing.T, tool string) {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string][]byte{".octo-mcp-upload-root": nil, "fixture.txt": []byte("staged fixture")} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gate := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		select {
		case <-gate:
		case <-r.Context().Done():
		}
		_, _ = w.Write([]byte(`{"url":"fixture"}`))
	}))
	defer backend.Close()
	defer unblock()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
	h := lifecycleHTTPHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	// Always unblock before Close, even when an assertion fails with requests held.
	defer unblock()
	body := lifecycleUploadBody(tool, maxHTTPBodyBytes-1024)
	var wg sync.WaitGroup
	start := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := lifecycleRequest(context.Background(), server.URL, body)
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Body.Close()
			_, _ = io.Copy(io.Discard, r.Body)
			if r.StatusCode != http.StatusOK {
				t.Errorf("admitted upload returned HTTP %d", r.StatusCode)
			}
		}()
	}
	defer func() { unblock(); wg.Wait() }()
	start()
	waitLifecycle(t, "first upload reaching the backend", func() bool { return hits.Load() == 1 })
	if got := h.readBudget.large.Load(); got != int64(len(body)) {
		t.Fatalf("decoded request lost its reservation while dispatch is blocked: got=%d want=%d", got, len(body))
	}
	for range 6 {
		start()
	}
	waitLifecycle(t, "seven admitted large requests", func() bool { return h.readBudget.large.Load() == 7*int64(len(body)) })
	waitLifecycle(t, "two active uploads", func() bool { return hits.Load() >= 2 })
	if hits.Load() != 2 {
		t.Fatalf("more than two multipart buffers executed concurrently: %d", hits.Load())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	rejected, err := lifecycleRequest(ctx, server.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	refusal, err := io.ReadAll(rejected.Body)
	_ = rejected.Body.Close()
	if err != nil || rejected.StatusCode != http.StatusServiceUnavailable || rejected.Header.Get("Retry-After") == "" || !json.Valid(refusal) {
		t.Fatalf("excess request must receive immediate JSON-RPC backpressure: status=%d body=%s err=%v", rejected.StatusCode, refusal, err)
	}
	ping, err := lifecycleRequest(ctx, server.URL, []byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = ping.Body.Close()
	if ping.StatusCode != http.StatusOK {
		t.Fatalf("large execution backlog denied small ping: HTTP %d", ping.StatusCode)
	}
	unblock()
	wg.Wait()
	waitLifecycle(t, "request budget cleanup", func() bool { return h.readBudget.large.Load() == 0 && h.readBudget.small.Load() == 0 })
	if hits.Load() != 7 {
		t.Fatalf("expected seven fixture uploads after release, got %d", hits.Load())
	}
}

type lifecycleWriteObserver struct {
	http.ResponseWriter
	started chan struct{}
	once    sync.Once
	bytes   atomic.Int64
}

func TestHTTPLifecycleCancelledUploadReleasesReservation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".octo-mcp-upload-root", "fixture.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gate := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate) }) }
	var hits atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-gate
		_, _ = w.Write([]byte(`{"url":"fixture"}`))
	}))
	defer backend.Close()
	defer unblock()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
	h := lifecycleHTTPHandler(t)
	server := httptest.NewServer(h)
	defer server.Close()
	defer unblock()
	var wg sync.WaitGroup
	body := lifecycleUploadBody("call_op", 0)
	start := func(ctx context.Context) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := lifecycleRequest(ctx, server.URL, body)
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}()
	}
	defer func() { unblock(); wg.Wait() }()
	start(context.Background())
	start(context.Background())
	waitLifecycle(t, "two occupied upload slots", func() bool { return hits.Load() == 2 })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start(ctx)
	waitLifecycle(t, "queued request reservation", func() bool { return h.readBudget.small.Load() == 3*minReadCharge })
	cancel()
	waitLifecycle(t, "cancelled waiter releasing its reservation", func() bool { return h.readBudget.small.Load() == 2*minReadCharge })
	if hits.Load() != 2 {
		t.Fatalf("cancelled upload reached the backend: %d", hits.Load())
	}
	unblock()
	wg.Wait()
	waitLifecycle(t, "all small reservations released", func() bool { return h.readBudget.small.Load() == 0 })
}

func TestHTTPLifecycleParseFailureAndNotificationReleaseBudget(t *testing.T) {
	h := lifecycleHTTPHandler(t)
	for _, input := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":`,
		strings.Repeat("x", smallReadThreshold+1),
	} {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(input))
		req.ContentLength = -1 // includes the small-to-large budget transfer path
		h.ServeHTTP(httptest.NewRecorder(), req)
		if h.readBudget.small.Load() != 0 || h.readBudget.large.Load() != 0 {
			t.Fatal("notification or parse error leaked its request reservation")
		}
	}
}

func TestHTTPLifecycleStatelessResourceContract(t *testing.T) {
	h := lifecycleHTTPHandler(t)
	rpc := func(method string, params any) rpcResponse {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body)))
		var result rpcResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Error != nil {
			t.Fatalf("%s failed: body=%s err=%v", method, w.Body.String(), err)
		}
		return result
	}
	// Resources are advertised by the server. Standard clients need not send
	// a nonstandard capabilities.resources field, and HTTP retains no session.
	for _, version := range []string{defaultProtocolVersion, "unsupported-client-version"} {
		response := rpc("initialize", map[string]any{
			"protocolVersion": version, "capabilities": map[string]any{"roots": map[string]any{}},
			"clientInfo": map[string]any{"name": "fixture", "version": "1"},
		})
		var initialized struct {
			ProtocolVersion string                     `json:"protocolVersion"`
			Capabilities    map[string]json.RawMessage `json:"capabilities"`
		}
		if err := json.Unmarshal(response.Result, &initialized); err != nil || initialized.ProtocolVersion != defaultProtocolVersion || initialized.Capabilities["resources"] == nil {
			t.Fatalf("server must select its implemented version and advertise resources: %s err=%v", response.Result, err)
		}
	}
	description := rpc("tools/call", map[string]any{"name": "describe_op", "arguments": map[string]any{"operation_id": "message.send"}})
	var result toolResult
	if err := json.Unmarshal(description.Result, &result); err != nil || result.IsError {
		t.Fatalf("describe failed: %s err=%v", description.Result, err)
	}
	payload := toolText(t, result)
	skill := payload["skill"].(map[string]any)
	uri, _ := skill["resource_uri"].(string)
	if uri == "" {
		t.Fatal("description omitted the server resource URI")
	}
	resource := rpc("resources/read", map[string]any{"uri": uri})
	var content struct {
		Contents []resourceContent `json:"contents"`
	}
	if err := json.Unmarshal(resource.Result, &content); err != nil || len(content.Contents) == 0 || content.Contents[0].Text == "" {
		t.Fatalf("advertised resource is unreadable on a subsequent POST: %s err=%v", resource.Result, err)
	}
}

func (w *lifecycleWriteObserver) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *lifecycleWriteObserver) Write(p []byte) (int, error) {
	w.bytes.Store(int64(len(p)))
	w.once.Do(func() { close(w.started) })
	return w.ResponseWriter.Write(p)
}

func TestHTTPLifecycleSlowResponseWriteIsBounded(t *testing.T) {
	large := `{"items":[{"fixture":"` + strings.Repeat("x", 8<<20) + `"}]}`
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, large) }))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	h := lifecycleHTTPHandler(t)
	// Race plus coverage instruments several full 8 MiB JSON passes before
	// the socket write begins. Keep that setup inside a generous request
	// budget while still asserting the real write deadline terminates it.
	h.requestTimeout = 15 * time.Second
	writing := make(chan struct{})
	done := make(chan struct{})
	var observed *lifecycleWriteObserver
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed = &lifecycleWriteObserver{ResponseWriter: w, started: writing}
		h.ServeHTTP(observed, r)
		close(done)
	}))
	defer server.Close()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if tcp, ok := conn.(*net.TCPConn); ok {
		if err := tcp.SetReadBuffer(1024); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_op","arguments":{"operation_id":"group.list","arguments":{}}}}`
	_, err = fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Type: application/json\r\nAuthorization: Bearer bf_fixture\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-writing:
	case <-time.After(30 * time.Second):
		t.Fatal("server never attempted the response write")
	}
	if observed.bytes.Load() < 1<<20 {
		t.Fatalf("fixture did not reach a large response write: %d bytes", observed.bytes.Load())
	}
	select {
	case <-done:
	case <-time.After(h.requestTimeout + 3*time.Second):
		t.Fatal("slow-reading client retained the HTTP handler beyond the request deadline")
	}
	if h.readBudget.small.Load() != 0 {
		t.Fatal("timed-out response retained its request reservation")
	}
}
