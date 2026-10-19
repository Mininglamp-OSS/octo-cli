package mcp

import (
	"bytes"
	"strings"
	"testing"
)

// TestReadEnvelope_PrefersStdoutEnvelopeOverBenignStderr pins the stdout-first
// ordering: a benign diagnostic on stderr must not mask a real success
// envelope. (Reverting readEnvelope to stderr-first turns this red.)
func TestReadEnvelope_PrefersStdoutEnvelopeOverBenignStderr(t *testing.T) {
	out := bytes.NewBufferString(`{"ok":true,"data":{"x":1}}`)
	errb := bytes.NewBufferString("warning: benign diagnostic line\n")
	env, ok := readEnvelope(out, errb, nil)
	if !ok {
		t.Fatalf("a valid stdout envelope must win over a benign stderr line, got ok=false: %s", env)
	}
	if !strings.Contains(string(env), `"ok":true`) {
		t.Errorf("expected the stdout success envelope, got %s", env)
	}
}

// TestReadEnvelope_UsesErrEnvelopeWhenNoStdout keeps the other direction: an
// error envelope on stderr with no stdout is reported as a failure.
func TestReadEnvelope_UsesErrEnvelopeWhenNoStdout(t *testing.T) {
	errb := bytes.NewBufferString(`{"ok":false,"error":{"type":"validation","message":"x"}}`)
	env, ok := readEnvelope(new(bytes.Buffer), errb, nil)
	if ok {
		t.Fatalf("an error envelope must report ok=false, got ok=true: %s", env)
	}
	if !strings.Contains(string(env), `"ok":false`) {
		t.Errorf("expected the stderr error envelope, got %s", env)
	}
}
