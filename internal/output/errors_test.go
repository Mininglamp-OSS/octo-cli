package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

// wrappedErr is a local stand-in for a retry sentinel that embeds an
// *ExitError. Clients of AsExitError must still reach the structured error
// through the Unwrap chain.
type wrappedErr struct {
	*ExitError
}

func (w *wrappedErr) Unwrap() error {
	if w == nil {
		return nil
	}
	return w.ExitError
}

func TestAsExitError_UnwrapsThroughWrapper(t *testing.T) {
	orig := ErrValidation("bad input", "try again")
	wrapped := &wrappedErr{ExitError: orig}

	got := AsExitError(wrapped)
	if got == nil {
		t.Fatal("AsExitError should reach the embedded *ExitError")
	}
	if got != orig {
		t.Errorf("got different *ExitError instance: %p vs %p", got, orig)
	}
	if got.Type != "validation" || got.Code != "VALIDATION_ERROR" {
		t.Errorf("fields lost: %+v", got)
	}
}

func TestAsExitError_ChainedWrappers(t *testing.T) {
	orig := ErrAuth("no token", "set OCTO_BOT_TOKEN")
	// Two layers of wrapping: wrappedErr → fmt.Errorf("ctx: %w", ...).
	inner := &wrappedErr{ExitError: orig}
	outer := wrappedErr{ExitError: nil} // unused; use errors.Join to layer.
	_ = outer

	joined := errors.Join(inner, errors.New("side note"))
	got := AsExitError(joined)
	if got == nil {
		t.Fatal("AsExitError should find *ExitError across errors.Join")
	}
	if got.Type != "auth_error" {
		t.Errorf("type = %q", got.Type)
	}
}

func TestAsExitError_NilAndPlainError(t *testing.T) {
	if AsExitError(nil) != nil {
		t.Error("nil input should return nil")
	}
	if AsExitError(errors.New("plain")) != nil {
		t.Error("plain error should return nil")
	}
}

