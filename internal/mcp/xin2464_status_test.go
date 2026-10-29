package mcp

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// The engine's RESULT_UNKNOWN is decided from the whole attempt history and
// keeps the Type of the last response, so a write that may have been applied
// and whose replay was refused with 400/401/409 carries Type validation or
// auth_error. The explicit unknown outcome must win over that Type; the same
// refusal on a first attempt is still an ordinary auth or validation failure.
func TestClassifyFailure_ResultUnknownWinsOverType(t *testing.T) {
	for _, c := range []struct {
		name string
		err  *output.ExitError
		want string
	}{
		{"unknown after 400", &output.ExitError{Type: "validation", Code: "RESULT_UNKNOWN", HTTPStatus: 400}, "result_unknown"},
		{"unknown after 401", &output.ExitError{Type: "auth_error", Code: "RESULT_UNKNOWN", HTTPStatus: 401}, "result_unknown"},
		{"unknown after 409", &output.ExitError{Type: "validation", Code: "RESULT_UNKNOWN", HTTPStatus: 409}, "result_unknown"},
		{"unknown after 404", &output.ExitError{Type: "api_error", Code: "RESULT_UNKNOWN", HTTPStatus: 404}, "result_unknown"},
		{"first-attempt 400", &output.ExitError{Type: "validation", Code: "VALIDATION_ERROR", HTTPStatus: 400}, "validation_error"},
		{"first-attempt 401", &output.ExitError{Type: "auth_error", Code: "UNAUTHORIZED", HTTPStatus: 401}, "auth_error"},
		{"first-attempt 409", &output.ExitError{Type: "validation", Code: "CONFLICT", HTTPStatus: 409}, "validation_error"},
		{"first-attempt 404", &output.ExitError{Type: "api_error", Code: "NOT_FOUND", HTTPStatus: 404}, "execution_error"},
		{"local validation", &output.ExitError{Type: "validation", Code: "VALIDATION_ERROR"}, "validation_error"},
		{"local auth", &output.ExitError{Type: "auth_error", Code: "NO_CREDENTIAL"}, "auth_error"},
	} {
		if got := classifyFailure("DELETE", nil, c.err); got != c.want {
			t.Errorf("%s: classifyFailure = %q, want %q", c.name, got, c.want)
		}
		if c.want == "result_unknown" {
			out := classifyExecuteResult([]byte(`{"ok":false}`), false, "docs.delete", "fp", false, "DELETE", c.err)
			if !out.IsError || len(out.Content) == 0 {
				t.Fatalf("%s: want an error tool result", c.name)
			}
			for _, w := range []string{`"status": "result_unknown"`, `"code": "RESULT_UNKNOWN"`, `"retryable": false`, "Do NOT auto-retry"} {
				if !strings.Contains(out.Content[0].Text, w) {
					t.Errorf("%s: missing %s: %s", c.name, w, out.Content[0].Text)
				}
			}
		}
	}
}
