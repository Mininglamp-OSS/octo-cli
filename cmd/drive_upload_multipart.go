package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

const (
	multipartCapabilityHeader = "X-Drive-Upload-Capabilities"
	multipartCapabilityValue  = "multipart-v1"
	multipartPresignBatchMax  = 20
	multipartPartCountMax     = 10000
	multipartMaxPartRetries   = 3
)

// Multipart retrying is intentionally short: half a second total keeps a
// transient storage or presign failure from stalling an interactive command,
// while the delays still widen and let a brief outage recover.
const multipartRetryBaseDelay = 50 * time.Millisecond

// prepareDriveUploadRequest performs the capability handshake without guessing
// the mode locally. The first request is byte-compatible with the legacy
// single-part prepare. Only a client_capability_required refusal causes the
// second request to declare multipart-v1; the second reply is the one effective
// snapshot used for the rest of the command.
func prepareDriveUploadRequest(
	ctx context.Context,
	cli *client.Client,
	mount string,
	serviceName string,
	responseSecretFields []string,
	body map[string]any,
	size uint64,
) (raw []byte, capabilityDeclared bool, err error) {
	request := &client.Request{
		Service:              serviceName,
		Method:               http.MethodPost,
		Path:                 mount + "/files/prepare-upload",
		Body:                 body,
		ResponseSecretFields: responseSecretFields,
		SuppressSpaceHeader:  true,
	}
	raw, err = cli.Do(ctx, request)
	if err == nil {
		return raw, false, nil
	}
	if !isCapabilityRequired(err) {
		return nil, false, enrichFileTooLargeError(err, size)
	}

	request.Headers = map[string]string{
		multipartCapabilityHeader: multipartCapabilityValue,
	}
	raw, err = cli.Do(ctx, request)
	if err != nil {
		return nil, true, enrichFileTooLargeError(err, size)
	}
	return raw, true, nil
}

func isCapabilityRequired(err error) bool {
	ee := output.AsExitError(err)
	return ee != nil && ee.Code == "client_capability_required"
}

// enrichFileTooLargeError turns the backend's top-level 413 fields into a
// human-readable limit. The backend places max_size_kb beside error/message,
// not inside details, so a generic parser hint alone would omit the number.
func enrichFileTooLargeError(err error, declaredSize uint64) error {
	ee := output.AsExitError(err)
	if ee == nil || ee.Code != "file_too_large" {
		return err
	}
	var fields struct {
		MaxSizeKB json.Number `json:"max_size_kb"`
		Size      json.Number `json:"size"`
	}
	_ = json.Unmarshal(ee.Detail, &fields)

	maxKB := fields.MaxSizeKB.String()
	if maxKB == "" || maxKB == "0" {
		return err
	}
	limitBytes := "unknown"
	if kb, convErr := fields.MaxSizeKB.Int64(); convErr == nil && kb > 0 {
		limitBytes = output.Uint64JSONNumber(uint64(kb) * 1024).String()
	}
	ee.Message = fmt.Sprintf(
		"file is too large: declared size %d bytes exceeds the effective upload limit of %s KiB (%s bytes)",
		declaredSize, maxKB, limitBytes,
	)
	ee.Hint = "choose a smaller file; the server determines whether this account may use multipart and no upload bytes were sent"
	return ee
}

type multipartPart struct {
	number    uint64
	url       string
	expiresAt time.Time
}

type partsPresignResponse struct {
	Parts []struct {
		PartNumber uint64 `json:"part_number"`
		URL        string `json:"url"`
		ExpiresAt  string `json:"expires_at"`
	} `json:"parts"`
}

