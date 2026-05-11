package cmdutil

import (
	"bytes"
	"io"
	"os"
)

// IOStreams groups the three io streams a command uses: stdin (for @- input),
// stdout (for envelope output), and stderr (for diagnostics / error envelopes).
// Tests substitute in-memory buffers; production wires os.Stdin/Stdout/Stderr.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}

// NewStdIOStreams returns streams bound to the process stdio.
func NewStdIOStreams() *IOStreams {
	return &IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr}
}

// NewTestIOStreams returns in-memory streams plus the concrete buffers so the
// test can assert on captured output.
func NewTestIOStreams() (streams *IOStreams, inBuf, outBuf, errBuf *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &IOStreams{In: in, Out: out, ErrOut: errOut}, in, out, errOut
}
