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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
// host would leak it. Nothing else is inherited either.
//
// Redirects are followed for a GET (storage gateways use them) but every hop is
// re-validated by assertSafeTransferTarget, so the https-or-loopback-http rule
// holds for the destination that actually serves the bytes and not merely for
// the first URL the backend handed over. An unsafe hop fails the transfer rather
// than downgrading it, and the 10-hop cap Go's default policy applies is kept.
//
// A redirect that would change the method, or any redirect on a body-carrying
// request, is refused. For 301/302/303 Go rewrites a PUT into a bodiless GET, so
// a storage host answering 2xx after such a hop would report a successful upload
// with nothing written — and the caller would then confirm a drive row pointing
// at an object that does not exist. (307/308 preserve the method and fail on their
// own, because an *os.File body cannot be replayed.) Refusing is not merely safe
// but correct: a presigned signature is bound to the original URL and can never be
// honoured at a different one, so there is no legitimate upload redirect to lose.
//
// The Referer header is dropped on every hop. Go fills it in from the previous
// request's full URL, and for a presigned URL that includes the signature in the
// query string — a short-lived bearer credential for the object. The redirect
// target addresses its own URL and never presents the original signature, so it
// has no use for the value; sending it would leave object read (GET) or write
// (PUT) access sitting in a third party's access log.
//
// loopbackAPI reflects whether the configured Octo origin is itself loopback. It
// gates the plain-http exception: that exception exists so local development
// works, and against a remote origin a cooperating storage host could otherwise
// answer 302 http://127.0.0.1:<port>/… and steer the transfer at a service on
// the caller's own machine. A redirect may not land on a loopback host at all
// under a remote origin, whatever its scheme — the initial URL comes from the
// trusted backend and may legitimately name an internal host, but a hop chosen by
// the storage host may not.
func transferClient(field string, loopbackAPI bool) *http.Client {
	return &http.Client{
		Timeout: transferTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Header.Del("Referer")
			if len(via) >= maxTransferRedirects {
				return fmt.Errorf("stopped after %d redirects", maxTransferRedirects)
			}
			if err := assertRedirectKeepsTheRequest(field, req, via); err != nil {
				return err
			}
			if err := assertSafeTransferTarget(field, req.URL, loopbackAPI); err != nil {
				return err
			}
			if !loopbackAPI && isLoopbackHost(req.URL.Hostname()) {
				return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
					fmt.Sprintf("%s redirected to a loopback host, which is not reachable object storage", field),
					"the storage endpoint redirected the transfer at the local machine; report it")
			}
			return nil
		},
	}
}

// assertRedirectKeepsTheRequest refuses a redirect that would not carry the
// original request. Go silently rewrites a PUT into a bodiless GET on 301/302/303,
// which for an upload turns "the object was never written" into a 2xx the caller
// reports as success.
func assertRedirectKeepsTheRequest(field string, req *http.Request, via []*http.Request) *output.ExitError {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	if req.Method != previous.Method {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s redirected a %s into a %s, which would not carry the request body",
				field, previous.Method, req.Method),
			"the storage endpoint answered with a method-changing redirect; report it")
	}
	if previous.Method != http.MethodGet && previous.Method != http.MethodHead {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s redirected a %s request; a presigned signature is bound to its own URL and cannot be honoured elsewhere",
				field, previous.Method),
			"the storage endpoint redirected an upload; report it")
	}
	return nil
}

// maxTransferRedirects mirrors the cap Go's default redirect policy applies.
// Setting CheckRedirect replaces that policy wholesale, so the cap has to be
// restated or a redirect loop would run until the transfer timeout.
const maxTransferRedirects = 10

// apiOriginIsLoopback reports whether the configured Octo origin is a loopback
// host, which is the only situation in which plain-http object storage is
// accepted. A config that cannot be read is treated as non-loopback: the strict
// rule is the safe default.
func apiOriginIsLoopback(f *cmdutil.Factory) bool {
	cfg, err := f.Config()
	if err != nil {
		return false
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return false
	}
	return isLoopbackHost(origin.Hostname())
}