// uploadMultipart runs the complete multipart branch and returns the raw
// complete response. Every error after the effective prepare snapshot has a
// session to clean up, except RESULT_UNKNOWN: completion may already have won,
// so aborting there would be an unsafe guess.
func uploadMultipart(
	cmd *cobra.Command,
	f *cmdutil.Factory,
	cli *client.Client,
	mount string,
	file *os.File,
	size uint64,
	prepared *prepareUploadResponse,
) ([]byte, error) {
	fileID := prepared.FileID.String()
	if err := validateMultipartPrepare(file, size, prepared); err != nil {
		return nil, withMultipartAbort(cmd, f, cli, mount, fileID, prepared.UploadID, err)
	}

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()

	parts, err := presignAllMultipartParts(ctx, cli, f, mount, fileID, prepared)
	if err != nil {
		return nil, withMultipartAbort(cmd, f, cli, mount, fileID, prepared.UploadID, err)
	}

	etags, err := uploadMultipartParts(ctx, cancel, f, cli, mount, file, size, prepared, parts)
	if err != nil {
		return nil, withMultipartAbort(cmd, f, cli, mount, fileID, prepared.UploadID, err)
	}

	completed, err := completeMultipartUpload(ctx, cli, f, mount, fileID, prepared.UploadID, etags)
	if err != nil {
		ee := output.AsExitError(err)
		if ee != nil && (ee.Code == "RESULT_UNKNOWN" || ee.OutcomeUnknown()) {
			return nil, err
		}
		return nil, withMultipartAbort(cmd, f, cli, mount, fileID, prepared.UploadID, err)
	}
	return completed, nil
}

func validateMultipartPrepare(file *os.File, size uint64, prepared *prepareUploadResponse) *output.ExitError {
	if prepared.UploadID == "" {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart prepare did not return upload_id", "report the backend response")
	}
	if prepared.PartSize == 0 {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart prepare returned part_size 0", "report the backend response")
	}
	if prepared.PartCount == 0 || prepared.PartCount > multipartPartCountMax {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			fmt.Sprintf("multipart prepare returned invalid part_count %d", prepared.PartCount),
			"the contract allows 1..10000 parts")
	}
	if prepared.MaxConcurrentParts == 0 {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart prepare returned max_concurrent_parts 0", "report the backend response")
	}

	info, err := file.Stat()
	if err != nil {
		return output.ErrValidation(fmt.Sprintf("<local-path>: %v", err), "check the path and permissions")
	}
	if uint64(info.Size()) != size {
		return output.ErrValidation("<local-path> changed size after prepare", "retry the upload")
	}

	if prepared.PartCount > 1 && prepared.PartSize > ^uint64(0)/(prepared.PartCount-1) {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart part geometry overflows uint64", "report the backend response")
	}
	prefix := (prepared.PartCount - 1) * prepared.PartSize
	if prefix >= size {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart part_count and part_size exceed the declared file size", "report the backend response")
	}
	lastLength := size - prefix
	if lastLength > prepared.PartSize {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart part_count and part_size leave bytes outside the declared parts", "report the backend response")
	}
	if prefix > uint64(math.MaxInt64) || lastLength > uint64(math.MaxInt64) {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			"multipart part offset does not fit a local file offset", "report the backend response")
	}
	return nil
}

func presignAllMultipartParts(
	ctx context.Context,
	cli *client.Client,
	f *cmdutil.Factory,
	mount, fileID string,
	prepared *prepareUploadResponse,
) ([]multipartPart, error) {
	parts := make([]multipartPart, prepared.PartCount)
	for first := uint64(1); first <= prepared.PartCount; first += multipartPresignBatchMax {
		last := first + multipartPresignBatchMax - 1
		if last > prepared.PartCount {
			last = prepared.PartCount
		}
		numbers := make([]uint64, 0, multipartPresignBatchMax)
		for n := first; n <= last; n++ {
			numbers = append(numbers, n)
		}
		signed, err := presignMultipartParts(ctx, cli, f, mount, fileID, prepared.UploadID, numbers)
		if err != nil {
			return nil, err
		}
		for i, part := range signed {
			parts[first-1+uint64(i)] = part
		}
		progressf(f, "multipart prepared parts %d-%d of %d", first, last, prepared.PartCount)
	}
	return parts, nil
}

