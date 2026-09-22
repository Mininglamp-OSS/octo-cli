package mcp

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestNearestOperations_BoundsRankingInput is the P1 DoS proof: a giant
// operation_id must not blow up the edit-distance ranking. The call returns
// promptly and the ranking cost is bounded by maxRankQueryRunes regardless of
// input size.
func TestNearestOperations_BoundsRankingInput(t *testing.T) {
	reg := registry.MustNew()
	huge := strings.Repeat("a", 5<<20) // 5 MiB, well past the 8 MiB body cap headroom
	done := make(chan []string, 1)
	go func() { done <- nearestOperations(reg, huge) }()
	select {
	case <-done:
		// returned promptly — the input was capped
	case <-time.After(2 * time.Second):
		t.Fatal("nearestOperations did not bound a multi-megabyte operation_id")
	}
	// A normal typo still ranks correctly after the bound.
	got := nearestOperations(reg, "message.snd")
	var hasSend bool
	for _, c := range got {
		if c == "message.send" {
			hasSend = true
		}
	}
	if !hasSend {
		t.Errorf("bounded ranking must still surface message.send for 'message.snd', got %v", got)
	}
}

// TestUnknownOperationPayload_TruncatesEchoedID confirms the reflected id is
// capped, so a multi-megabyte operation_id is not echoed whole into the error.
func TestUnknownOperationPayload_TruncatesEchoedID(t *testing.T) {
	reg := registry.MustNew()
	huge := strings.Repeat("x", 1<<20)
	payload := unknownOperationPayload(reg, huge)
	msg := payload["error"].(map[string]any)["message"].(string)
	if len([]rune(msg)) > 260 {
		t.Errorf("echoed operation_id must be truncated, message len=%d", len([]rune(msg)))
	}
}

// TestHTTPHandler_RequestTimeoutConfigured pins that a per-request timeout is
// wired (env override honored, sane default otherwise).
func TestHTTPHandler_RequestTimeoutConfigured(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	if h.requestTimeout <= 0 {
		t.Errorf("a per-request timeout must be set, got %v", h.requestTimeout)
	}
	t.Setenv("OCTO_MCP_HTTP_REQUEST_TIMEOUT", "17s")
	h2, _ := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if h2.requestTimeout != 17*time.Second {
		t.Errorf("env override must be honored, got %v", h2.requestTimeout)
	}
}

// TestHTTPTransport_UnauthenticatedDescribeOpHugeIDIsBounded is the end-to-end
// DoS guard: an unauthenticated POST carrying a large operation_id to
// describe_op returns promptly (bounded ranking) rather than pinning a core.
func TestHTTPTransport_UnauthenticatedDescribeOpHugeIDIsBounded(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{})
	if err != nil {
		t.Fatalf("NewHTTPHandler: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	huge := strings.Repeat("z", 2<<20) // 2 MiB operation_id, no bearer
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"describe_op","arguments":{"operation_id":"` + huge + `"}}}`
	done := make(chan int, 1)
	go func() {
		code, _ := postJSON(t, ts.URL, "", "", body)
		done <- code
	}()
	select {
	case code := <-done:
		if code != 200 {
			t.Errorf("describe_op with a huge id should still return 200 (unknown-op payload), got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("unauthenticated describe_op with a huge id did not complete promptly")
	}
}