// assertSafeTransferURL rejects a presigned URL that is not safe to fetch, and
// returns it parsed for the caller.
func assertSafeTransferURL(field, raw string, loopbackAPI bool) (*url.URL, *output.ExitError) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Error's text embeds the whole raw URL, signature included, so the
		// parse cause is reported without it.
		return nil, output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s is not a valid URL: %v", field, urlParseCause(err)),
			"the backend returned an unusable presigned URL; report it")
	}
	if serr := assertSafeTransferTarget(field, u, loopbackAPI); serr != nil {
		return nil, serr
	}
	return u, nil
}

// urlParseCause strips the *url.Error wrapper that url.Parse returns, whose
// Error() quotes the entire input URL.
func urlParseCause(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}

// assertSafeTransferTarget holds the transfer safety rules for an already-parsed
// URL, so the initial presigned URL and every redirect hop are judged by exactly
// the same code. Only absolute http(s) URLs are allowed, plain http only for
// loopback hosts and only when the configured Octo origin is itself loopback, so
// local development works without weakening production. Embedded credentials are
// refused outright — a userinfo component would be silently sent to the host and
// can also be used to disguise the real target.
func assertSafeTransferTarget(field string, u *url.URL, loopbackAPI bool) *output.ExitError {
	if u == nil || u.Host == "" || !u.IsAbs() {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must be an absolute URL", field),
			"the backend returned an unusable presigned URL; report it")
	}
	if u.User != nil {
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s must not embed credentials", field),
			"the backend returned an unusable presigned URL; report it")
	}
	if err := assertNumericHostIsAnIP(field, u); err != nil {
		return err
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s uses plain http on a non-loopback host", field),
				"object storage must be https outside local development")
		}
		if !loopbackAPI {
			return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
				fmt.Sprintf("%s points at a loopback host, but the configured Octo origin is not local", field),
				"plain-http loopback object storage is accepted only against a local Octo origin")
		}
	default:
		return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
			fmt.Sprintf("%s uses unsupported scheme %q", field, u.Scheme),
			"only http (loopback) and https are fetched")
	}
	return nil
}

// transferNetworkError reports an object-storage transfer failure without
// printing the URL.
//
// A presigned URL's signature lives in its query string, which makes the whole
// URL a short-lived bearer credential for that object — and *url.Error.Error()
// embeds it, so formatting the error straight into an envelope publishes it on
// stderr with no --verbose needed. The host is the part that carries the
// diagnostic value ("can I reach storage at all"), and the unwrapped cause keeps
// the actual reason (connection refused, TLS failure, timeout).
//
// A rejection raised by our own CheckRedirect arrives wrapped in *url.Error;
// that ExitError is returned as-is so an unsafe redirect keeps reporting
// UNSAFE_PRESIGNED_URL rather than being reclassified as a network fault.
func transferNetworkError(op string, u *url.URL, err error) *output.ExitError {
	cause := unwrapTransferError(err)
	if ee := output.AsExitError(cause); ee != nil {
		return ee
	}
	host := "object storage"
	if u != nil && u.Host != "" {
		host = strconv.Quote(u.Host)
	}
	return output.ErrNetwork(
		fmt.Sprintf("%s against %s failed: %v", op, host, cause),
		"check network access to object storage")
}

// unwrapTransferError strips the *url.Error wrappers whose Error() would print
// the presigned URL, leaving the cause that actually describes the failure.
func unwrapTransferError(err error) error {
	for {
		var ue *url.Error
		if !errors.As(err, &ue) || ue.Err == nil {
			return err
		}
		err = ue.Err
	}
}

