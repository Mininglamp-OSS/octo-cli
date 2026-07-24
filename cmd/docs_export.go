package cmd

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

var docsExportFormats = map[string]bool{
	"md": true, "docx": true, "pdf": true, "xlsx": true, "png": true, "svg": true,
}

// registerDocsExportCmd adds the unified file export workflow. It is handwritten
// because the output path and its extension have cross-field validation, and
// because the wire endpoint differs between document files and board images.
func registerDocsExportCmd(root *cobra.Command, f *cmdutil.Factory) {
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

	var exportFormat, outputPath string
	cmd := &cobra.Command{
		Use:   "export <docId>",
		Short: "Export a document to a local file",
		Long: `Export an Octo document to a local file.

Use --export-format (rather than the global --format, which controls the output
envelope). The destination extension must match the selected export format.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsExport(cmd, f, args[0], exportFormat, outputPath)
		},
	}
	cmd.Flags().StringVar(&exportFormat, "export-format", "", "file format: md | docx | pdf | xlsx | png | svg (required)")
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "destination file path (required)")
	docs.AddCommand(cmd)
}

func validateDocsExport(format, outputPath string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if !docsExportFormats[format] {
		return "", output.ErrValidation("unsupported --export-format", "use md, docx, pdf, xlsx, png, or svg")
	}
	if outputPath == "" {
		return "", output.ErrValidation("--output is required", "pass -o with a destination file path")
	}
	gotExt := strings.ToLower(filepath.Ext(outputPath))
	wantExt := "." + format
	if gotExt != wantExt {
		return "", output.ErrValidation("output extension does not match --export-format", "use a "+wantExt+" destination path")
	}
	return format, nil
}

func runDocsExport(cmd *cobra.Command, f *cmdutil.Factory, docID, format, outputPath string) error {
	format, err := validateDocsExport(format, outputPath)
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // best-effort structured error
		return err
	}

	path := "/v1/bot/docs/" + url.PathEscape(docID) + "/export"
	if format == "md" || format == "docx" || format == "pdf" || format == "xlsx" {
		path += "/file"
	}
	query := url.Values{"format": []string{format}}

	if f.Globals != nil && f.Globals.DryRun {
		dryRun, marshalErr := json.Marshal(map[string]any{
			"dry_run":       true,
			"method":        http.MethodGet,
			"path":          path,
			"query":         map[string]string{"format": format},
			"output":        outputPath,
			"export_format": format,
		})
		if marshalErr != nil {
			return marshalErr
		}
		return f.EmitSuccess(dryRun)
	}

	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // the original error remains authoritative if rendering it also fails
		return err
	}
	result, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodGet,
		Path:                path,
		Query:               query,
		BinaryResponse:      true,
		OutputPath:          outputPath,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		_ = f.EmitError(err) //nolint:errcheck // the original error remains authoritative if rendering it also fails
		return err
	}
	return f.EmitSuccess(result)
}
