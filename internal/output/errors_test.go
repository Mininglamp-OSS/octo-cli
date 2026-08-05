package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
		{"BOT_WORKSPACE_MEMBERSHIP_REQUIRED", "permission"},
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

func TestParseBackendErrorWorkspaceBotMembershipHint(t *testing.T) {
	body := []byte(`{"error":{"code":"BOT_WORKSPACE_MEMBERSHIP_REQUIRED","message":"bot must be added to workspace members"}}`)
	ee := ParseBackendError(http.StatusForbidden, body)
	if ee.Code != "BOT_WORKSPACE_MEMBERSHIP_REQUIRED" || ee.Type != "permission" {
		t.Fatalf("error = %+v", ee)
	}
	if ee.Hint != "ask a Workspace owner or admin to add this Bot in Workspace Members" {
		t.Fatalf("hint = %q", ee.Hint)
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
		{409, "validation", "CONFLICT"},
		{412, "validation", "PRECONDITION_FAILED"},
		{413, "validation", "PAYLOAD_TOO_LARGE"},
		{422, "validation", "UNPROCESSABLE_ENTITY"},
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

// --- octo-drive error envelope ---

// octo-drive returns `{"error":"<code>","message":"..."}` — the code is a bare
// string, not a nested object, and the human text is under "message" rather than
// "msg". These tests pin that shape and the lowercase code mapping, and confirm
// the two older envelope families still classify exactly as before.

func TestParseBackendError_DriveLowercaseCodes(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantType string
		wantCode string
		wantExit int
	}{
		{"permission_denied", 403, `{"error":"permission_denied","message":"drive: permission denied"}`, "permission", "permission_denied", 1},
		{"password_required", 403, `{"error":"password_required","message":"drive: password required"}`, "permission", "password_required", 1},
		{"wrong_password", 403, `{"error":"wrong_password","message":"drive: wrong password"}`, "permission", "wrong_password", 1},
		{"share_expired", 403, `{"error":"share_expired","message":"drive: share expired"}`, "permission", "share_expired", 1},
		{"not_found", 404, `{"error":"not_found","message":"drive: not found"}`, "api_error", "not_found", 1},
		{"conflict", 409, `{"error":"conflict","message":"drive: conflict"}`, "validation", "conflict", 2},
		{"invalid_argument", 400, `{"error":"invalid_argument","message":"drive: invalid argument"}`, "validation", "invalid_argument", 2},
		{"unauthorized", 401, `{"error":"unauthorized","message":"missing token"}`, "auth_error", "unauthorized", 3},
		{"auth_unavailable", 500, `{"error":"auth_unavailable","message":"upstream down"}`, "network", "auth_unavailable", 1},
		{"internal", 500, `{"error":"internal","message":"boom"}`, "api_error", "internal", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ee := ParseBackendError(tc.status, []byte(tc.body))
			if ee.Type != tc.wantType {
				t.Errorf("type: got %q, want %q", ee.Type, tc.wantType)
			}
			if ee.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", ee.Code, tc.wantCode)
			}
			if ee.ExitCode() != tc.wantExit {
				t.Errorf("exit: got %d, want %d", ee.ExitCode(), tc.wantExit)
			}
			if ee.Message == "" {
				t.Error("message must carry the backend text")
			}
			if ee.Hint == "" {
				t.Error("a mapped drive code must carry an actionable hint")
			}
		})
	}
}

// Both code conventions must work at once: the uppercase matters codes are
// untouched by the lowercase additions.
func TestParseBackendError_BothCodeCasesWork(t *testing.T) {
	upper := ParseBackendError(403, []byte(`{"error":{"code":"SPACE_FORBIDDEN","message":"nope"}}`))
	if upper.Type != "permission" || upper.Code != "SPACE_FORBIDDEN" {
		t.Errorf("uppercase mapping regressed: %+v", upper)
	}
	lower := ParseBackendError(403, []byte(`{"error":"permission_denied","message":"nope"}`))
	if lower.Type != "permission" || lower.Code != "permission_denied" {
		t.Errorf("lowercase mapping: %+v", lower)
	}
}

