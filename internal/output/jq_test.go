package output

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/itchyny/gojq"
)

func TestApplyJQ_Empty(t *testing.T) {
	v := map[string]any{"id": "x"}
	got, err := ApplyJQ(v, "")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestApplyJQ_FieldAccess(t *testing.T) {
	v := map[string]any{"data": map[string]any{"id": "t1"}}
	got, err := ApplyJQ(v, ".data.id")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 || got[0] != "t1" {
		t.Errorf("got %v", got)
	}
}

func TestApplyJQ_ArrayLength(t *testing.T) {
	v := map[string]any{"data": []any{1, 2, 3}}
	got, err := ApplyJQ(v, ".data | length")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
	// jq returns int as int for length
	switch n := got[0].(type) {
	case int:
		if n != 3 {
			t.Errorf("got %d", n)
		}
	case float64:
		if n != 3 {
			t.Errorf("got %v", n)
		}
	default:
		t.Errorf("unexpected type %T", got[0])
	}
}

func TestApplyJQ_Map(t *testing.T) {
	v := map[string]any{
		"data": []any{
			map[string]any{"id": "a"},
			map[string]any{"id": "b"},
		},
	}
	got, err := ApplyJQ(v, ".data[].id")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v", got)
	}
}

func TestApplyJQ_FromRawMessage(t *testing.T) {
	raw := json.RawMessage(`{"id":"x","n":7}`)
	got, err := ApplyJQ(raw, ".n")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d", len(got))
	}
}

func TestCompileJQ_ParserErrorsDoNotEchoIdentifiers(t *testing.T) {
	const marker = "app_REVIEW_FAKE_SECRET012345"
	_, err := CompileJQ(".data.token " + marker)
	if err == nil {
		t.Fatal("CompileJQ returned no error for malformed syntax")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("parse error exposed caller-provided identifier: %v", err)
	}
	cliErr := AsExitError(err)
	if cliErr == nil {
		t.Fatalf("parse error is not structured: %v", err)
	}
	if cliErr.Type != "config" || cliErr.Code != "CLI_ERROR" {
		t.Fatalf("parse error type/code = %q/%q, want config/CLI_ERROR", cliErr.Type, cliErr.Code)
	}
	if len(cliErr.Detail) != 0 || errors.Unwrap(err) != nil {
		t.Fatalf("parse error retained parser details or an underlying cause: %v", err)
	}
	for _, visible := range []string{cliErr.Message, cliErr.Hint, string(cliErr.Detail)} {
		if strings.Contains(visible, marker) {
			t.Fatalf("parse error field exposed caller-provided identifier: %q", visible)
		}
	}
}

func TestCompileJQ_CompilerErrorsDoNotEchoIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{
			name: "function",
			expr: "app_REVIEW_FAKE_SECRET012345(.)",
		},
		{
			name: "variable",
			expr: "$app_REVIEW_FAKE_SECRET012345",
		},
		{
			name: "module",
			expr: `import "app_REVIEW_FAKE_SECRET012345" as m; .`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := gojq.Parse(tc.expr); err != nil {
				t.Fatalf("test expression must parse before compilation: %v", err)
			}
			_, err := CompileJQ(tc.expr)
			if err == nil {
				t.Fatal("CompileJQ returned no error for invalid expression")
			}
			cliErr := AsExitError(err)
			if cliErr == nil {
				t.Fatalf("CompileJQ error is not structured: %v", err)
			}
			if cliErr.Type != "config" || cliErr.Code != "CLI_ERROR" {
				t.Fatalf("CompileJQ error type/code = %q/%q, want config/CLI_ERROR", cliErr.Type, cliErr.Code)
			}
			if len(cliErr.Detail) != 0 {
				t.Fatalf("CompileJQ error exposed compiler details: %s", cliErr.Detail)
			}
			if errors.Unwrap(err) != nil {
				t.Fatalf("CompileJQ error retained an underlying cause: %v", err)
			}
			for _, visible := range []string{err.Error(), cliErr.Message, cliErr.Hint, string(cliErr.Detail)} {
				if strings.Contains(visible, "app_REVIEW_FAKE_SECRET012345") {
					t.Fatalf("CompileJQ error exposed caller-provided identifier: %q", visible)
				}
			}
		})
	}
}

func TestCompileJQ_ValidExpressionCanRun(t *testing.T) {
	program, err := CompileJQ(".id")
	if err != nil {
		t.Fatalf("CompileJQ: %v", err)
	}
	got, err := program.Apply(map[string]any{"id": "allowed"})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(got) != 1 || got[0] != "allowed" {
		t.Fatalf("got %v, want [allowed]", got)
	}
}

func TestApplyJQ_NoMatches(t *testing.T) {
	// Select filter that yields nothing.
	got, err := ApplyJQ(map[string]any{"a": []any{1, 2, 3}}, ".a[] | select(. > 10)")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no results, got %v", got)
	}
}

func TestApplyJQ_MultipleResults(t *testing.T) {
	got, err := ApplyJQ(map[string]any{"a": []any{1, 2, 3}}, ".a[]")
	if err != nil {
		t.Fatalf("ApplyJQ: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestApplyJQ_RuntimeError(t *testing.T) {
	// jq .foo on a number is a runtime error (not a parse error).
	_, err := ApplyJQ(float64(42), ".foo")
	if err == nil {
		t.Error("expected runtime error for field access on scalar")
	}
}

func TestNormalizeForJQ_PrimitiveFastPath(t *testing.T) {
	// Primitives round-trip as-is (no JSON marshal round-trip).
	out, err := normalizeForJQ("hello")
	if err != nil {
		t.Fatalf("normalizeForJQ: %v", err)
	}
	if out != "hello" {
		t.Errorf("got %v", out)
	}
}

func TestNormalizeForJQ_MapFastPath(t *testing.T) {
	in := map[string]any{"a": 1}
	out, err := normalizeForJQ(in)
	if err != nil {
		t.Fatalf("normalizeForJQ: %v", err)
	}
	if m, ok := out.(map[string]any); !ok || m["a"] != 1 {
		t.Errorf("got %v", out)
	}
}

// TestApplyJQ_ARuntimeErrorDoesNotEchoTheValue is round-14's --jq item, and the irony is
// the point: a caller reaches for --jq to narrow what gets printed, and a filter that fails
// at runtime printed the value it choked on — which is response data, and on a share
// command that is the share token. stdout stayed empty while stderr carried the secret.
//
// gojq's message quotes the offending value and nothing in the output layer has a secret
// list to mask against, so the value is not repeated at all.
func TestApplyJQ_ARuntimeErrorDoesNotEchoTheValue(t *testing.T) {
	const token = "SHARETOKEN0123456789"
	body := []byte(`{"data":{"share_token":"` + token + `"}}`)

	_, err := ApplyJQ(body, ".data.share_token|tonumber")
	if err == nil {
		t.Fatal("converting a non-numeric string with tonumber must fail")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("the jq runtime error echoed the response value: %v", err)
	}
	ee := AsExitError(err)
	if ee == nil {
		t.Fatalf("the error must be structured, or WrapCLIError files it as a config error "+
			"and prints its text: %v", err)
	}
	if ee.Type != "validation" {
		t.Errorf("Type = %q, want validation", ee.Type)
	}
}
