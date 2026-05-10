package output

import (
	"errors"
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