func presignMultipartParts(
	ctx context.Context,
	cli *client.Client,
	f *cmdutil.Factory,
	mount, fileID, uploadID string,
	numbers []uint64,
) ([]multipartPart, error) {
	if len(numbers) == 0 || len(numbers) > multipartPresignBatchMax {
		return nil, output.ErrWithHint("internal", "UPLOAD_PLAN_INVALID",
			fmt.Sprintf("presign batch has %d parts; the contract allows 1..%d", len(numbers), multipartPresignBatchMax),
			"report the CLI upload plan")
	}
	serviceName, responseSecretFields, err := responseSecretMetadataForOperation(f, "drive.upload.parts_presign")
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"upload_id":    uploadID,
		"part_numbers": numbers,
	}
	raw, err := cli.Do(ctx, &client.Request{
		Service:              serviceName,
		Method:               http.MethodPost,
		Path:                 mount + "/files/" + fileID + "/upload/parts-presign",
		Body:                 body,
		ResponseSecretFields: responseSecretFields,
		SuppressSpaceHeader:  true,
	})
	if err != nil {
		return nil, err
	}

	var reply partsPresignResponse
	if derr := decodeDriveResponse(raw, &reply); derr != nil {
		return nil, derr
	}
	return validatePresignedParts(reply.Parts, numbers)
}

func validatePresignedParts(
	raw []struct {
		PartNumber uint64 `json:"part_number"`
		URL        string `json:"url"`
		ExpiresAt  string `json:"expires_at"`
	},
	numbers []uint64,
) ([]multipartPart, error) {
	want := make(map[uint64]bool, len(numbers))
	for _, n := range numbers {
		want[n] = true
	}
	if len(raw) != len(numbers) {
		return nil, output.ErrWithHint("internal", "RESPONSE_DECODE",
			fmt.Sprintf("parts-presign returned %d parts for a %d-part request", len(raw), len(numbers)),
			"report the backend response")
	}
	parts := make([]multipartPart, len(raw))
	seen := make(map[uint64]bool, len(raw))
	for i, item := range raw {
		if !want[item.PartNumber] || seen[item.PartNumber] || item.URL == "" {
			return nil, output.ErrWithHint("internal", "RESPONSE_DECODE",
				"parts-presign returned a missing, duplicate, extra, or empty part",
				"report the backend response")
		}
		seen[item.PartNumber] = true
		expiry, _ := time.Parse(time.RFC3339, item.ExpiresAt)
		parts[i] = multipartPart{number: item.PartNumber, url: item.URL, expiresAt: expiry}
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].number < parts[j].number })
	return parts, nil
}

type partPutResult struct {
	etag      string
	signature bool
	retryable bool
	err       *output.ExitError
}

func uploadMultipartParts(
	ctx context.Context,
	cancel context.CancelFunc,
	f *cmdutil.Factory,
	cli *client.Client,
	mount string,
	file *os.File,
	size uint64,
	prepared *prepareUploadResponse,
	parts []multipartPart,
) ([]string, error) {
	concurrency := prepared.MaxConcurrentParts
	if concurrency > prepared.PartCount {
		concurrency = prepared.PartCount
	}
	workers := int(concurrency)
	loopbackAPI := apiOriginIsLoopback(f)
	storeClient := transferClient("part upload_url", loopbackAPI, nil, verboseNoter(f))
	etags := make([]string, prepared.PartCount)

	ctx, cancelWorkers := context.WithCancel(ctx)
	defer cancelWorkers()

	jobs := make(chan uint64)
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	fail := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for number := range jobs {
				if ctx.Err() != nil {
					return
				}
				part := parts[number-1]
				etag, err := uploadOneMultipartPart(
					ctx, f, cli, storeClient, mount, file, size, prepared, loopbackAPI, &part,
				)
				if err != nil {
					fail(err)
					return
				}
				etags[number-1] = etag
				progressf(f, "multipart uploaded part %d/%d", number, prepared.PartCount)
			}
		}()
	}

schedule:
	for n := uint64(1); n <= prepared.PartCount; n++ {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break schedule
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	err := firstErr
	errMu.Unlock()
	if err != nil {
		return nil, err
	}
	if cmdErr := ctx.Err(); cmdErr != nil {
		return nil, output.ErrWithHint("network", "UPLOAD_INTERRUPTED",
			"multipart upload was interrupted", "retry the upload; the incomplete session was aborted")
	}
	for i, etag := range etags {
		if etag == "" {
			return nil, output.ErrWithHint("internal", "UPLOAD_PART_MISSING",
				fmt.Sprintf("multipart upload finished without an ETag for part %d", i+1),
				"report this CLI bug")
		}
	}
	return etags, nil
}

