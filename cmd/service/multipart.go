package service

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/output"
)

// buildMultipartBody assembles a multipart/form-data payload for operations
// tagged x-octo-multipart. The binary upload is read from --file and attached
// under the "file" form field (backend uses FormFile("file")). Any promoted
// body flags the user set are included as form text fields.
func buildMultipartBody(cobraCmd *cobra.Command, rt *operationRuntime) ([]byte, string, error) {
	if rt.filePath == nil || *rt.filePath == "" {
		return nil, "", output.ErrValidation("--file is required for multipart upload", "pass --file <path>")
	}
	path := *rt.filePath
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		return nil, "", output.ErrWithHint("internal", "MULTIPART_FAILED", err.Error(), "")
	}
	if _, err := part.Write(data); err != nil {
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
