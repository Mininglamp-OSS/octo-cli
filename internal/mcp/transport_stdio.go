package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// ServeStdio runs the newline-delimited JSON-RPC loop over in/out (the MCP
// stdio transport). One JSON message per line in, one per line out;
// notifications produce no output. It returns nil on clean EOF.
//
// stdio is the local-debug / trusted-or-sandboxed-client transport only: the
// bearer token sits in the process env, reachable by a shell-capable host, so
// the over-privilege防护 here rests on an external trust assumption (design
// §5.5.1). streamable HTTP is the production transport.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReaderSize(in, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
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
