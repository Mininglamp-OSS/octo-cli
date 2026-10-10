package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// maxStdioMessageBytes caps a single newline-delimited message, matching the
// HTTP body cap, so a malformed or hostile peer cannot drive unbounded memory
// growth on one line.
const maxStdioMessageBytes = maxMCPMessageBytes

// ServeStdio runs the newline-delimited JSON-RPC loop over in/out (the MCP
// stdio transport). One JSON message per line in, one per line out;
// notifications produce no output. It returns nil on clean EOF.
//
// stdio is the local-debug / trusted-or-sandboxed-client transport only: the
// bearer token sits in the process env, reachable by a shell-capable host, so
// the over-privilege protection here rests on an external trust assumption. HTTP is the production transport.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	if closer, ok := in.(io.ReadCloser); ok {
		stop := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close() //nolint:errcheck // unblock a pending stdin read on shutdown
			case <-stop:
			}
		}()
		defer close(stop)
	}
	r := bufio.NewReaderSize(in, 64*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, oversized, err := readStdioMessage(r)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if oversized {
			if writeErr := writeMessage(out, newErrorResponse(nil, codeParseError, "message exceeds 8 MiB limit")); writeErr != nil {
				return writeErr
			}
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			if handleErr := s.handleStdioLine(ctx, trimmed, out); handleErr != nil {
				return handleErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readStdioMessage reads one newline-delimited message, accumulating across the
// reader's internal buffer boundary. If the message exceeds
// maxStdioMessageBytes it is dropped (oversized=true) but still drained to the
// next newline so the stream resynchronises rather than mis-framing the next
// message. The returned err is nil on a full line, io.EOF at end of input.
func readStdioMessage(r *bufio.Reader) (line []byte, oversized bool, err error) {
	var buf []byte
	for {
		chunk, e := r.ReadSlice('\n')
		if !oversized {
			if len(buf)+len(chunk) > maxStdioMessageBytes {
				oversized = true
				buf = nil // stop retaining; we will not parse this message
			} else {
				buf = append(buf, chunk...)
			}
		}
		if errors.Is(e, bufio.ErrBufferFull) {
			continue
		}
		return buf, oversized, e
	}
}

func (s *Server) handleStdioLine(ctx context.Context, line []byte, out io.Writer) error {
	req, code, err := decodeRPCRequest(line)
	if err != nil {
		prefix := "invalid JSON-RPC request: "
		if code == codeParseError {
			prefix = "parse error: "
		}
		return writeMessage(out, newErrorResponse(nil, code, prefix+err.Error()))
	}
	resp, has := s.Dispatch(ctx, req)
	if has {
		return writeMessage(out, resp)
	}
	return nil
}

// writeMessage encodes one JSON-RPC message as a single line (json.Encoder adds
// the trailing newline and does not indent).
func writeMessage(out io.Writer, resp rpcResponse) error {
	enc := json.NewEncoder(out)
	return enc.Encode(resp)
}
