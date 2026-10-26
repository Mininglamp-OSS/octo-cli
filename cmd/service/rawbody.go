package service

import (
	"fmt"
	"io"
	"os"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// buildRawBody reads the --file bytes for an x-octo-raw-body operation, whose
// request body is the file itself. RawBodyMaxBytes, when declared, bounds the
// read: one byte past the limit is enough to refuse the upload locally rather
// than buffering an oversized file and letting the backend reject it. An empty
// file is refused too — every raw-body route at the pinned backends answers an
// empty body with 400. A pinned descriptor is borrowed from the caller (the MCP
// server's confined upload) and is never reopened or closed here.
func buildRawBody(rt *operationRuntime, pinned *os.File) ([]byte, error) {
	if rt.filePath == nil || *rt.filePath == "" {
		return nil, output.ErrValidation("--file is required for this upload", "pass --file <path>")
	}
	f := pinned
	if f == nil {
		opened, err := os.Open(*rt.filePath)
		if err != nil {
			return nil, output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
		}
		defer opened.Close() //nolint:errcheck // read-only descriptor cleanup
		f = opened
	}
	var r io.Reader = f
	limit := rt.detail.RawBodyMaxBytes
	if limit > 0 {
		r = io.LimitReader(f, limit+1)
	}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, output.ErrValidation(fmt.Sprintf("--file: %v", err), "check the file is readable")
	}
	if limit > 0 && int64(len(raw)) > limit {
		return nil, output.ErrValidation(
			fmt.Sprintf("--file exceeds the %d-byte limit for %s", limit, rt.detail.ID),
			"upload a smaller file; the backend enforces the same or a lower limit")
	}
	if len(raw) == 0 {
		return nil, output.ErrValidation("--file is empty", "the backend rejects an empty upload body")
	}
	return raw, nil
}