// isLoopbackHost reports whether host names the local machine.
//
// The host is lower-cased and a trailing root dot stripped first, because
// url.Parse normalises neither while a resolver treats "LOCALHOST", "localhost."
// and "localhost" alike. Both directions this helper gates were wrong without
// that: a hop onto "LOCALHOST" was followed under a remote origin, and a
// developer whose configured origin read "LOCALHOST" was refused plain-http
// object storage.
//
// A numeric-looking host that net.ParseIP rejects is handled by
// assertNumericHostIsAnIP rather than here, so no resolver lookup happens on a
// validation path.
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	// RFC 6761 reserves .localhost for the loopback interface.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// assertNumericHostIsAnIP refuses a host made only of digits and dots that is
// not a valid IP address. A zero-padded dotted quad such as 127.000.000.001 is
// rejected by net.ParseIP but accepted by the resolver, so treating it as a name
// would let it slip past the loopback rules above in whichever direction those
// are being applied. No legitimate storage host is spelled that way, and
// resolving it here would put a DNS lookup on a validation path.
func assertNumericHostIsAnIP(field string, u *url.URL) *output.ExitError {
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || net.ParseIP(host) != nil {
		return nil
	}
	if strings.TrimFunc(host, func(r rune) bool { return r == '.' || (r >= '0' && r <= '9') }) != "" {
		return nil // contains something other than digits and dots: a real name
	}
	return output.ErrWithHint("api_error", "UNSAFE_PRESIGNED_URL",
		fmt.Sprintf("%s has a numeric host %q that is not a valid IP address", field, u.Hostname()),
		"a zero-padded or malformed numeric host is refused rather than resolved; report it")
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

// fetchToFile downloads rawURL into target. The bytes land in a randomly-named
// sibling "<base>.<random>.part" first, are fsync'd, and only then renamed over
// target, so an interrupted transfer never leaves a truncated file that looks
// complete. The partial file is removed on any failure. Unless overwrite is set,
// an existing target is refused before a single byte is fetched.
func fetchToFile(cmd *cobra.Command, f *cmdutil.Factory, field, rawURL, target string, overwrite bool) (*downloadResult, *output.ExitError) {
	loopbackAPI := apiOriginIsLoopback(f)
	u, err := assertSafeTransferURL(field, rawURL, loopbackAPI)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, output.ErrValidation("--output is required", "pass -o with a destination file path")
	}
	if err := assertWritableTarget(target, overwrite); err != nil {
		return nil, err
	}

	req, rerr := http.NewRequestWithContext(cmd.Context(), http.MethodGet, rawURL, http.NoBody)
	if rerr != nil {
		return nil, transferNetworkError("download", u, rerr)
	}
	resp, rerr := transferClient(field, loopbackAPI).Do(req)
	if rerr != nil {
		return nil, transferNetworkError("download", u, rerr)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, output.ErrWithHint("api_error", "DOWNLOAD_FAILED",
			fmt.Sprintf("object storage returned status %d", resp.StatusCode),
			"the signed URL may have expired; re-run the command to get a fresh one")
	}

	// A random part file, created O_EXCL by os.CreateTemp, rather than a
	// predictable "<target>.part": the fixed name could be pre-created as a
	// symlink by anyone able to write the destination directory (truncating
	// whatever it pointed at with downloaded bytes — assertWritableTarget Lstats
	// target, never the part file), and two concurrent downloads to the same
	// destination would interleave into one file.
	part, oerr := os.CreateTemp(filepath.Dir(target), filepath.Base(target)+".*.part")
	if oerr != nil {
		return nil, output.ErrValidation(fmt.Sprintf("create a partial file next to %q: %v", target, oerr),
			"check the destination directory exists and is writable")
	}
	partPath := part.Name()
	cleanup := func() { _ = os.Remove(partPath) } //nolint:errcheck // best-effort cleanup

	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(part, hasher), resp.Body)
	if copyErr != nil {
		_ = part.Close() //nolint:errcheck // already returning the copy error
		cleanup()
		ee := transferNetworkError("download", u, copyErr)
		ee.Hint = "transfer interrupted; the partial file was removed"
		return nil, ee
	}
	if err := part.Sync(); err != nil {
		_ = part.Close() //nolint:errcheck // already returning the sync error
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("flush the partial file for %q: %v", target, err), "check available disk space")
	}
	if err := part.Close(); err != nil {
		cleanup()
		return nil, output.ErrValidation(fmt.Sprintf("close the partial file for %q: %v", target, err), "")
	}
	// Re-check just before the rename so --overwrite=false does not clobber a
	// file that appeared while the transfer was running. This narrows the window
	// but cannot close it: POSIX rename replaces unconditionally, so a file
	// created between this check and the rename below is still replaced.
	// --overwrite=false is therefore best-effort against a concurrent writer,
	// which is the right trade for a CLI — the alternative (linkat/O_EXCL) would
	// lose atomic replacement.
	if err := assertWritableTarget(target, overwrite); err != nil {
		cleanup()
		return nil, err
	}
	// Publish. The mode is set explicitly rather than left to os.CreateTemp's
	// 0600, so a download does not silently tighten a destination the caller had
	// deliberately made readable: an existing target keeps its own mode, a fresh
	// one gets 0600. (The binary --output path in internal/client uses 0644, which
	// is why leaving this implicit made two downloads in one CLI disagree.)
	if err := publishDownload(partPath, target, overwrite); err != nil {
		cleanup()
		return nil, err
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

// publishDownload moves the completed partial file onto target.
//
// Without --overwrite the publication is a hard link, which fails with EEXIST
// atomically and in the same directory — so "refuse an existing destination"
// becomes an actual guarantee rather than a check-then-rename window a second
// writer can slip through. With --overwrite, replacement is the point, so rename
// is correct and unconditional replacement is what the caller asked for. A
// filesystem without hard links falls back to rename, which is the previous
// behaviour rather than a failure.
//
// The mode is applied before the file is visible under its final name.
func publishDownload(partPath, target string, overwrite bool) *output.ExitError {
	if err := applyDownloadMode(partPath, target); err != nil {
		return err
	}
	if !overwrite {
		switch err := os.Link(partPath, target); {
		case err == nil:
			_ = os.Remove(partPath) //nolint:errcheck // best-effort cleanup of the link source
			return nil
		case errors.Is(err, os.ErrExist):
			return output.ErrWithHint("validation", "FILE_EXISTS",
				fmt.Sprintf("%q already exists", target),
				"pass --overwrite to replace it, or choose another path")
		}
		// Hard links unsupported (or cross-device): fall through to rename, which
		// is what this did before and is still guarded by the re-check above.
	}
	if err := os.Rename(partPath, target); err != nil {
		return output.ErrValidation(fmt.Sprintf("finalise %q: %v", target, err), "")
	}
	return nil
}

// applyDownloadMode gives the partial file the mode its destination should end up
// with: an existing target's own mode, so --overwrite does not narrow a file the
// caller widened on purpose, and 0600 for a fresh one.
func applyDownloadMode(partPath, target string) *output.ExitError {
	mode := os.FileMode(0o600)
	if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	if err := os.Chmod(partPath, mode); err != nil {
		return output.ErrValidation(fmt.Sprintf("set mode on the partial file for %q: %v", target, err), "")
	}
	return nil
}

// assertWritableTarget refuses an existing destination unless overwrite is set,
// and refuses a destination that is not a regular file even with --overwrite
// (renaming over a directory or a device would fail or do damage).
//
// The Lstat is deliberate: a symlink at the destination is refused even with
// --overwrite, so a download never writes through one.
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
func decodeDriveResponse(raw []byte, into any) *output.ExitError {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return output.ErrWithHint("internal", "RESPONSE_DECODE",
			fmt.Sprintf("unexpected response shape: %v", err),
			"report the response the backend returned")
	}
	return nil
}
