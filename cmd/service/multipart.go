package service

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// buildMultipartBody assembles a multipart/form-data payload for operations
// tagged x-octo-multipart. The binary upload is streamed from --file via
// io.Copy (not ReadFile) to keep memory proportional to the copy buffer
// rather than file size. The file is attached under the "file" form field
// (backend uses FormFile("file")). Any promoted body flags the user set are
// included as form text fields. A pinned descriptor is borrowed from the caller
// and is never reopened or closed here.
func buildMultipartBody(cobraCmd *cobra.Command, rt *operationRuntime, pinned *os.File) (body []byte, contentType string, err error) { //nolint:gocyclo // multipart assembly handles borrowed descriptors, typed fields and I/O failures
	if rt.filePath == nil || *rt.filePath == "" {
		return nil, "", output.ErrValidation("--file is required for multipart upload", "pass --file <path>")
	}
	path := *rt.filePath

	f := pinned
	if f == nil {
		f, err = os.Open(path)
		if err != nil {
			return nil, "", output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
		}
		defer f.Close() //nolint:errcheck // ordinary CLI owns the file; MCP owns its pinned descriptor
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}

	// Any promoted body fields the user set become form text fields.
	for flagName, bf := range rt.bodyFlags {
		if !cobraCmd.Flags().Changed(flagName) {
			continue
		}
		var value string
		switch bf.kind {
		case kindInt:
			value = strconv.Itoa(*bf.intVal)
		case kindBool:
			value = strconv.FormatBool(*bf.boolVal)
		case kindStringSlice:
			// Flatten slice: one form field per value.
			for _, v := range *bf.strSlc {
				if err := w.WriteField(bf.apiName, v); err != nil {
					return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
				}
			}
			continue
		default:
			value = *bf.strVal
		}
		if err := w.WriteField(bf.apiName, value); err != nil {
			return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
		}
	}

	if err := w.Close(); err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}
	return buf.Bytes(), w.FormDataContentType(), nil
}
