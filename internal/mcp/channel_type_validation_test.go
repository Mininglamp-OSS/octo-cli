package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestChannelTypeSchemaRefusesBeforeBackend(t *testing.T) {
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message_id":"1"}`))
	}))
	defer backend.Close()
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
	for operation, fields := range map[string]map[string]any{
		"message.send":         {"payload": map[string]any{"type": 1, "content": "must not send"}},
		"message.sync":         {},
		"message.edit":         {"message_id": "1", "content_edit": "{}"},
		"message.read-receipt": {"message_ids": []any{"1"}},
		"bot.typing":           {},
	} {
		t.Run(operation, func(t *testing.T) {
			fields["channel_id"] = "fixture"
			fields["channel_type"] = 99
			result := s.callOp(context.Background(), operation, fields)
			// Supply only declared fields so another validation error cannot satisfy this test.
			if !result.IsError || !strings.Contains(result.Content[0].Text, "ENUM_NOT_ALLOWED") {
				t.Fatalf("expected channel enum refusal: %s", result.Content[0].Text)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid channel types reached backend %d times", calls.Load())
	}
}
