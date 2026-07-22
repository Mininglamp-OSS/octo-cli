package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const maxDocsImportBytes int64 = 25 * 1024 * 1024

type docsImportFormat struct {
	name        string
	contentType string
	excalidraw  bool
}

// registerDocsImportCmd adds the file-oriented import workflow after the
// metadata-driven docs tree is built. The backend parses and applies each file
// atomically so a large converted document never has to pass through the generic
// JSON edit-body limit.
func registerDocsImportCmd(root *cobra.Command, f *cmdutil.Factory) {
	var docs *cobra.Command
	for _, child := range root.Commands() {
		if child.Name() == "docs" {
			docs = child
			break
		}
	}
	if docs == nil {
		return
	}

	var filePath string
	var mode string
	cmd := &cobra.Command{
		Use:   "import <docId>",
		Short: "Import a document, spreadsheet, or Excalidraw file",
		Long: `Import a local file into an existing Octo document.

The file extension selects the importer: .docx and .md/.markdown target a doc;
.xlsx targets a sheet; .excalidraw targets a board. Document and sheet imports
atomically replace existing content. Excalidraw imports default to merge, which
preserves existing board elements. --mode replace explicitly overwrites the board;
the backend creates a safety snapshot and applies concurrency protection.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsImport(cmd, f, args[0], filePath, mode, cmd.Flags().Changed("mode"))
		},
	}
	cmd.Flags().StringVar(&filePath, "file", "", "path to the .docx, .md, .markdown, .xlsx, or .excalidraw file (required)")
	cmd.Flags().StringVar(&mode, "mode", "merge", "Excalidraw import mode: merge preserves existing elements; replace explicitly overwrites with backend safety snapshot and concurrency protection")
	_ = cmd.MarkFlagRequired("file") //nolint:errcheck // static flag name
	docs.AddCommand(cmd)
}

func docsImportFormatForPath(path string) (docsImportFormat, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		return docsImportFormat{name: "docx", contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document"}, nil
	case ".md", ".markdown":
		return docsImportFormat{name: "markdown", contentType: "text/markdown"}, nil
	case ".xlsx":
		return docsImportFormat{name: "xlsx", contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"}, nil
	case ".excalidraw":
		return docsImportFormat{name: "excalidraw", contentType: "application/json", excalidraw: true}, nil
	default:
		return docsImportFormat{}, output.ErrValidation("unsupported import file type", "use a .docx, .md, .markdown, .xlsx, or .excalidraw file")
	}
}

func runDocsImport(cmd *cobra.Command, f *cmdutil.Factory, docID, filePath, mode string, modeChanged bool) error {
	format, err := docsImportFormatForPath(filePath)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort emit before returning err
		return err
	}
	if modeChanged && !format.excalidraw {
		ee := output.ErrValidation("--mode is only available for .excalidraw imports", "remove --mode or use an .excalidraw file")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	if format.excalidraw && mode != "merge" && mode != "replace" {
		ee := output.ErrValidation("--mode must be merge or replace", "use --mode merge to preserve existing elements, or --mode replace to explicitly overwrite the board")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	info, err := os.Stat(filePath)
	if err != nil {
		ee := output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	if !info.Mode().IsRegular() {
		ee := output.ErrValidation("--file must point to a regular file", "pass a local .docx, .md, .markdown, .xlsx, or .excalidraw file")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	if info.Size() == 0 {
		ee := output.ErrValidation("--file is empty", "pass a non-empty import file")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	if info.Size() > maxDocsImportBytes {
		ee := output.ErrValidation("--file exceeds the 25 MiB import limit", "use a smaller import file")
		_ = f.EmitError(ee) //nolint:errcheck
		return ee
	}
	var bytes []byte
	if format.excalidraw {
		bytes, err = os.ReadFile(filePath)
		if err != nil {
			ee := output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
			_ = f.EmitError(ee) //nolint:errcheck
			return ee
		}
		if err := validateExcalidrawEnvelope(bytes); err != nil {
			_ = f.EmitError(err) //nolint:errcheck
			return err
		}
	}
	if f.Globals != nil && f.Globals.DryRun {
		path := "/v1/bot/docs/" + url.PathEscape(docID) + "/import/" + format.name
		details := map[string]any{
			"dry_run":      true,
			"method":       http.MethodPost,
			"path":         path,
			"file":         filePath,
			"size":         info.Size(),
			"content_type": format.contentType,
		}
		if format.excalidraw {
			path += "?mode=" + url.QueryEscape(mode)
			details["path"] = path
			details["mode"] = mode
			details["semantics"] = map[string]any{
				"preserves_existing":  mode == "merge",
				"explicit_overwrite":  mode == "replace",
				"safety_snapshot":     mode == "replace",
				"concurrency_guarded": true,
			}
		}
		dryRun, marshalErr := json.Marshal(details)
		if marshalErr != nil {
			return marshalErr
		}
		return f.EmitSuccess(dryRun)
	}
	if bytes == nil {
		bytes, err = os.ReadFile(filePath)
		if err != nil {
			ee := output.ErrValidation(fmt.Sprintf("--file: %v", err), "check path and permissions")
			_ = f.EmitError(ee) //nolint:errcheck
			return ee
		}
	}

	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck
		return err
	}
	basePath := "/v1/bot/docs/" + url.PathEscape(docID)
	importPath := basePath + "/import/" + format.name
	var query url.Values
	if format.excalidraw {
		query = url.Values{"mode": []string{mode}}
	}
	parsedRaw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                importPath,
		Query:               query,
		Headers:             map[string]string{"X-Octo-Import-Apply": "true"},
		RawBody:             bytes,
		ContentType:         format.contentType,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck
		return err
	}
	var applied map[string]any
	if err := json.Unmarshal(parsedRaw, &applied); err != nil {
		return f.EmitSuccess(parsedRaw)
	}
	applied["format"] = format.name
	out, err := json.Marshal(applied)
	if err != nil {
		return err
	}
	return f.EmitSuccess(out)
}

func validateExcalidrawEnvelope(data []byte) error {
	var envelope struct {
		Type     json.RawMessage `json:"type"`
		Version  json.RawMessage `json:"version"`
		Elements json.RawMessage `json:"elements"`
		Files    json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return output.ErrValidation("invalid .excalidraw JSON", "pass a JSON Excalidraw envelope")
	}
	var kind string
	if err := json.Unmarshal(envelope.Type, &kind); err != nil || kind != "excalidraw" {
		return output.ErrValidation("invalid .excalidraw envelope: type must be excalidraw", "export the file from Excalidraw and try again")
	}
	var version int
	if err := json.Unmarshal(envelope.Version, &version); err != nil || version != 2 {
		return output.ErrValidation("invalid .excalidraw envelope: version must be 2", "export the file from Excalidraw and try again")
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(envelope.Elements, &elements); err != nil || elements == nil {
		return output.ErrValidation("invalid .excalidraw envelope: elements must be an array", "export the file from Excalidraw and try again")
	}
	var files map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Files, &files); err != nil || files == nil {
		return output.ErrValidation("invalid .excalidraw envelope: files must be an object", "export the file from Excalidraw and try again")
	}
	return nil
}
