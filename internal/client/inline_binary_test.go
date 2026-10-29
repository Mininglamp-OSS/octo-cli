package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestInlineBinaryStreamsLargeBodies pins the bounded inline path used by the
// MCP server: a body within the limit is returned inline, a larger one is
// described with its full size and sha256, and readBoundedBody never keeps
// more than limit+1 bytes however large the body is.
func TestInlineBinaryStreamsLargeBodies(t *testing.T) {
	const limit = 1 << 10
	small := []byte{0x89, 'P', 'N', 'G', 0, 1}
	large := bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		if r.URL.Path == "/large" {
			_, _ = w.Write(large)
			return
		}
		_, _ = w.Write(small)
	}))
	defer srv.Close()
	c := newTestClient(srv)

	call := func(path string) map[string]any {
		t.Helper()
		raw, err := c.Do(context.Background(), &Request{Method: http.MethodGet, Path: path, BinaryResponse: true, InlineBinaryLimit: limit})
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		var env map[string]any
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return env
	}

	env := call("/small")
	got, _ := base64.StdEncoding.DecodeString(env["content_base64"].(string))
	sum := sha256.Sum256(small)
	if env["inline"] != true || !bytes.Equal(got, small) || env["sha256"] != hex.EncodeToString(sum[:]) || env["size"] != float64(len(small)) {
		t.Fatalf("small envelope = %v", env)
	}

	env = call("/large")
	sum = sha256.Sum256(large)
	if env["inline"] != false || env["content_base64"] != nil || env["inline_limit"] != float64(limit) {
		t.Fatalf("large envelope must be described, not inlined: %v", env)
	}
	if env["size"] != float64(len(large)) || env["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("large size/sha256 = %v/%v, want %d/%x", env["size"], env["sha256"], len(large), sum)
	}

	head, size, digest, err := readBoundedBody(bytes.NewReader(large), limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != limit+1 || size != int64(len(large)) || !bytes.Equal(digest, sum[:]) {
		t.Fatalf("readBoundedBody kept %d bytes (want %d), size %d, digest %x", len(head), limit+1, size, digest)
	}
}