func uploadOneMultipartPart(
	ctx context.Context,
	f *cmdutil.Factory,
	cli *client.Client,
	storeClient *http.Client,
	mount string,
	file *os.File,
	size uint64,
	prepared *prepareUploadResponse,
	loopbackAPI bool,
	part *multipartPart,
) (string, error) {
	for attempt := 0; attempt <= multipartMaxPartRetries; attempt++ {
		if attempt > 0 {
			delay := multipartRetryBaseDelay * time.Duration(1<<(attempt-1))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		if !part.expiresAt.IsZero() && !time.Now().Before(part.expiresAt) {
			resigned, err := resignOneMultipartPart(ctx, cli, f, mount, prepared.FileID.String(), prepared.UploadID, part.number)
			if err != nil {
				return "", err
			}
			*part = *resigned
		}

		offset := (part.number - 1) * prepared.PartSize
		length := prepared.PartSize
		if part.number == prepared.PartCount {
			length = size - offset
		}
		result := putMultipartPart(ctx, f, storeClient, file, offset, length, prepared, part, loopbackAPI)
		if result.err == nil {
			return result.etag, nil
		}
		if result.signature {
			if attempt == multipartMaxPartRetries {
				return "", result.err
			}
			resigned, err := resignOneMultipartPart(ctx, cli, f, mount, prepared.FileID.String(), prepared.UploadID, part.number)
			if err != nil {
				return "", err
			}
			*part = *resigned
			continue
		}
		if !result.retryable || attempt == multipartMaxPartRetries {
			return "", result.err
		}
	}
	return "", output.ErrWithHint("api_error", "UPLOAD_FAILED",
		fmt.Sprintf("multipart part %d failed after %d retries", part.number, multipartMaxPartRetries),
		"retry the upload")
}

func resignOneMultipartPart(
	ctx context.Context,
	cli *client.Client,
	f *cmdutil.Factory,
	mount string,
	fileID, uploadID string,
	number uint64,
) (*multipartPart, error) {
	parts, err := presignMultipartParts(ctx, cli, f, mount, fileID, uploadID, []uint64{number})
	if err != nil {
		return nil, err
	}
	if len(parts) != 1 || parts[0].number != number {
		return nil, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"parts-presign did not return exactly the part that was re-signed",
			"report the backend response")
	}
	return &parts[0], nil
}

func putMultipartPart(
	ctx context.Context,
	f *cmdutil.Factory,
	storeClient *http.Client,
	file *os.File,
	offset, length uint64,
	prepared *prepareUploadResponse,
	part *multipartPart,
	loopbackAPI bool,
) partPutResult {
	target, err := assertSafeTransferURL("part upload_url", part.url, loopbackAPI)
	if err != nil {
		return partPutResult{err: err}
	}
	section := io.NewSectionReader(file, int64(offset), int64(length))
	req, reqErr := http.NewRequestWithContext(ctx, http.MethodPut, part.url, section)
	if reqErr != nil {
		return partPutResult{err: transferNetworkError("part upload", target, reqErr), retryable: true}
	}
	req.ContentLength = int64(length)
	if prepared.ContentType != "" {
		req.Header.Set("Content-Type", prepared.ContentType)
	}
	if prepared.ContentDisposition != "" {
		req.Header.Set("Content-Disposition", prepared.ContentDisposition)
	}

	resp, doErr := storeClient.Do(req)
	if doErr != nil {
		return partPutResult{
			err:       transferNetworkError("part upload", target, doErr),
			retryable: true,
		}
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))

	switch {
	case resp.StatusCode == http.StatusForbidden:
		return partPutResult{
			signature: true,
			retryable: true,
			err: output.ErrWithHint("api_error", "UPLOAD_PART_FORBIDDEN",
				fmt.Sprintf("object storage rejected the signature for part %d", part.number),
				"re-signing only this part"),
		}
	case resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= 500:
		return partPutResult{
			retryable: true,
			err: output.ErrWithHint("api_error", "UPLOAD_PART_RETRYABLE",
				fmt.Sprintf("object storage returned status %d for part %d", resp.StatusCode, part.number),
				"retrying this part"),
		}
	case resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated:
		return partPutResult{
			err: output.ErrWithHint("api_error", "UPLOAD_FAILED",
				fmt.Sprintf("object storage returned status %d for part %d", resp.StatusCode, part.number),
				"the part request was rejected; the incomplete multipart session will be aborted"),
		}
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return partPutResult{
			retryable: true,
			err: output.ErrWithHint("api_error", "UPLOAD_NOT_CONFIRMED",
				fmt.Sprintf("object storage did not return an ETag for part %d", part.number),
				"retrying this part"),
		}
	}
	return partPutResult{etag: etag}
}

