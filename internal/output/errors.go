package output

import (
	"encoding/json"
	"errors"
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
// Uses errors.As so wrappers (e.g. retry sentinels) can still surface the
// structured exit info through their Unwrap chain.
func AsExitError(err error) *ExitError {
	if err == nil {
		return nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
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

// backendErrorMapping maps backend error codes to CLI taxonomy + hint. See
// docs/architecture-design.md §4.3.
//
// Two code conventions coexist because two backend families do: the matters /
// dmworkim family emits SCREAMING_SNAKE codes, octo-drive emits lowercase
// snake_case ones. Both are looked up in this single table, and a code and its
// cross-family counterpart carry the **same** Type — an agent must be able to
// branch on the CLI taxonomy and exit code without first working out which
// backend answered. Only the hint differs, so it can name the domain's own
// remedy. Codes not present here fall back to status-based inference, unchanged.
var backendErrorMapping = map[string]struct {
	Type string
	Hint string
}{
	"UNAUTHORIZED":                      {"auth_error", "check OCTO_TOKEN / OCTO_BOT_TOKEN; bot may be unpublished"},
	"AUTH_REQUIRED":                     {"auth_error", "provide a valid Octo or Loop bearer credential"},
	"AUTH_UNAVAILABLE":                  {"network", "auth service unreachable; retry later"},
	"VALIDATION_ERROR":                  {"validation", "check params with `octo-cli schema <op>`"},
	"MATTER_NOT_FOUND":                  {"api_error", "verify ID with `octo-cli matters list`"},
	"NOT_FOUND":                         {"api_error", "resource not found"},
	"ASSIGNEE_NOT_FOUND":                {"api_error", "assignee not in space or invalid UID"},
	"FORBIDDEN":                         {"permission", "bot lacks permission; check space membership"},
	"BOT_WORKSPACE_MEMBERSHIP_REQUIRED": {"permission", "ask a Workspace owner or admin to add this Bot in Workspace Members"},
	"SPACE_FORBIDDEN":                   {"permission", "bot not a member of this space"},
	"DUPLICATE_ASSIGNEE":                {"validation", "already assigned; check current assignees"},
	"DUPLICATE":                         {"validation", "the resource already exists; inspect the current resource before retrying"},
	"UNSUPPORTED_MEDIA_TYPE":            {"validation", "use the content type declared by `octo-cli schema <op>`"},
	"CLIENT_VERSION_TOO_OLD":            {"config", "upgrade octo-cli and retry"},
	"RATE_LIMITED":                      {"rate_limited", "server-side rate limit; retry after cooldown"},
	"UPSTREAM_UNAVAILABLE":              {"network", "upstream dependency unavailable; retry later"},
	"INTERNAL_ERROR":                    {"api_error", "internal server error; retry or report"},
	"PAYLOAD_TOO_LARGE":                 {"validation", "request body exceeds 1MB limit"},
	"CONFLICT":                          {"validation", "resource state conflicts; re-read and retry"},
	"PRECONDITION_FAILED":               {"validation", "base version stale; re-read to get the current base version, then retry"},
	"UNPROCESSABLE_ENTITY":              {"validation", "request understood but semantically invalid; check field shapes"},

	// octo-drive lowercase codes (drive spec §7.2). Each shares its Type with
	// the uppercase counterpart above: conflict/CONFLICT and
	// invalid_argument/VALIDATION_ERROR are both validation (exit 2),
	// not_found/NOT_FOUND both api_error, unauthorized/UNAUTHORIZED both
	// auth_error (exit 3).
	"unauthorized":      {"auth_error", "token invalid, revoked, or the user/bot is inactive; re-check the credential"},
	"auth_unavailable":  {"network", "auth service unreachable; retry later"},
	"permission_denied": {"permission", "caller lacks the required drive space role"},
	"password_required": {"permission", "pass --password for this share"},
	"wrong_password":    {"permission", "verify the share password"},
	"share_expired":     {"permission", "request a new share"},
	"not_found":         {"api_error", "verify the id/token and that the space is accessible"},
	"conflict":          {"validation", "drive state conflicts; re-read and retry"},
	"invalid_argument":  {"validation", "inspect the operation schema with `octo-cli schema <op>`"},
	"internal":          {"api_error", "internal server error; retry or report"},
}

// ParseBackendError converts an HTTP response body (and status) to an *ExitError.
// It tries the matters envelope first, then the octo-drive {error,message}
// shape, then dmworkim {msg,status}, falling back to a raw dump. The status
// code is used as a signal when the body is unparseable.
func ParseBackendError(status int, body []byte) *ExitError {
	return parseBackendError(status, body, false)
}

// ParsePublicAPIError parses a Fleet Public API error envelope. Unlike the
// legacy parser, unknown machine codes use the HTTP status as their taxonomy
// fallback. The protocol is selected by the caller; a presentation-only field
// such as hint must never change an agent's exit code.
func ParsePublicAPIError(status int, body []byte) *ExitError {
	return parseBackendError(status, body, true)
}

func parseBackendError(status int, body []byte, publicAPI bool) *ExitError {
	// Layer 1: matters envelope {"error":{"code":"...","message":"...","details":{...}}}
	var mEnv struct {
		Error struct {
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Details json.RawMessage `json:"details"`
			Hint    string          `json:"hint"`
		} `json:"error"`
	}
	if len(body) > 0 && json.Unmarshal(body, &mEnv) == nil && mEnv.Error.Code != "" {
		// Preserve the historical api_error classification for unknown legacy
		// codes. Public API callers explicitly opt into status-based fallback.
		fallbackType := "api_error"
		if publicAPI {
			fallbackType = typeFromStatus(status)
		}
		return newBackendError(mEnv.Error.Code, mEnv.Error.Message, fallbackType, mEnv.Error.Hint, body)
	}

	// Layer 2: octo-drive envelope {"error":"not_found","message":"..."} — the
	// code is a bare string rather than a nested object, and the human text is
	// under "message" (not "msg"). Only snake_case identifiers are treated as
	// codes; free-text `error` values (octo-drive's space middleware emits
	// "missing X-Space-Id", "invalid token", …) fall through to the raw
	// fallback below, exactly as they did before this layer existed.
	var dvEnv struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if len(body) > 0 && json.Unmarshal(body, &dvEnv) == nil && looksLikeErrorCode(dvEnv.Error) {
		msg := dvEnv.Message
		if msg == "" {
			msg = dvEnv.Error
		}
		return newBackendError(dvEnv.Error, msg, typeFromStatus(status), hintFromStatus(status), body)
	}

	// Layer 3: dmworkim {"msg":"...","status":400}
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
		// The backend's payload is carried even here. This branch is reached for
		// any shape the three envelope families do not match, and dropping the
		// body left message — truncated at 2048 bytes — as the only copy of the
		// backend's answer. Only valid JSON is attached: Detail is spliced into
		// the envelope raw, so non-JSON text would make the envelope unparseable.
		Detail: jsonDetail(body),
	}
}

// jsonDetail returns body as a raw JSON detail payload, or nil when it is not
// JSON and therefore cannot be embedded in the envelope.
func jsonDetail(body []byte) json.RawMessage {
	if len(body) == 0 || !json.Valid(body) {
		return nil
	}
	return json.RawMessage(body)
}

// IsErrorCodeShaped reports whether s has the shape of a machine-readable error
// code — lower-case alphanumerics and underscores.
//
// Shape alone is not evidence that a value *is* a code: a caller-supplied id can be
// all lower-case too. Use IsKnownErrorCode when the question is whether a value may
// be exempted from redaction.
func IsErrorCodeShaped(s string) bool {
	return looksLikeErrorCode(s)
}

// IsKnownErrorCode reports whether s is a code this CLI recognises — that is, one
// present in the backend error mapping.
//
// This is the strong form of the question, and the one the transport's response
// redaction asks. Membership in a closed, enumerated vocabulary is something a
// caller-supplied id cannot acquire, which is what makes it safe to leave such a
// value unmasked in the code position: the CLI prints these codes on every failure
// of their kind, so nothing is disclosed by not masking one.
func IsKnownErrorCode(s string) bool {
	_, ok := backendErrorMapping[s]
	return ok
}

// newBackendError builds the ExitError for a backend-supplied code. A code
// present in backendErrorMapping supplies the taxonomy and hint; an unmapped
// code keeps its literal code and takes the caller-supplied fallback taxonomy,
// which differs per envelope family so no existing classification shifts.
func newBackendError(code, message, fallbackType, fallbackHint string, body []byte) *ExitError {
	typ, hint := fallbackType, fallbackHint
	if m, ok := backendErrorMapping[code]; ok {
		typ, hint = m.Type, m.Hint
	}
	return &ExitError{
		Type:    typ,
		Code:    code,
		Message: message,
		Hint:    hint,
		Detail:  json.RawMessage(body),
	}
}

// looksLikeErrorCode reports whether s is a lowercase snake_case identifier,
// the shape octo-drive uses for machine-readable codes. Anything else (empty,
// spaces, mixed case, punctuation) is human prose and must not be promoted to
// an ExitError.Code.
func looksLikeErrorCode(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
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
	case 409:
		return "CONFLICT"
	case 412:
		return "PRECONDITION_FAILED"
	case 413:
		return "PAYLOAD_TOO_LARGE"
	case 422:
		return "UNPROCESSABLE_ENTITY"
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
