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
const maxStdioMessageBytes = 8 << 20

// ServeStdio runs the newline-delimited JSON-RPC loop over in/out (the MCP
// stdio transport). One JSON message per line in, one per line out;
// notifications produce no output. It returns nil on clean EOF.
//
// stdio is the local-debug / trusted-or-sandboxed-client transport only: the
// bearer token sits in the process env, reachable by a shell-capable host, so
// the over-privilege防护 here rests on an external trust assumption (design
// §5.5.1). HTTP is the production transport.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 64*1024)
	for {
		line, oversized, err := readStdioMessage(r)
		if oversized {
			writeMessage(out, newErrorResponse(nil, codeParseError, "message exceeds 8 MiB limit"))
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			s.handleStdioLine(ctx, trimmed, out)
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

func (s *Server) handleStdioLine(ctx context.Context, line []byte, out io.Writer) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeMessage(out, newErrorResponse(nil, codeParseError, "parse error: "+err.Error()))
		return
	}
	resp, has := s.Dispatch(ctx, req)
	if has {
		writeMessage(out, resp)
	}
}

// writeMessage encodes one JSON-RPC message as a single line (json.Encoder adds
// the trailing newline and does not indent).
func writeMessage(out io.Writer, resp rpcResponse) {
	enc := json.NewEncoder(out)
	_ = enc.Encode(resp)
}
