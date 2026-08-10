// Package cmd — drive composite commands.
//
// Forty of the drive leaves are generated from internal/registry/specs/drive.json.
// The six in this file and its siblings are hand-written because they are not a
// single request: `upload file` runs prepare → object PUT → confirm, `download
// file` and `share download` fetch a presigned URL and then write bytes to disk,
// `share create` branches on the node type, and `share blob-create` / `share
// access` / `share download` take an argument shape (positional file id, whole
// share URL) the metadata engine cannot express. All of them resolve their mount
// through service.MountForOperation, so the bot/user-key routing and the
// token-kind gate come from the same spec metadata as the generated leaves.
package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// transferTimeout bounds a single object-storage transfer. It is deliberately
// far longer than the API timeout: a presigned PUT/GET moves the whole file,
// while every drive metadata call is a small JSON round-trip.
const transferTimeout = 30 * time.Minute

// registerDriveCmds attaches the hand-written drive leaves after the
// metadata-driven tree exists, so they can hang off the generated `drive
// upload` / `drive download` / `drive share` groups.
//
// Three generated leaves are detached first: `share blob-create`, `share
// access` and `share download` keep their spec operations (so `octo-cli schema
// drive.share.access` still describes the real endpoint) but their CLI surface
// is the hand-written one, which takes a positional file id / whole share URL.
func registerDriveCmds(root *cobra.Command, f *cmdutil.Factory) {
	drive := service.FindChild(root, "drive")
	if drive == nil {
		return
	}
	if upload := service.FindChild(drive, "upload"); upload != nil {
		upload.AddCommand(newDriveUploadFileCmd(f))
	}
	if download := service.FindChild(drive, "download"); download != nil {
		download.AddCommand(newDriveDownloadFileCmd(f))
	}
	if share := service.FindChild(drive, "share"); share != nil {
		service.RemoveLeaf(share, "blob-create")
		service.RemoveLeaf(share, "access")
		service.RemoveLeaf(share, "download")
		share.AddCommand(newDriveShareCreateCmd(f))
		share.AddCommand(newDriveShareBlobCreateCmd(f))
		share.AddCommand(newDriveShareAccessCmd(f))
		share.AddCommand(newDriveShareDownloadCmd(f))
	}
}

// --- transport for object storage ---

// transferClient is the HTTP client used for presigned object-storage
// PUT/GET. It is deliberately separate from the API client so a caller
// credential can never reach the storage endpoint: the presigned URL already
// carries its own authorisation, and forwarding an Octo token to a third-party
// host would leak it. Redirects are followed (storage gateways use them) but
// nothing else is inherited.
func transferClient() *http.Client {
	return &http.Client{Timeout: transferTimeout}
}

// assertSafeTransferURL rejects a presigned URL that is not safe to fetch.
// Only absolute http(s) URLs are allowed, plain http only for loopback hosts so
// local development works without weakening production. Embedded credentials
// are refused outright — a userinfo component would be silently sent to the
// host and can also be used to disguise the real target.
func assertSafeTransferURL(field, raw string) (*url.URL, *output.ExitError) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s is not a valid URL: %v", field, err),
			"the backend returned an unusable presigned URL; report it")
	}
	if u.Host == "" || !u.IsAbs() {
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must be an absolute URL", field),
			"the backend returned an unusable presigned URL; report it")
	}
	if u.User != nil {
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must not embed credentials", field),
			"the backend returned an unusable presigned URL; report it")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s uses plain http on a non-loopback host", field),
				"object storage must be https outside local development")
		}
	default:
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s uses unsupported scheme %q", field, u.Scheme),
			"only http (loopback) and https are fetched")
	}
	return u, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// --- local file writing ---

// downloadResult is the payload `download file` and `share download` emit.
type downloadResult struct {
	Path     string `json:"path"`
	Size     string `json:"size"`
	SHA256   string `json:"sha256"`
	Filename string `json:"filename"`
	ShareURL string `json:"share_url,omitempty"`
}