func completeMultipartUpload(
	ctx context.Context,
	cli *client.Client,
	f *cmdutil.Factory,
	mount, fileID, uploadID string,
	etags []string,
) ([]byte, error) {
	serviceName, responseSecretFields, err := responseSecretMetadataForOperation(f, "drive.upload.complete")
	if err != nil {
		return nil, err
	}
	parts := make([]map[string]any, len(etags))
	for i, etag := range etags {
		parts[i] = map[string]any{
			"part_number": i + 1,
			"etag":        etag,
		}
	}
	raw, err := cli.Do(ctx, &client.Request{
		Service:                        serviceName,
		Method:                         http.MethodPost,
		Path:                           mount + "/files/" + fileID + "/upload/complete",
		Body:                           map[string]any{"upload_id": uploadID, "parts": parts},
		ResponseSecretFields:           responseSecretFields,
		SuppressSpaceHeader:            true,
		UnknownOutcomeOnNetworkFailure: true,
	})
	if err != nil {
		if ee := output.AsExitError(err); ee != nil && ee.Code == "RESULT_UNKNOWN" {
			ee.Hint = "completion may already have succeeded; inspect the file status before starting a new upload, and do not retry this upload_id blindly"
			ee.Detail = appendCompleteContext(ee.Detail, fileID, uploadID)
		}
		return nil, err
	}
	return raw, nil
}

func appendCompleteContext(detail json.RawMessage, fileID, uploadID string) json.RawMessage {
	payload := map[string]any{
		"file_id":   fileID,
		"upload_id": uploadID,
		"backend_error": func() any {
			if len(detail) == 0 {
				return nil
			}
			return detail
		}(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return detail
	}
	return raw
}

// withMultipartAbort detaches cleanup from a cancelled command context and
// bounds it with the same five-second budget as single-part cancellation. The
// original error remains authoritative; cleanup state is added as detail.
func withMultipartAbort(
	cmd *cobra.Command,
	f *cmdutil.Factory,
	cli *client.Client,
	mount, fileID, uploadID string,
	cause error,
) error {
	ee := output.AsExitError(cause)
	if ee == nil {
		ee = output.ErrWithHint("internal", "UPLOAD_FAILED", cause.Error(), "")
	}
	abortCtx, stop := context.WithTimeout(context.WithoutCancel(cmd.Context()), cancelUploadTimeout)
	defer stop()

	abortResult := "aborted"
	abortService, abortSecrets, metadataErr := responseSecretMetadataForOperation(f, "drive.upload.abort")
	if metadataErr != nil {
		abortResult = "abort_failed: " + metadataErr.Error()
	} else if _, abortErr := cli.Do(abortCtx, &client.Request{
		Service:              abortService,
		Method:               http.MethodPost,
		Path:                 mount + "/files/" + fileID + "/upload/abort",
		Body:                 map[string]any{"upload_id": uploadID},
		ResponseSecretFields: abortSecrets,
		SuppressSpaceHeader:  true,
	}); abortErr != nil {
		if abortEE := output.AsExitError(abortErr); abortEE != nil && abortEE.Code == "upload_session_not_active" {
			abortResult = "not_active"
		} else {
			abortResult = "abort_failed: " + abortErr.Error()
		}
	}
	progressf(f, "multipart upload failed for file %s (%s)", fileID, abortResult)

	detail, merr := json.Marshal(map[string]any{
		"file_id":        fileID,
		"upload_id":      uploadID,
		"pending_upload": abortResult,
		"cause":          ee.Message,
		"cause_code":     ee.Code,
		"backend_error":  rawJSONOrNull(ee.Detail),
	})
	if merr == nil {
		ee.Detail = detail
	}
	if strings.HasPrefix(abortResult, "abort_failed:") {
		ee.Hint = strings.TrimSpace(ee.Hint +
			" The incomplete multipart session could not be aborted — run `octo-cli drive upload abort " +
			fileID + " --data '{\"upload_id\":\"" + uploadID + "\"}'`.")
	}
	return ee
}
