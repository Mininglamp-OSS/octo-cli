package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestStep2MessageEditAcceptsRawMode(t *testing.T) {
	requests, err := runStep2MessageEdit(t, step2RawMessageEditBody())
	if err != nil {
		t.Fatalf("raw message edit: %v", err)
	}
	if requests != 1 {
		t.Fatalf("raw message edit reached HTTP %d times, want 1", requests)
	}
}

func TestStep2MessageEditAcceptsRegistryIntent(t *testing.T) {
	requests, err := runStep2MessageEdit(t, step2RegistryMessageEditBody())
	if err != nil {
		t.Fatalf("Registry message edit: %v", err)
	}
	if requests != 1 {
		t.Fatalf("Registry message edit reached HTTP %d times, want 1", requests)
	}
}

func TestStep2MessageEditRejectsRegistryIntentWithAnyContentEditPresence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "non-empty", value: "raw update"},
		{name: "empty", value: ""},
		{name: "null", value: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := step2RegistryMessageEditBody()
			body["content_edit"] = tc.value
			requests, err := runStep2MessageEdit(t, body)
			assertStep2MessageEditValidationError(t, err)
			if requests != 0 {
				t.Fatalf("mixed Registry/raw edit reached HTTP %d times", requests)
			}
		})
	}
}

func TestStep2MessageEditRequiresFieldsForTheSelectedMode(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "raw content_edit", body: bodyWithout(step2RawMessageEditBody(), "content_edit")},
		{name: "raw empty content_edit", body: bodyWith(step2RawMessageEditBody(), "content_edit", "")},
		{name: "raw whitespace content_edit", body: bodyWith(step2RawMessageEditBody(), "content_edit", " \t ")},
		{name: "raw null content_edit", body: bodyWith(step2RawMessageEditBody(), "content_edit", nil)},
		{name: "Registry template_ref", body: bodyWithout(step2RegistryMessageEditBody(), "template_ref")},
		{name: "Registry state", body: bodyWithout(step2RegistryMessageEditBody(), "state")},
		{name: "Registry data", body: bodyWithout(step2RegistryMessageEditBody(), "data")},
		{name: "Registry card_seq", body: bodyWithout(step2RegistryMessageEditBody(), "card_seq")},
		{name: "Registry empty state", body: bodyWith(step2RegistryMessageEditBody(), "state", "")},
		{name: "Registry whitespace state", body: bodyWith(step2RegistryMessageEditBody(), "state", " \t ")},
		{name: "Registry null template_ref", body: bodyWith(step2RegistryMessageEditBody(), "template_ref", nil)},
		{name: "Registry null state", body: bodyWith(step2RegistryMessageEditBody(), "state", nil)},
		{name: "Registry null data", body: bodyWith(step2RegistryMessageEditBody(), "data", nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			requests, err := runStep2MessageEdit(t, tc.body)
			assertStep2MessageEditValidationError(t, err)
			if requests != 0 {
				t.Fatalf("invalid %s edit reached HTTP %d times", tc.name, requests)
			}
		})
	}
}

func runStep2MessageEdit(t *testing.T, body map[string]any) (requestCount int, executeErr error) {
	t.Helper()
	requests := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal message edit body: %v", err)
	}
	root.SetArgs([]string{"message", "edit", "--data", string(data)})
	executeErr = root.Execute()
	return requests, executeErr
}

func assertStep2MessageEditValidationError(t *testing.T, err error) {
	t.Helper()
	exitErr := output.AsExitError(err)
	if exitErr == nil || exitErr.Type != "validation" || exitErr.Code != "VALIDATION_ERROR" {
		t.Fatalf("validation error = %#v, want structured validation error", exitErr)
	}
}

func step2RawMessageEditBody() map[string]any {
	return map[string]any{
		"message_id":   "123",
		"channel_id":   "channel-1",
		"channel_type": 1,
		"content_edit": `{"type":17,"card":{}}`,
	}
}

func step2RegistryMessageEditBody() map[string]any {
	// This synthetic reference exercises only the CLI preflight contract; the
	// test receiver is not a deployed Registry catalog.
	return map[string]any{
		"message_id":   "123",
		"channel_id":   "channel-1",
		"channel_type": 1,
		"template_ref": map[string]any{"template_id": "demo", "version": "1"},
		"state":        "active",
		"data":         map[string]any{"title": "Updated"},
		"card_seq":     7,
	}
}

func bodyWithout(body map[string]any, field string) map[string]any {
	cloned := make(map[string]any, len(body)-1)
	for key, value := range body {
		if key != field {
			cloned[key] = value
		}
	}
	return cloned
}

func bodyWith(body map[string]any, field string, value any) map[string]any {
	body[field] = value
	return body
}
