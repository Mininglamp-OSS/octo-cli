package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// prepareUploadResponse is the subset of the prepare-upload reply the composite
// needs. file_id stays a json.Number so a uint64 above 2^53 is never rounded.
type prepareUploadResponse struct {
	FileID             json.Number `json:"file_id"`
	UploadURL          string      `json:"upload_url"`
	ObjectPath         string      `json:"object_path"`
	ContentType        string      `json:"content_type"`
	ContentDisposition string      `json:"content_disposition"`
	MaxFileSize        int64       `json:"max_file_size"`
	ExpiresAt          string      `json:"expires_at"`
}

// newDriveUploadFileCmd builds `octo-cli drive upload file <local-path>`.
func newDriveUploadFileCmd(f *cmdutil.Factory) *cobra.Command {
	var spaceID, parentID, name, contentType string
	cmd := &cobra.Command{
		Use:   "file <local-path>",
		Short: "Upload a local file into a drive space (prepare + PUT + confirm)",
		Long: `Upload a local file into a drive space.

Runs the full three-step sequence: prepare-upload to create a pending file and
get a presigned PUT, the PUT itself, then confirm-upload. The PUT goes out on a
separate HTTP client that carries no Octo credential and no space header — the
presigned URL is its own authorisation. If the PUT or the confirm fails, the
pending file is cancelled on a best-effort basis and the error reports both the
file id and the cancel outcome, so no half-uploaded row is left behind silently.

--dry-run stops after describing the prepare request and the local file: no URL
is fetched, nothing is uploaded, and no pending row is created.

Underlying operations: drive.upload.prepare, drive.upload.confirm, drive.upload.cancel.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveUploadFile(cmd, f, args[0], driveUploadOpts{
				spaceID:     spaceID,
				parentID:    parentID,
				name:        name,
				contentType: contentType,
			})
		},
	}
	cmd.Flags().StringVar(&spaceID, "space-id", "", "destination drive space id (required)")
	cmd.Flags().StringVar(&parentID, "parent-id", "0", "destination folder id (decimal uint64 id, max "+output.MaxUint64Decimal+"); 0 is the space root")
	cmd.Flags().StringVar(&name, "name", "", "file name in the drive; defaults to the local base name")
	cmd.Flags().StringVar(&contentType, "content-type", "", "MIME type; detected from the file extension when omitted")
	_ = cmd.MarkFlagRequired("space-id") //nolint:errcheck // static flag name
	return cmd
}

type driveUploadOpts struct {
	spaceID     string
	parentID    string
	name        string
	contentType string
}

// runDriveUploadFile is the orchestrator. Each failure branch returns a distinct
// structured CLI error, which is why the branch count is high.
//
//nolint:gocyclo // one linear upload sequence; every branch is a separate covered failure mode
func runDriveUploadFile(cmd *cobra.Command, f *cmdutil.Factory, localPath string, o driveUploadOpts) error {
	// Open once and keep the descriptor: the size below is what gets signed, and
	// re-opening by path after the prepare round-trip would let a replacement at
	// that path be uploaded under the previous file's signed Content-Length.
	// Stat'ing the descriptor rather than the path closes the same window.
	file, openErr := os.Open(localPath)
	if openErr != nil {
		return failErr(f, output.ErrValidation(fmt.Sprintf("<local-path>: %v", openErr), "check the path and permissions"))
	}
	defer file.Close() //nolint:errcheck // read-only handle

	info, statErr := file.Stat()
	if statErr != nil {
		return failErr(f, output.ErrValidation(fmt.Sprintf("<local-path>: %v", statErr), "check the path and permissions"))
	}
	if !info.Mode().IsRegular() {
		return failErr(f, output.ErrValidation("<local-path> must point to a regular file", "pass a local file, not a directory or device"))
	}
	if info.Size() <= 0 {
		return failErr(f, output.ErrValidation("<local-path> is empty", "the backend signs an exact byte count; upload a non-empty file"))
	}
	parent, perr := output.ParseUint64Decimal("--parent-id", o.parentID)
	if perr != nil {
		return failErr(f, perr)
	}
	name := o.name
	if name == "" {
		name = filepath.Base(localPath)
	}
	ct := o.contentType
	if ct == "" {
		ct = detectContentType(localPath)
	}
	size := uint64(info.Size())

	prepareBody := map[string]any{
		"space_id":     o.spaceID,
		"parent_id":    output.Uint64JSONNumber(parent),
		"name":         name,
		"size":         output.Uint64JSONNumber(size),
		"content_type": ct,
	}

	// Identity resolution and the allowed-token-kinds gate come first, before the
	// dry-run branch: a generated leaf routes and gates before it describes
	// itself, so a composite that answered --dry-run with an unusable credential
	// would report a request the caller can never make. An unsupported kind is
	// TOKEN_KIND_NOT_ALLOWED / exit 2 here as it is there.
	mount, err := service.MountForOperation(f, "drive.upload.prepare")
	if err != nil {
		return failErr(f, err)
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":      true,
			"method":       http.MethodPost,
			"operation":    "drive.upload.prepare",
			"path":         mount + "/files/prepare-upload",
			"body":         prepareBody,
			"local_path":   localPath,
			"local_size":   fmt.Sprintf("%d", size),
			"content_type": ct,
			"note":         "dry run stops here: no presigned URL is fetched, no bytes are uploaded, no pending file is created",
		})
	}

	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}

	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/files/prepare-upload",
		Body:                prepareBody,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return failErr(f, err)
	}
	var prepared prepareUploadResponse
	if derr := decodeDriveResponse(raw, &prepared); derr != nil {
		return failErr(f, derr)
	}
	fileID := prepared.FileID.String()
	if fileID == "" || fileID == "0" {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"prepare-upload did not return a file_id", "report the backend response"))
	}

	// From here on a pending row exists: every failure path must try to cancel it.
	loopbackAPI := apiOriginIsLoopback(f)
	uploadURL, uerr := assertSafeTransferURL("upload_url", prepared.UploadURL, loopbackAPI)
	if uerr != nil {
		return failErr(f, withCancel(cmd, f, cli, mount, fileID, uerr))
	}
	progressf(f, "uploading %d bytes to object storage", size)
	if perr := putObject(cmd, file, int64(size), &prepared, uploadURL, loopbackAPI); perr != nil {
		return failErr(f, withCancel(cmd, f, cli, mount, fileID, perr))
	}
	progressf(f, "upload complete; confirming file %s", fileID)

	confirmed, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/files/" + fileID + "/confirm-upload",
		Body:                map[string]any{"actual_size": output.Uint64JSONNumber(size)},
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return failErr(f, withCancel(cmd, f, cli, mount, fileID, err))
	}

	normalized, nerr := output.NormalizeResponse(confirmed, nil, []string{"id", "parent_id"})
	if nerr != nil {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_NORMALIZE", nerr.Error(), ""))
	}
	notice, merr := json.Marshal(map[string]any{
		"uploaded_bytes": fmt.Sprintf("%d", size),
		"object_path":    prepared.ObjectPath,
	})
	if merr != nil {
		return failErr(f, merr)
	}
	return f.EmitSuccessWithMeta(normalized, output.EnvelopeMeta{Notice: notice})
}

// putObject streams the already-open local file to the presigned URL. The
// request echoes the Content-Type (and Content-Disposition when the backend set
// one) and sends an exact Content-Length, all of which the storage gateway
// signed — omitting or changing any of them makes it reject the upload with 403.
// No Authorization or X-Space-Id is set: the caller's Octo credential must never
// reach storage.
//
// The descriptor is the one that was stat'd for the signed size, not a fresh
// open: re-opening by path after the prepare round-trip would let a replacement
// at that path be sent under the previous file's signed Content-Length.
//
// A transport failure is reported through transferNetworkError, which names the
// host but never the URL: the presigned signature is in the query string.
func putObject(cmd *cobra.Command, file *os.File, size int64, prepared *prepareUploadResponse, target *url.URL, loopbackAPI bool) *output.ExitError {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return output.ErrValidation(fmt.Sprintf("<local-path>: %v", err), "check the path and permissions")
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPut, prepared.UploadURL, file)
	if err != nil {
		return transferNetworkError("upload", target, err)
	}
	req.ContentLength = size
	if prepared.ContentType != "" {
		req.Header.Set("Content-Type", prepared.ContentType)
	}
	if prepared.ContentDisposition != "" {
		req.Header.Set("Content-Disposition", prepared.ContentDisposition)
	}

	resp, err := transferClient("upload_url", loopbackAPI).Do(req)
	if err != nil {
		return transferNetworkError("upload", target, err)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return output.ErrWithHint("api_error", "UPLOAD_FAILED",
			fmt.Sprintf("object storage returned status %d", resp.StatusCode),
			"the presigned URL may have expired or the echoed headers/size did not match; retry the upload")
	}
	return nil
}

// cancelUploadTimeout bounds the detached best-effort cancel-upload. Short on
// purpose: the caller is already failing, and the cleanup must not add a long
// wait to the exit path.
const cancelUploadTimeout = 5 * time.Second

// withCancel attaches a best-effort cancel-upload to a failure so the pending
// row does not linger. The original error stays authoritative; the cancel
// outcome is reported alongside it as detail, including when the cancel itself
// failed — an operator needs to know a pending row may still exist.
func withCancel(cmd *cobra.Command, f *cmdutil.Factory, cli *client.Client, mount, fileID string, cause error) error {
	ee := output.AsExitError(cause)
	if ee == nil {
		ee = output.ErrWithHint("internal", "UPLOAD_FAILED", cause.Error(), "")
	}
	// The command context is cancelled by SIGINT/SIGTERM, and a caller
	// interrupting an upload is the most common way to get here — so sending the
	// cleanup on that context would mean the failure that triggers the cleanup has
	// already killed the channel the cleanup needs, leaving the pending row behind.
	// Detached, with its own short bound so an unreachable backend cannot hang the
	// process on the way out.
	cancelCtx, stop := context.WithTimeout(context.WithoutCancel(cmd.Context()), cancelUploadTimeout)
	defer stop()

	cancelResult := "cancelled"
	if _, err := cli.Do(cancelCtx, &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/files/" + fileID + "/cancel-upload",
		SuppressSpaceHeader: true,
	}); err != nil {
		cancelResult = "cancel_failed: " + err.Error()
	}
	progressf(f, "upload failed for file %s (%s)", fileID, cancelResult)

	detail, merr := json.Marshal(map[string]any{
		"file_id":       fileID,
		"pending_file":  cancelResult,
		"cause":         ee.Message,
		"cause_code":    ee.Code,
		"backend_error": rawJSONOrNull(ee.Detail),
	})
	if merr == nil {
		ee.Detail = detail
	}
	if cancelResult != "cancelled" {
		ee.Hint = strings.TrimSpace(ee.Hint + " The pending file could not be cancelled — run `octo-cli drive upload cancel " + fileID + "`.")
	}
	return ee
}

func rawJSONOrNull(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// detectContentType guesses a MIME type from the file extension, falling back to
// the generic binary type the storage gateway accepts.
func detectContentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}
