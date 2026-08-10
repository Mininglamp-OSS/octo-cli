package cmd

import (
	"net/http"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// downloadURLResponse is the signed-URL reply shared by `download file` and the
// share-download endpoint.
type downloadURLResponse struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	ExpiresAt   string `json:"expires_at"`
}

// newDriveDownloadFileCmd builds `octo-cli drive download file <file-id>`.
func newDriveDownloadFileCmd(f *cmdutil.Factory) *cobra.Command {
	var outputPath string
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "file <file-id>",
		Short: "Download a blob to a local file",
		Long: `Download a drive blob to a local file.

Fetches a short-lived signed URL, then reads the bytes on a separate HTTP client
that carries no Octo credential — the signed URL is its own authorisation. The
bytes are written to "<output>.part", fsync'd, and renamed into place only after
a complete transfer, so an interrupted download never leaves a truncated file
that looks whole. An existing destination is refused unless --overwrite is set.

Use ` + "`drive download url`" + ` instead if you only want the signed URL.

Underlying operation: drive.download.url.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveDownloadFile(cmd, f, args[0], outputPath, overwrite)
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "destination file path (required)")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace the destination file if it already exists")
	_ = cmd.MarkFlagRequired("output") //nolint:errcheck // static flag name
	return cmd
}

func runDriveDownloadFile(cmd *cobra.Command, f *cmdutil.Factory, fileID, outputPath string, overwrite bool) error {
	if _, err := output.ParseUint64Decimal("<file-id>", fileID); err != nil {
		return failErr(f, err)
	}
	if outputPath == "" {
		return failErr(f, output.ErrValidation("--output is required", "pass -o with a destination file path"))
	}
	// Refuse an occupied destination before asking the backend for a URL, so a
	// mistake costs no signed URL and no transfer.
	if err := assertWritableTarget(outputPath, overwrite); err != nil {
		return failErr(f, err)
	}

	// Identity resolution and the allowed-token-kinds gate run before the dry-run
	// branch, matching the generated leaves: a credential of the wrong kind must
	// fail with TOKEN_KIND_NOT_ALLOWED rather than have --dry-run describe a
	// request it could never send.
	mount, err := service.MountForOperation(f, "drive.download.url")
	if err != nil {
		return failErr(f, err)
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":   true,
			"method":    http.MethodGet,
			"operation": "drive.download.url",
			"path":      mount + "/files/" + fileID + "/download",
			"output":    outputPath,
			"overwrite": overwrite,
			"note":      "dry run stops here: no signed URL is fetched and nothing is written to disk",
		})
	}

	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodGet,
		Path:                mount + "/files/" + fileID + "/download",
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return failErr(f, err)
	}
	var signed downloadURLResponse
	if derr := decodeLossless(raw, &signed); derr != nil {
		return failErr(f, derr)
	}
	if signed.URL == "" {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"the download endpoint returned no url", "report the backend response"))
	}

	result, ferr := fetchToFile(cmd, f, "url", signed.URL, outputPath, overwrite)
	if ferr != nil {
		return failErr(f, ferr)
	}
	if signed.Filename != "" {
		result.Filename = signed.Filename
	}
	return emitJSON(f, result)
}