// fetchToFile downloads rawURL into target. The bytes land in a sibling
// "<target>.part" first, are fsync'd, and only then renamed over target, so an
// interrupted transfer never leaves a truncated file that looks complete and
// never clobbers an existing good copy. The partial file is removed on any
// failure. Unless overwrite is set, an existing target is refused before a
// single byte is fetched.
func fetchToFile(cmd *cobra.Command, f *cmdutil.Factory, field, rawURL, target string, overwrite bool) (*downloadResult, *output.ExitError) {
	if _, err := assertSafeTransferURL(field, rawURL); err != nil {
		return nil, err
	}
	if target == "" {
		return nil, output.ErrValidation("--output is required", "pass -o with a destination file path")
	}
	if err := assertWritableTarget(target, overwrite); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, output.ErrNetwork(err.Error(), "invalid download request")
	}
	resp, err := transferClient().Do(req)
	if err != nil {
		return nil, output.ErrNetwork(fmt.Sprintf("download: %v", err), "check network access to object storage")
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, output.ErrWithHint("api_error", "DOWNLOAD_FAILED",
			fmt.Sprintf("object storage returned status %d", resp.StatusCode),
			"the signed URL may have expired; re-run the command to get a fresh one")
	}

	partPath := target + ".part"
	part, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, output.ErrValidation(fmt.Sprintf("create %q: %v", partPath, err),
			"check the destination directory exists and is writable")
	}
	cleanup := func() { _ = os.Remove(partPath) } //nolint:errcheck // best-effort cleanup

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(part, hasher), resp.Body)
	if copyErr != nil {
		_ = part.Close() //nolint:errcheck // already returning the copy error
		cleanup()
		return nil, output.ErrNetwork(fmt.Sprintf("download: %v", copyErr), "transfer interrupted; the partial file was removed")
	}
	if err := part.Sync(); err != nil {
		_ = part.Close() //nolint:errcheck // already returning the sync error
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("flush %q: %v", partPath, err), "check available disk space")
	}
	if err := part.Close(); err != nil {
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("close %q: %v", partPath, err), "")
	}
	// Re-check just before the rename: --overwrite=false must not clobber a file
	// that appeared while the transfer was running.
	if err := assertWritableTarget(target, overwrite); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Rename(partPath, target); err != nil {
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("finalise %q: %v", target, err), "")
	}

	filename := resp.Header.Get("X-Octo-Filename")
	if filename == "" {
		filename = filepath.Base(target)
	}
	progressf(f, "downloaded %d bytes to %s", written, target)
	return &downloadResult{
		Path:     target,
		Size:     fmt.Sprintf("%d", written),
		SHA256:   hex.EncodeToString(hasher.Sum(nil)),
		Filename: filename,
	}, nil
}

// assertWritableTarget refuses an existing destination unless overwrite is set,
// and refuses a destination that is not a regular file even with --overwrite
// (renaming over a directory or a device would fail or do damage).
func assertWritableTarget(target string, overwrite bool) *output.ExitError {
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return output.ErrValidation(fmt.Sprintf("stat %q: %v", target, err), "check the destination path")
	}
	if !info.Mode().IsRegular() {
		return output.ErrValidation(
			fmt.Sprintf("%q exists and is not a regular file", target),
			"choose a different destination path")
	}
	if !overwrite {
		return output.ErrWithHint("validation", "FILE_EXISTS",
			fmt.Sprintf("%q already exists", target),
			"pass --overwrite to replace it, or choose another path")
	}
	return nil
}

// progressf writes a transfer progress line to stderr. It is gated on
// --verbose so stdout stays pure JSON and a normal agent run stays quiet.
func progressf(f *cmdutil.Factory, format string, args ...any) {
	if f.Globals == nil || !f.Globals.Verbose {
		return
	}
	fmt.Fprintf(f.ErrOut(), "[octo] "+format+"\n", args...) //nolint:errcheck // stderr progress line
}

// --- shared JSON helpers ---

// decodeLossless unmarshals a backend body with UseNumber so uint64 ids keep
// their exact decimal text on the way through the composite commands.
func decodeLossless(raw []byte, into any) *output.ExitError {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			fmt.Sprintf("unexpected response shape: %v", err),
			"report the response the backend returned")
	}
	return nil
}