// TestParseBackendError_MattersCodes exercises every entry in the backend
// mapping table so we catch any accidental drift in taxonomy or hint wording.
func TestParseBackendError_MattersCodes(t *testing.T) {
	cases := []struct {
		code     string
		wantType string
	}{
		{"UNAUTHORIZED", "auth_error"},
		{"AUTH_UNAVAILABLE", "network"},
		{"VALIDATION_ERROR", "validation"},
		{"MATTER_NOT_FOUND", "api_error"},
		{"NOT_FOUND", "api_error"},
		{"ASSIGNEE_NOT_FOUND", "api_error"},
		{"FORBIDDEN", "permission"},
		{"SPACE_FORBIDDEN", "permission"},
		{"DUPLICATE_ASSIGNEE", "validation"},
		{"RATE_LIMITED", "rate_limited"},
		{"UPSTREAM_UNAVAILABLE", "network"},
		{"INTERNAL_ERROR", "api_error"},
		{"PAYLOAD_TOO_LARGE", "validation"},
	}
	for _, c := range cases {
		t.Run(c.code, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"error":{"code":%q,"message":"x"}}`, c.code))
			ee := ParseBackendError(400, body)
			if ee.Code != c.code {
				t.Errorf("Code = %q, want %q", ee.Code, c.code)
			}
			if ee.Type != c.wantType {
				t.Errorf("Type = %q, want %q", ee.Type, c.wantType)
			}
			if ee.Hint == "" {
				t.Errorf("Hint should be set for known code %q", c.code)
			}
			// Detail must contain the original body.
			if len(ee.Detail) == 0 {
				t.Errorf("Detail should preserve original body")
			}
		})
	}
}

func TestParseBackendError_MattersUnknownCode(t *testing.T) {
	body := []byte(`{"error":{"code":"SOMETHING_NEW","message":"nope"}}`)
	ee := ParseBackendError(418, body)
	if ee.Code != "SOMETHING_NEW" {
		t.Errorf("Code = %q", ee.Code)
	}
	if ee.Type != "api_error" {
		t.Errorf("unknown codes should fall back to api_error, got %q", ee.Type)
	}
	if ee.Hint != "" {
		t.Errorf("unknown codes should have no hint, got %q", ee.Hint)
	}
	if ee.Message != "nope" {
		t.Errorf("Message = %q", ee.Message)
	}
}

func TestParseBackendError_DmworkimFormat(t *testing.T) {
	body := []byte(`{"msg":"中文错误","status":400}`)
	ee := ParseBackendError(400, body)
	if ee.Message != "中文错误" {
		t.Errorf("Message = %q, want 中文错误", ee.Message)
	}
	if ee.Type != "validation" {
		t.Errorf("400 should map to validation, got %q", ee.Type)
	}
	if ee.Code != "HTTP_400" {
		t.Errorf("Code = %q", ee.Code)
	}
}

func TestParseBackendError_DmworkimAuth401(t *testing.T) {
	body := []byte(`{"msg":"认证失败","status":401}`)
	ee := ParseBackendError(401, body)
	if ee.Type != "auth_error" {
		t.Errorf("Type = %q, want auth_error", ee.Type)
	}
	if ee.Code != "UNAUTHORIZED" {
		t.Errorf("Code = %q, want UNAUTHORIZED", ee.Code)
	}
	if ee.Message != "认证失败" {
		t.Errorf("Message = %q", ee.Message)
	}
	if ee.Hint == "" {
		t.Error("401 should carry a hint")
	}
}

func TestParseBackendError_EmptyBody_StatusInference(t *testing.T) {
	cases := []struct {
		status   int
		wantType string
		wantCode string
	}{
		{401, "auth_error", "UNAUTHORIZED"},
		{403, "permission", "FORBIDDEN"},
		{404, "api_error", "NOT_FOUND"},
		{413, "validation", "PAYLOAD_TOO_LARGE"},
		{429, "rate_limited", "RATE_LIMITED"},
		{500, "api_error", "INTERNAL_ERROR"},
		{502, "api_error", "HTTP_502"},
		{503, "api_error", "UPSTREAM_UNAVAILABLE"},
		{504, "api_error", "HTTP_504"},
		{418, "validation", "HTTP_418"},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("status_%d", c.status), func(t *testing.T) {
			ee := ParseBackendError(c.status, nil)
			if ee.Type != c.wantType {
				t.Errorf("status %d: Type = %q, want %q", c.status, ee.Type, c.wantType)
			}
			if ee.Code != c.wantCode {
				t.Errorf("status %d: Code = %q, want %q", c.status, ee.Code, c.wantCode)
			}
			// Status inference paths don't carry the original body.
			if len(ee.Detail) != 0 {
				t.Errorf("empty body should produce no Detail")
			}
		})
	}
}

func TestParseBackendError_MalformedJSON(t *testing.T) {
	body := []byte(`{not valid json`)
	ee := ParseBackendError(500, body)
	if ee.Type != "api_error" {
		t.Errorf("Type = %q, want api_error", ee.Type)
	}
	if ee.Code != "INTERNAL_ERROR" {
		t.Errorf("Code = %q", ee.Code)
	}
	// The raw body (short enough) should appear in the message.
	if !contains(ee.Message, "{not valid json") {
		t.Errorf("malformed body should be included in message, got %q", ee.Message)
	}
}

func TestParseBackendError_LargeBodyOmitted(t *testing.T) {
	big := make([]byte, 3000)
	for i := range big {
		big[i] = 'a'
	}
	ee := ParseBackendError(500, big)
	// Bodies ≥ 2048 bytes are excluded from the message.
	if contains(ee.Message, "aaaa") {
		t.Errorf("oversized body should not be in message, got len %d", len(ee.Message))
	}
}

func TestParseBackendError_DetailRoundTrip(t *testing.T) {
	body := []byte(`{"error":{"code":"VALIDATION_ERROR","message":"m","details":{"field":"title"}}}`)
	ee := ParseBackendError(400, body)
	var detail map[string]any
	if err := json.Unmarshal(ee.Detail, &detail); err != nil {
		t.Fatalf("Detail not valid JSON: %v", err)
	}
	if _, ok := detail["error"]; !ok {
		t.Errorf("Detail should preserve envelope shape: %v", detail)
	}
}

func TestExitError_ExitCodes(t *testing.T) {
	cases := []struct {
		typ  string
		want int
	}{
		{"auth_error", 3},
		{"validation", 2},
		{"config", 2},
		{"api_error", 1},
		{"network", 1},
		{"rate_limited", 1},
		{"permission", 1},
		{"internal", 1},
		{"", 1},
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			ee := &ExitError{Type: c.typ}
			if got := ee.ExitCode(); got != c.want {
				t.Errorf("%s: exit code %d, want %d", c.typ, got, c.want)
			}
		})
	}
}

func TestExitError_ErrorString(t *testing.T) {
	var nilErr *ExitError
	if nilErr.Error() != "" {
		t.Errorf("nil *ExitError.Error should be empty")
	}
	ee := &ExitError{Code: "FOO", Message: "bar"}
	if ee.Error() != "FOO: bar" {
		t.Errorf("Error() = %q", ee.Error())
	}
	noCode := &ExitError{Message: "plain"}
	if noCode.Error() != "plain" {
		t.Errorf("no-code Error() = %q", noCode.Error())
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