// An agent branches on Type and the exit code, not on which backend answered.
// So a lowercase drive code and its uppercase counterpart must classify
// identically — only the hint may differ, to name the domain's own remedy.
func TestParseBackendError_CrossFamilyTaxonomyIsIdentical(t *testing.T) {
	pairs := []struct {
		lower, upper string
		status       int
		wantType     string
		wantExit     int
	}{
		{"conflict", "CONFLICT", 409, "validation", 2},
		{"invalid_argument", "VALIDATION_ERROR", 400, "validation", 2},
		{"not_found", "NOT_FOUND", 404, "api_error", 1},
		{"unauthorized", "UNAUTHORIZED", 401, "auth_error", 3},
		{"permission_denied", "FORBIDDEN", 403, "permission", 1},
		{"internal", "INTERNAL_ERROR", 500, "api_error", 1},
		{"auth_unavailable", "AUTH_UNAVAILABLE", 500, "network", 1},
	}
	for _, p := range pairs {
		t.Run(p.lower+"/"+p.upper, func(t *testing.T) {
			lower := ParseBackendError(p.status, []byte(`{"error":"`+p.lower+`","message":"m"}`))
			upper := ParseBackendError(p.status, []byte(`{"error":{"code":"`+p.upper+`","message":"m"}}`))

			if lower.Type != p.wantType || upper.Type != p.wantType {
				t.Errorf("type: lower=%q upper=%q, want both %q", lower.Type, upper.Type, p.wantType)
			}
			if lower.ExitCode() != p.wantExit || upper.ExitCode() != p.wantExit {
				t.Errorf("exit: lower=%d upper=%d, want both %d", lower.ExitCode(), upper.ExitCode(), p.wantExit)
			}
			// The code itself stays verbatim so the backend's own value is still
			// available to a caller that wants it.
			if lower.Code != p.lower || upper.Code != p.upper {
				t.Errorf("code: lower=%q upper=%q, want the literals back", lower.Code, upper.Code)
			}
		})
	}
}

// The parity above is a property of the table, not just of the pairs spelled out
// in the test: every lowercase drive code whose uppercase counterpart exists must
// agree on Type. This catches a future addition that only edits one side.
func TestBackendErrorMapping_CaseCounterpartsAgreeOnType(t *testing.T) {
	for code, entry := range backendErrorMapping {
		if code != strings.ToLower(code) {
			continue // only walk the lowercase drive codes
		}
		counterpart, ok := backendErrorMapping[strings.ToUpper(code)]
		if !ok {
			continue // no counterpart to agree with
		}
		if entry.Type != counterpart.Type {
			t.Errorf("%s is %q but %s is %q; a caller must not have to know which backend answered",
				code, entry.Type, strings.ToUpper(code), counterpart.Type)
		}
	}
}

// An unmapped code keeps the historical fallback for its envelope family: the
// matters family stays api_error with no hint, and a drive-shaped body falls
// back to status inference — which is what those bodies already got before the
// drive layer existed.
func TestParseBackendError_UnknownCodeFallbackUnchanged(t *testing.T) {
	matters := ParseBackendError(403, []byte(`{"error":{"code":"BRAND_NEW_CODE","message":"m"}}`))
	if matters.Type != "api_error" || matters.Code != "BRAND_NEW_CODE" || matters.Hint != "" {
		t.Errorf("matters fallback changed: %+v", matters)
	}
	drive := ParseBackendError(409, []byte(`{"error":"brand_new_code","message":"m"}`))
	if drive.Type != typeFromStatus(409) || drive.Code != "brand_new_code" {
		t.Errorf("drive fallback: got %s/%s, want %s from the status", drive.Type, drive.Code, typeFromStatus(409))
	}
}

// A free-text `error` value is prose, not a code. octo-drive's space middleware
// emits several; promoting them to ExitError.Code would produce garbage codes,
// so they must fall through to the raw status fallback.
func TestParseBackendError_FreeTextErrorIsNotACode(t *testing.T) {
	for _, body := range []string{
		`{"error":"missing X-Space-Id"}`,
		`{"error":"not a member of the requested space"}`,
		`{"error":"invalid token"}`,
		`{"error":"space lookup upstream error"}`,
	} {
		ee := ParseBackendError(400, []byte(body))
		if ee.Code != codeFromStatus(400) {
			t.Errorf("%s: code %q, want the status-derived %q", body, ee.Code, codeFromStatus(400))
		}
	}
}

// The dmworkim {msg,status} family must still be recognised — it has no "error"
// key, so the drive layer never sees it.
func TestParseBackendError_DmworkimEnvelopeUnaffected(t *testing.T) {
	ee := ParseBackendError(400, []byte(`{"msg":"bad request","status":400}`))
	if ee.Message != "bad request" {
		t.Errorf("message: got %q", ee.Message)
	}
	if ee.Code != codeFromStatus(400) {
		t.Errorf("code: got %q", ee.Code)
	}
}
