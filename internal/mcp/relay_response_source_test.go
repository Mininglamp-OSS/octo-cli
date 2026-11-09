package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTurnRelayPreservesSourceResponseAboveRequestLimit(t *testing.T) {
	expected := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"` + strings.Repeat("x", 9<<20) + `"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(expected)
	}))
	defer server.Close()
	ticket, _ := json.Marshal(map[string]any{"endpoint": server.URL, "token": strings.Repeat("a", 64)})
	var output bytes.Buffer
	err := ServeTurnRelay(context.Background(), bytes.NewReader(ticket), strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}\n"), &output)
	if err != nil || !bytes.Equal(bytes.TrimSpace(output.Bytes()), expected) {
		t.Fatal("source response was capped by unrelated incoming-request ceiling", err, output.Len())
	}
}
