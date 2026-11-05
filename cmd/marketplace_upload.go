package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func registerMarketplaceUploadCmd(root *cobra.Command, f *cmdutil.Factory) {
	market := service.FindChild(root, "marketplace")
	if market == nil {
		return
	}
	uploads := service.FindChild(market, "skill-upload")
	if uploads == nil {
		return
	}
	create := service.FindChild(uploads, "create")
	if create == nil {
		return
	}
	generatedRun := create.RunE
	var localPath string
	create.Flags().StringVar(&localPath, "file", "", "local .zip or .skill archive; derive name/size and upload bytes before returning")
	create.Long += `

With --file <path>, derive file_name and file_size from the local archive and
stream it to storage. Do not combine --file with --data, --file-name or --file-size.
Without --file, initialization remains metadata-only.

The signed URL stays internal on the file path. Use the returned skill_upload_id
with skill-upload parse, poll skill-parse-task get, then plugin import and publish.
--dry-run checks the local file without creating an upload or transferring bytes.
A failed PUT does not trigger parsing. No automatic retry is made; a pending
upload may remain because the backend has no cancellation operation.`
	create.RunE = func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("file") {
			return generatedRun(cmd, args)
		}
		for _, flag := range []string{"data", "file-name", "file-size"} {
			if cmd.Flags().Changed(flag) {
				return failErr(f, output.ErrValidation("--file cannot be combined with --"+flag, "pass only --file; archive name and size are derived locally"))
			}
		}
		return runMarketplaceSkillUpload(cmd, f, localPath)
	}
}

type marketplaceUploadTarget struct {
	UploadID string            `json:"skill_upload_id"`
	URL      string            `json:"presigned_url"`
	Method   string            `json:"method"`
	Headers  map[string]string `json:"headers"`
}

func runMarketplaceSkillUpload(cmd *cobra.Command, f *cmdutil.Factory, path string) error {
	file, size, err := openMarketplaceArchive(path)
	if err != nil {
		return failErr(f, err)
	}
	defer file.Close() //nolint:errcheck // read-only descriptor
	requestPath, err := service.PathForOperation(f, "skill_upload.create")
	if err != nil {
		return failErr(f, err)
	}
	body := map[string]any{"file_name": filepath.Base(path), "file_size": size}
	if err = service.ValidateRequestBody(f, "skill_upload.create", body, nil); err != nil {
		return failErr(f, err)
	}
	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run": true, "operation": "skill_upload.create", "method": http.MethodPost,
			"path": requestPath, "body": body,
			"note": "no upload target is created, no bytes are uploaded, and parse is not triggered",
		})
	}
	serviceName, secrets, err := responseSecretMetadataForOperation(f, "skill_upload.create")
	if err != nil {
		return failErr(f, err)
	}
	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Service: serviceName, Method: http.MethodPost, Path: requestPath, Body: body,
		ResponseSecretFields: secrets, DisableRetry: true, UnknownOutcomeOnNetworkFailure: true,
	})
	if err != nil {
		return failErr(f, err)
	}
	// Decode errors can contain backend-controlled text; never echo them or raw.
	var reply struct {
		Data marketplaceUploadTarget `json:"data"`
	}
	if json.Unmarshal(raw, &reply) != nil || strings.TrimSpace(reply.Data.UploadID) == "" || reply.Data.Method != http.MethodPut {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"Skill upload initialization did not return a valid PUT target and upload id", "report the backend response shape without its signed URL"))
	}
	prepared := reply.Data
	loopback := apiOriginIsLoopback(f)
	target, uerr := assertSafeTransferURL("presigned_url", prepared.URL, loopback)
	if uerr != nil {
		return failErr(f, marketplaceUploadError(uerr, prepared.UploadID))
	}
	headers, herr := marketplaceUploadHeaders(prepared.Headers, size)
	if herr != nil {
		return failErr(f, marketplaceUploadError(herr, prepared.UploadID))
	}
	progressf(f, "uploading %d Skill archive bytes to object storage", size)
	if perr := putPresignedObject(cmd, f, file, size, target, headers, loopback); perr != nil {
		return failErr(f, marketplaceUploadError(perr, prepared.UploadID))
	}
	// Whitelist output: neither the raw response nor signed header values leave
	// this workflow. Parsing remains a separate, explicit API operation.
	return emitJSON(f, map[string]any{
		"skill_upload_id": prepared.UploadID, "uploaded_bytes": size,
	})
}

func openMarketplaceArchive(path string) (*os.File, int64, error) {
	invalid := func() (*os.File, int64, error) {
		return nil, 0, output.ErrValidation("<local-path> must be a readable, non-empty regular .zip or .skill file", "pass the Skill ZIP archive path")
	}
	// Probe before opening to refuse directories/devices/FIFOs without blocking.
	info, err := os.Stat(path)
	ext := strings.ToLower(filepath.Ext(path))
	if err != nil || !info.Mode().IsRegular() || (ext != ".zip" && ext != ".skill") {
		return invalid()
	}
	file, err := os.Open(path)
	if err != nil {
		return invalid()
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		_ = file.Close() //nolint:errcheck // best-effort close of an invalid read-only archive
		return invalid()
	}
	return file, info.Size(), nil
}

func marketplaceUploadHeaders(signed map[string]string, size int64) (http.Header, *output.ExitError) {
	headers := make(http.Header)
	for name, value := range signed {
		key := http.CanonicalHeaderKey(name)
		if len(headers.Values(key)) > 0 {
			return nil, output.ErrValidation("Skill upload returned duplicate signed headers", "report the backend response shape")
		}
		switch key {
		case "Authorization", "Proxy-Authorization", "Cookie", "Cookie2", "X-Space-Id", "Host", "Connection", "Transfer-Encoding", "Trailer":
			return nil, output.ErrValidation("Skill upload returned an unsupported signed header", "report the backend response shape")
		case "Content-Length":
			if value != strconv.FormatInt(size, 10) {
				return nil, output.ErrValidation("Skill upload signed size differs from the local archive", "initialize a new upload with the correct file size")
			}
			continue // net/http sets Content-Length from the open descriptor size.
		}
		headers.Set(key, value)
	}
	return headers, nil
}

func marketplaceUploadError(err *output.ExitError, uploadID string) *output.ExitError {
	// The API has no cancel endpoint; keep the id for diagnosis without claiming
	// that the pending upload was removed or starting a parse on partial bytes.
	err.Detail, _ = json.Marshal(map[string]any{"skill_upload_id": uploadID, "upload_confirmed": false}) //nolint:errcheck // only JSON scalar values
	err.Hint = fmt.Sprintf("upload %s was not confirmed; parse was not triggered; inspect the pending upload before retrying", uploadID)
	return err
}
