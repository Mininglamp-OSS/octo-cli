package output

import (
	"encoding/json"
	"fmt"
)

// ExitError is the canonical CLI error representation. All errors reaching the
// top-level command should be an *ExitError so the envelope renderer can emit a
// structured JSON error. Agent consumers parse Type/Code to decide next action.
type ExitError struct {
	Type    string          // CLI taxonomy: auth_error | validation | api_error | network | rate_limited | permission | internal
	Code    string          // machine code (string from backend or a numeric-as-string sentinel)
	Message string          // human-readable message (English)
	Hint    string          // suggested next action
	Detail  json.RawMessage // original backend payload (optional)
}

// Error satisfies the error interface. Prefer the envelope renderer over Error()
// when producing user-facing output.
func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

// ExitCode returns the process exit code for this error type.
// auth_error → 3, validation/config → 2, all others → 1.
func (e *ExitError) ExitCode() int {
	switch e.Type {
	case "auth_error":
		return 3
	case "validation", "config":
		return 2
	default:
		return 1
	}
}

// AsExitError unwraps an error to *ExitError, returning nil if none present.
func AsExitError(err error) *ExitError {
	if err == nil {
		return nil
	}
	if ee, ok := err.(*ExitError); ok {
		return ee
	}
	return nil
}

// --- constructors ---

// ErrWithHint wraps an arbitrary error with a Type, Code, and Hint. Useful when
// propagating non-API errors that still need structured output.
func ErrWithHint(typ, code, msg, hint string) *ExitError {
	return &ExitError{Type: typ, Code: code, Message: msg, Hint: hint}
}

// ErrAuth reports an authentication failure.
func ErrAuth(msg, hint string) *ExitError {
	return &ExitError{Type: "auth_error", Code: "UNAUTHORIZED", Message: msg, Hint: hint}
}

// ErrValidation reports a client-side or server-side validation error.
func ErrValidation(msg, hint string) *ExitError {
	return &ExitError{Type: "validation", Code: "VALIDATION_ERROR", Message: msg, Hint: hint}
}

// ErrAPI reports a generic API error (4xx/5xx unclassified).
func ErrAPI(code, msg, hint string) *ExitError {
	return &ExitError{Type: "api_error", Code: code, Message: msg, Hint: hint}
}

// ErrNetwork reports a transport-level failure (DNS, connect, TLS, timeout).
func ErrNetwork(msg, hint string) *ExitError {
	return &ExitError{Type: "network", Code: "NETWORK_ERROR", Message: msg, Hint: hint}
}


// --- backend error mapping ---

// backendErrorMapping maps matters error codes to CLI taxonomy + hint. See
// docs/architecture-design.md §4.3.
var backendErrorMapping = map[string]struct {
	Type string
	Hint string
}{
	"UNAUTHORIZED":         {"auth_error", "check OCTO_BOT_TOKEN; bot may be unpublished"},
	"AUTH_UNAVAILABLE":     {"network", "auth service unreachable; retry later"},
	"VALIDATION_ERROR":     {"validation", "check params with `octo schema <op>`"},
	"MATTER_NOT_FOUND":     {"api_error", "verify ID with `octo matters list`"},
	"NOT_FOUND":            {"api_error", "resource not found"},
	"ASSIGNEE_NOT_FOUND":   {"api_error", "assignee not in space or invalid UID"},
	"FORBIDDEN":            {"permission", "bot lacks permission; check space membership"},
	"SPACE_FORBIDDEN":      {"permission", "bot not a member of this space"},
	"DUPLICATE_ASSIGNEE":   {"validation", "already assigned; check current assignees"},
	"RATE_LIMITED":         {"rate_limited", "server-side rate limit; retry after cooldown"},
	"UPSTREAM_UNAVAILABLE": {"network", "upstream dependency unavailable; retry later"},
	"INTERNAL_ERROR":       {"api_error", "internal server error; retry or report"},
	"PAYLOAD_TOO_LARGE":    {"validation", "request body exceeds 1MB limit"},
}

// ParseBackendError converts an HTTP response body (and status) to an *ExitError.
// It tries matters envelope first, then dmworkim {msg,status}, falling back to a
// raw dump. The status code is used as a signal when the body is unparseable.
func ParseBackendError(status int, body []byte) *ExitError {
	// Layer 1: matters envelope {"error":{"code":"...","message":"...","details":{...}}}
	var mEnv struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if len(body) > 0 && json.Unmarshal(body, &mEnv) == nil && mEnv.Error.Code != "" {
		typ := "api_error"
		hint := ""
		if m, ok := backendErrorMapping[mEnv.Error.Code]; ok {
			typ = m.Type
			hint = m.Hint
		}
		e := &ExitError{
			Type:    typ,
			Code:    mEnv.Error.Code,
			Message: mEnv.Error.Message,
			Hint:    hint,
			Detail:  json.RawMessage(body),
		}
		return e
	}

	// Layer 2: dmworkim {"msg":"...","status":400}
	var dEnv struct {
		Msg    string `json:"msg"`
		Status int    `json:"status"`
	}
	if len(body) > 0 && json.Unmarshal(body, &dEnv) == nil && dEnv.Msg != "" {
		return &ExitError{
			Type:    typeFromStatus(status),
			Code:    codeFromStatus(status),
			Message: dEnv.Msg,
			Hint:    hintFromStatus(status),
			Detail:  json.RawMessage(body),
		}
	}

	// Fallback: raw body + status inference.
	msg := fmt.Sprintf("server returned status %d", status)
	if len(body) > 0 && len(body) < 2048 {
		msg = fmt.Sprintf("server returned status %d: %s", status, string(body))
	}
	return &ExitError{
		Type:    typeFromStatus(status),
		Code:    codeFromStatus(status),
		Message: msg,
		Hint:    hintFromStatus(status),
	}
}

func typeFromStatus(status int) string {
	switch {
	case status == 401:
		return "auth_error"
	case status == 403:
		return "permission"
	case status == 404:
		return "api_error"
	case status == 413:
		return "validation"
	case status == 429:
		return "rate_limited"
	case status >= 500 && status <= 599:
		return "api_error"
	case status >= 400 && status <= 499:
		return "validation"
	}
	return "api_error"
}

func codeFromStatus(status int) string {
	switch status {
	case 401:
		return "UNAUTHORIZED"
	case 403:
		return "FORBIDDEN"
	case 404:
		return "NOT_FOUND"
	case 413:
		return "PAYLOAD_TOO_LARGE"
	case 429:
		return "RATE_LIMITED"
	case 500:
		return "INTERNAL_ERROR"
	case 503:
		return "UPSTREAM_UNAVAILABLE"
	}
	return fmt.Sprintf("HTTP_%d", status)
}

func hintFromStatus(status int) string {
	if m, ok := backendErrorMapping[codeFromStatus(status)]; ok {
		return m.Hint
	}
	return ""
}
