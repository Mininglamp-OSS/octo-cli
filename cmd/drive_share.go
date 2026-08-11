package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// docPermission is what a document link conveys: nothing of its own. Opening it
// still requires an Octo login and an existing docs permission on that document.
const docPermission = "docs-own-permission"

// shareTarget is the single shape both sides of a share see. kind, share_url and
// downloadable are always present; the rest depend on the kind.
type shareTarget struct {
	Kind         string `json:"kind"`
	ShareURL     string `json:"share_url"`
	Downloadable bool   `json:"downloadable"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	ShareID      string `json:"share_id,omitempty"`
	DocID        string `json:"doc_id,omitempty"`
	DocSpaceID   string `json:"doc_space_id,omitempty"`
	Filename     string `json:"filename,omitempty"`
	Size         string `json:"size,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	Permission   string `json:"permission"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// driveEntryResponse is the subset of a DriveEntry the share commands branch on.
type driveEntryResponse struct {
	ID          json.Number `json:"id"`
	Type        string      `json:"type"`
	RefID       string      `json:"ref_id"`
	DocSpaceID  string      `json:"doc_space_id"`
	Name        string      `json:"name"`
	Size        json.Number `json:"size"`
	ContentType string      `json:"content_type"`
}

// shareResponse is the backend's raw share DTO: one opaque `id` doubling as the
// management handle and the access token.
type shareResponse struct {
	ID          string      `json:"id"`
	FileID      json.Number `json:"file_id"`
	Permission  string      `json:"permission"`
	ExpiresAt   string      `json:"expires_at"`
	PasswordSet bool        `json:"password_set"`
	CreatorUID  string      `json:"creator_uid"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
}

// shareAccessResponse is the authenticated access reply for a blob share.
type shareAccessResponse struct {
	Permission  string      `json:"permission"`
	ExpiresAt   string      `json:"expires_at"`
	FileID      json.Number `json:"file_id"`
	FileName    string      `json:"file_name"`
	FileSize    json.Number `json:"file_size"`
	ContentType string      `json:"content_type"`
}

type shareCreateOpts struct {
	permission       string
	expiresInSeconds int
	password         string
	passwordFile     string
}

func addShareCreateFlags(cmd *cobra.Command, o *shareCreateOpts) {
	cmd.Flags().StringVar(&o.permission, "permission", "download", "what the receiver may do: view or download (blob shares only)")
	cmd.Flags().IntVar(&o.expiresInSeconds, "expires-in-seconds", 0, "share lifetime; 0 uses the backend default of 7 days (blob shares only)")
	cmd.Flags().StringVar(&o.password, "password", "", "optional share password; hand it over out of band, never inside the URL (blob shares only). Visible in ps/argv — prefer --password-file")
	addSharePasswordFileFlag(cmd, &o.passwordFile)
	cmd.MarkFlagsMutuallyExclusive("password", "password-file")
}

// addSharePasswordFileFlag registers the non-argv route for a share password,
// mirroring what `auth login` already offers for a bot token (--token-file /
// --with-token, "never from the command line"). A share password is what gates
// access to shared bytes, and on argv it is readable from ps and /proc and lands
// in shell history for the process lifetime. --password stays supported for
// interactive use and backwards compatibility.
func addSharePasswordFileFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVar(target, "password-file", "",
		"read the share password from this file, or from stdin when the path is \"-\"; keeps it off argv")
}

// resolveSharePassword returns the effective password: the --password value, or
// the contents of --password-file with the trailing line terminator removed.
//
// Only the final line terminator is stripped — one "\n" and the "\r" that may
// precede it — not all surrounding whitespace as readToken does for a bot token:
// a token's charset excludes spaces, while a password may legitimately start or
// end with one, and trimming it would turn a correct password into a silent
// authentication failure.
func resolveSharePassword(f *cmdutil.Factory, password, passwordFile string) (string, *output.ExitError) {
	if passwordFile == "" {
		return password, nil
	}
	var raw []byte
	var err error
	if passwordFile == "-" {
		raw, err = io.ReadAll(f.IOStreams.In)
	} else {
		raw, err = os.ReadFile(passwordFile)
	}
	if err != nil {
		return "", output.ErrValidation(
			fmt.Sprintf("--password-file: %v", err),
			"check the path, or pass \"-\" to read the password from stdin")
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r"), nil
}

func (o shareCreateOpts) body(fileID uint64, password string) map[string]any {
	body := map[string]any{
		"file_id":            output.Uint64JSONNumber(fileID),
		"permission":         o.permission,
		"expires_in_seconds": o.expiresInSeconds,
	}
	if password != "" {
		body["password"] = password
	}
	return body
}

// shareCreateFlagLabels maps the share-create body fields back to the flags that
// set them, so a spec-enum rejection names --permission rather than the JSON key.
var shareCreateFlagLabels = map[string]string{
	"permission":         "permission",
	"expires_in_seconds": "expires-in-seconds",
	"password":           "password",
}

// prepareBlobShare runs everything that must happen before a share request is
// described or sent: the password is loaded, the body is checked against the
// drive.share.blob-create schema (so --permission is held to the spec's enum
// with zero HTTP, exactly as the generated leaf this command replaced was), and
// the credential is resolved and gated for that operation.
//
// It returns the assembled body and the resolved mount.
func prepareBlobShare(f *cmdutil.Factory, o shareCreateOpts, fileID uint64) (body map[string]any, mount string, err error) {
	password, perr := resolveSharePassword(f, o.password, o.passwordFile)
	if perr != nil {
		return nil, "", perr
	}
	body = o.body(fileID, password)
	if verr := service.ValidateRequestBody(f, driveShareCreateOp, body, shareCreateFlagLabels); verr != nil {
		return nil, "", verr
	}
	mount, err = service.MountForOperation(f, driveShareCreateOp)
	if err != nil {
		return nil, "", err
	}
	return body, mount, nil
}

// driveShareCreateOp is the spec operation both share-create surfaces post to.
const driveShareCreateOp = "drive.share.blob-create"

// --- drive share create ---

// newDriveShareCreateCmd builds `octo-cli drive share create <file-id>`.
func newDriveShareCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var o shareCreateOpts
	cmd := &cobra.Command{
		Use:   "create <file-id>",
		Short: "Create a share link for any shareable drive node",
		Long: `Create the one thing both sides of a share need: a share_url.

Looks the node up first, then branches on its type:

  blob — creates a share token and returns ` + "`/drive/s/<token>`" + `. downloadable
         reflects --permission.
  doc  — returns ` + "`/d/<docId>?sp=<docSpaceId>`" + `, built from the document's own
         Octo Space. downloadable is always false: a document link is an entrance,
         not a grant — the receiver still needs an Octo login and an existing docs
         permission. If the mount predates drive capturing doc_space_id the
         command fails closed rather than substituting the drive space id, which
         is a different scope and would produce a link to the wrong place.

Hand the receiver data.share_url verbatim; they never need the token. Pass the
same link to ` + "`drive share access`" + ` / ` + "`drive share download`" + `.

Underlying operations: drive.file.get, drive.share.blob-create.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveShareCreate(cmd, f, args[0], o)
		},
	}
	addShareCreateFlags(cmd, &o)
	return cmd
}

func runDriveShareCreate(cmd *cobra.Command, f *cmdutil.Factory, fileID string, o shareCreateOpts) error {
	id, perr := output.ParseUint64Decimal("<file-id>", fileID)
	if perr != nil {
		return failErr(f, perr)
	}
	cfg, err := f.Config()
	if err != nil {
		return failErr(f, err)
	}
	origin, oerr := webOrigin(cfg)
	if oerr != nil {
		return failErr(f, oerr)
	}
	// Spec-enum check on --permission and the credential gate both run here,
	// before the dry-run branch and before the node lookup, so an out-of-enum
	// value or an incompatible credential costs zero HTTP on either branch.
	shareBody, shareMount, err := prepareBlobShare(f, o, id)
	if err != nil {
		return failErr(f, err)
	}
	lookupMount, err := service.MountForOperation(f, "drive.file.get")
	if err != nil {
		return failErr(f, err)
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":    true,
			"operations": []string{"drive.file.get", driveShareCreateOp},
			"lookup":     map[string]any{"method": http.MethodGet, "path": lookupMount + "/files/" + fileID},
			"blob_share": map[string]any{"method": http.MethodPost, "path": shareMount + "/shares", "body": redactedShareBody(shareBody)},
			"note":       "dry run stops here: the node is not looked up and no share is created",
		})
	}

	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodGet,
		Path:                lookupMount + "/files/" + fileID,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return failErr(f, err)
	}
	var entry driveEntryResponse
	if derr := decodeDriveResponse(raw, &entry); derr != nil {
		return failErr(f, derr)
	}

	switch entry.Type {
	case shareKindDoc:
		return emitDocShareBranch(cmd, f, origin, &entry)

	case shareKindBlob:
		share, serr := createBlobShare(cmd, f, cli, shareMount, shareBody)
		if serr != nil {
			return failErr(f, serr)
		}
		// The share id is backend data going straight into the link's path, the
		// same situation as the document branch's ref_id: hold it to the charset
		// the consuming side enforces so this command cannot emit a share_url that
		// `share access` would then refuse.
		if verr := assertShareIDSegment("share token", share.ID); verr != nil {
			return failErr(f, verr)
		}
		return emitJSON(f, shareTarget{
			Kind:         shareKindBlob,
			ShareURL:     buildBlobShareURL(origin, share.ID),
			Downloadable: share.Permission == "download",
			DriveFileID:  share.FileID.String(),
			ShareID:      share.ID,
			Filename:     entry.Name,
			Size:         entry.Size.String(),
			ContentType:  entry.ContentType,
			Permission:   share.Permission,
			ExpiresAt:    share.ExpiresAt,
		})

	default:
		return failErr(f, output.ErrWithHint("validation", "NOT_SHAREABLE",
			fmt.Sprintf("a node of type %q cannot be shared", entry.Type),
			"share an individual file or a mounted document, not a folder"))
	}
}

// emitDocShareBranch is the document arm of `share create`: refuse the flags a document
// cannot carry, then emit the link.
//
// Refuse rather than drop. prepareBlobShare has already loaded the password and built the
// whole share body by this point, and this arm makes no request at all — so a supplied
// --password / --password-file / --expires-in-seconds was silently discarded, and
// `share create <doc-mount-id> --password-file pw` exited 0 with a share_url the operator then
// handed over believing it was password-gated. It was not, and nothing in the envelope said so.
//
// The Long help does say "(blob shares only)", but help text is not a runtime control: once
// the caller has actually passed the flag, the choices are refuse or warn, and the
// MISSING_DOC_SPACE_ID path below already sets the precedent by failing closed rather than
// substituting. Silently downgrading a security property is the defect class this command has
// spent every round closing.
//
// Extracted from runDriveShareCreate to keep it under the complexity limit.
func emitDocShareBranch(cmd *cobra.Command, f *cmdutil.Factory, origin *url.URL, entry *driveEntryResponse) error {
	if verr := assertNoBlobOnlyFlags(cmd); verr != nil {
		return failErr(f, verr)
	}
	return emitDocShareTarget(f, origin, entry)
}

// blobOnlyShareFlags are the share-create flags only a blob share can carry. A document link
// is an entrance into the docs permission system, not a grant this command parameterises, so
// there is nowhere for these to be applied.
var blobOnlyShareFlags = []string{"password", "password-file", "expires-in-seconds"}

// assertNoBlobOnlyFlags refuses a document share that was given a blob-only flag.
//
// The decision is on Changed, not on the value: `--expires-in-seconds 0` means "use the
// backend default" and is a different statement from not passing the flag at all, so a
// zero-value test would wave through a caller who said something explicit. --permission is
// deliberately not on the list: it has a non-zero default so every invocation would trip it,
// and the Long help already states that a document link is never downloadable.
//
// The flag names are reported, never their values — a refusal that exists to protect a
// password must not echo it.
func assertNoBlobOnlyFlags(cmd *cobra.Command) *output.ExitError {
	var set []string
	for _, name := range blobOnlyShareFlags {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			set = append(set, "--"+name)
		}
	}
	if len(set) == 0 {
		return nil
	}
	return output.ErrWithHint("validation", "SHARE_FLAG_NOT_APPLICABLE",
		fmt.Sprintf("this node is an online document, so %s cannot be applied", strings.Join(set, ", ")),
		"a document link is an entrance, not a grant: access is decided by the docs permission "+
			"system, not by a share password or an expiry. Re-run without those flags to get the "+
			"document link, or share an uploaded file if you need a password-protected, expiring share")
}

// emitDocShareTarget renders the document branch of `share create`. A document
// link is an entrance, not a grant, so it carries no share token and no
// permission of its own — but it must be built from the document's own Octo
// Space and must be a link this CLI can parse back.
func emitDocShareTarget(f *cmdutil.Factory, origin *url.URL, entry *driveEntryResponse) error {
	if entry.DocSpaceID == "" {
		return failErr(f, output.ErrWithHint("validation", "MISSING_DOC_SPACE_ID",
			"this document mount has no doc_space_id, so a correct document link cannot be built",
			"re-mount the document with `octo-cli drive doc mount`; the drive space id is NOT a valid substitute"))
	}
	docID := entry.RefID
	if docID == "" {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"the document mount returned no ref_id (doc id)", "report the backend response"))
	}
	// The doc id comes from the backend, and buildDocShareURL concatenates it
	// into the path. Hold it to the same charset the consuming side enforces
	// (assertShareIDSegment, used by `share access`) so this command cannot hand
	// out a link its own parser would refuse — a `?`, `#`, space or slash in a
	// ref_id would otherwise produce a structurally different URL.
	if verr := assertShareIDSegment("document id", docID); verr != nil {
		return failErr(f, verr)
	}
	return emitJSON(f, shareTarget{
		Kind:         shareKindDoc,
		ShareURL:     buildDocShareURL(origin, docID, entry.DocSpaceID),
		Downloadable: false,
		DriveFileID:  entry.ID.String(),
		DocID:        docID,
		DocSpaceID:   entry.DocSpaceID,
		Filename:     entry.Name,
		Permission:   docPermission,
	})
}

// --- drive share blob-create ---

// newDriveShareBlobCreateCmd builds `octo-cli drive share blob-create <file-id>`.
// It is hand-written only so the file id can be positional (the backend takes it
// in the body, which the metadata engine would surface as a flag); the request,
// the mount and the output shape all come from the drive.share.blob-create spec.
func newDriveShareBlobCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var o shareCreateOpts
	cmd := &cobra.Command{
		Use:   "blob-create <file-id>",
		Short: "Create a blob share token directly (low-level)",
		Long: `Create a blob share token by calling the share API directly.

Prefer ` + "`drive share create`" + `, which also handles documents and returns a
ready-to-hand-over share_url. Use this when automation needs the raw record —
managing passwords, expiry, or revoking by share_id.

data.share_id is the management handle (pass it to ` + "`drive share revoke`" + `) and
data.share_token is what a share URL embeds. They hold the same value today; do
not rely on that.

Underlying operation: drive.share.blob-create.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveShareBlobCreate(cmd, f, args[0], o)
		},
	}
	addShareCreateFlags(cmd, &o)
	return cmd
}

func runDriveShareBlobCreate(cmd *cobra.Command, f *cmdutil.Factory, fileID string, o shareCreateOpts) error {
	id, perr := output.ParseUint64Decimal("<file-id>", fileID)
	if perr != nil {
		return failErr(f, perr)
	}
	// Spec-enum check and credential gate before the dry-run branch: this command
	// replaced the generated drive.share.blob-create leaf, so it has to enforce
	// the same contract the leaf did rather than a weaker hand-written one.
	body, mount, err := prepareBlobShare(f, o, id)
	if err != nil {
		return failErr(f, err)
	}
	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":   true,
			"method":    http.MethodPost,
			"operation": driveShareCreateOp,
			"path":      mount + "/shares",
			"body":      redactedShareBody(body),
		})
	}
	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	share, serr := createBlobShare(cmd, f, cli, mount, body)
	if serr != nil {
		return failErr(f, serr)
	}
	return emitJSON(f, map[string]any{
		"share_id":      share.ID,
		"share_token":   share.ID,
		"drive_file_id": share.FileID.String(),
		"creator_uid":   share.CreatorUID,
		"permission":    share.Permission,
		"expires_at":    share.ExpiresAt,
		"password_set":  share.PasswordSet,
		"created_at":    share.CreatedAt,
		"updated_at":    share.UpdatedAt,
	})
}

// createBlobShare posts the share record against an already-gated mount with an
// already-validated body. The password is marked as a secret so it is masked in
// verbose traces.
func createBlobShare(cmd *cobra.Command, f *cmdutil.Factory, cli *client.Client, mount string, body map[string]any) (*shareResponse, error) {
	password, _ := body["password"].(string)
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/shares",
		Body:                body,
		SuppressSpaceHeader: true,
		SecretValues:        secretList(password),
	})
	if err != nil {
		return nil, err
	}
	var share shareResponse
	if derr := decodeDriveResponse(raw, &share); derr != nil {
		return nil, derr
	}
	if share.ID == "" {
		return nil, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"the share endpoint returned no id", "report the backend response")
	}
	return &share, nil
}

// --- drive share access ---

// newDriveShareAccessCmd builds `octo-cli drive share access <share-url>`.
func newDriveShareAccessCmd(f *cmdutil.Factory) *cobra.Command {
	var o sharePasswordOpts
	cmd := &cobra.Command{
		Use:   "access <share-url>",
		Short: "Resolve a share link to its target (requires a credential)",
		Long: `Resolve a share link that someone handed you.

Takes the whole share_url — no token extraction. The link is parsed locally and
only ever compared against the configured Octo origin; the CLI never fetches the
link's host, it calls the configured Octo API with the token it parsed out.
Links on any other host, with embedded credentials, or with a percent-encoded
path are refused.

A credential is always required. There is no anonymous share surface: the token
(and password, if any) authorise the share, while the credential authenticates
you as an enterprise identity. You do NOT have to be a member of the file's drive
space.

A document link resolves locally to its target and grants nothing — opening it
still needs an Octo login and an existing docs permission.

Underlying operation: drive.share.access.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveShareAccess(cmd, f, args[0], o)
		},
	}
	addSharePasswordFlags(cmd, &o)
	return cmd
}

func runDriveShareAccess(cmd *cobra.Command, f *cmdutil.Factory, shareURL string, o sharePasswordOpts) error {
	cfg, err := f.Config()
	if err != nil {
		return failErr(f, err)
	}
	parsed, perr := parseShareURL(cfg, shareURL)
	if perr != nil {
		return failErr(f, perr)
	}
	password, perr := resolveSharePassword(f, o.password, o.passwordFile)
	if perr != nil {
		return failErr(f, perr)
	}
	// The credential gate runs before the document branch below, which succeeds
	// locally: without this, `share access <doc-link>` returned a resolved target
	// on a credential kind that is not allowed to touch drive at all.
	mount, err := service.MountForOperation(f, "drive.share.access")
	if err != nil {
		return failErr(f, err)
	}

	// A document link carries its own target; there is nothing to ask the
	// backend, and the link conveys no permission of its own. Under --dry-run it
	// still has to say so: every other composite marks its dry run, and a scripted
	// caller distinguishes the two by inspecting the envelope, not by remembering
	// which flags it passed.
	if parsed.kind == shareKindDoc {
		if f.Globals != nil && f.Globals.DryRun {
			return emitJSON(f, map[string]any{
				"dry_run":      true,
				"operation":    "drive.share.access",
				"kind":         shareKindDoc,
				"share_url":    redactedShareURL(parsed),
				"doc_id":       parsed.docID,
				"doc_space_id": parsed.docSpaceID,
				"downloadable": false,
				"permission":   docPermission,
				"note":         "a document link resolves locally; no request is made even without --dry-run",
			})
		}
		return emitJSON(f, shareTarget{
			Kind:         shareKindDoc,
			ShareURL:     redactedShareURL(parsed),
			Downloadable: false,
			DocID:        parsed.docID,
			DocSpaceID:   parsed.docSpaceID,
			Permission:   docPermission,
		})
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":   true,
			"method":    http.MethodPost,
			"operation": "drive.share.access",
			// Both the path and the link are masked. Masking one and printing the
			// other made the envelope contradict itself, and this output is meant to
			// be safe to paste into a ticket.
			"path":         mount + "/shares/" + shareURLMask + "/access",
			"share_url":    redactedShareURL(parsed),
			"password_set": password != "",
		})
	}

	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/shares/" + parsed.token + "/access",
		Body:                passwordBody(password),
		SuppressSpaceHeader: true,
		SecretValues:        secretList(parsed.token, password),
	})
	if err != nil {
		return failErr(f, err)
	}
	var access shareAccessResponse
	if derr := decodeDriveResponse(raw, &access); derr != nil {
		return failErr(f, derr)
	}
	return emitJSON(f, shareTarget{
		Kind:         shareKindBlob,
		ShareURL:     redactedShareURL(parsed),
		Downloadable: access.Permission == "download",
		DriveFileID:  access.FileID.String(),
		Filename:     access.FileName,
		Size:         access.FileSize.String(),
		ContentType:  access.ContentType,
		Permission:   access.Permission,
		ExpiresAt:    access.ExpiresAt,
	})
}

// --- drive share download ---

// newDriveShareDownloadCmd builds `octo-cli drive share download <share-url>`.
func newDriveShareDownloadCmd(f *cmdutil.Factory) *cobra.Command {
	var outputPath string
	var o sharePasswordOpts
	var overwrite bool
	cmd := &cobra.Command{
		Use:   "download <share-url>",
		Short: "Download the file behind a share link (requires a credential)",
		Long: `Download the file behind a share link someone handed you.

Takes the whole share_url, parsed under the same same-origin rules as
` + "`drive share access`" + `. A credential is always required.

Only a blob share is downloadable. A document link fails locally with
NOT_DOWNLOADABLE before any request goes out — open it with ` + "`drive share access`" + `
or in a browser instead.

The bytes are written to a randomly-named partial file next to the destination and
renamed into place only after a complete transfer, on an HTTP client that carries
no Octo credential. An existing destination is refused unless --overwrite is set.

Underlying operation: drive.share.download.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveShareDownload(cmd, f, args[0], outputPath, o, overwrite)
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "destination file path (required)")
	addSharePasswordFlags(cmd, &o)
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace the destination file if it already exists")
	_ = cmd.MarkFlagRequired("output") //nolint:errcheck // static flag name
	return cmd
}

func runDriveShareDownload(cmd *cobra.Command, f *cmdutil.Factory, shareURL, outputPath string, o sharePasswordOpts, overwrite bool) error {
	parsed, password, perr := prepareShareDownload(f, shareURL, outputPath, o, overwrite)
	if perr != nil {
		return failErr(f, perr)
	}
	// Credential resolution and the allowed-token-kinds gate precede the dry-run
	// description, so --dry-run cannot describe a request the active credential
	// is not allowed to make.
	mount, err := service.MountForOperation(f, "drive.share.download")
	if err != nil {
		return failErr(f, err)
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":      true,
			"method":       http.MethodPost,
			"operation":    "drive.share.download",
			"path":         mount + "/shares/" + shareURLMask + "/download",
			"share_url":    redactedShareURL(parsed),
			"output":       outputPath,
			"overwrite":    overwrite,
			"password_set": password != "",
			"note":         "dry run stops here: no URL is fetched and nothing is written to disk",
		})
	}

	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/shares/" + parsed.token + "/download",
		Body:                passwordBody(password),
		SuppressSpaceHeader: true,
		SecretValues:        secretList(parsed.token, password),
	})
	if err != nil {
		return failErr(f, err)
	}
	var signed downloadURLResponse
	if derr := decodeDriveResponse(raw, &signed); derr != nil {
		return failErr(f, derr)
	}
	if signed.URL == "" {
		return failErr(f, output.ErrWithHint("internal", "RESPONSE_DECODE",
			"the share download endpoint returned no url", "report the backend response"))
	}
	result, ferr := fetchToFile(cmd, f, "url", signed.URL, outputPath, overwrite)
	if ferr != nil {
		return failErr(f, ferr)
	}
	if signed.Filename != "" {
		result.Filename = signed.Filename
	}
	result.ShareURL = redactedShareURL(parsed)
	return emitJSON(f, result)
}

// --- shared helpers ---

// prepareShareDownload runs everything `share download` can decide without a
// credential: the link is parsed and required to be a blob share, the
// destination is checked, and the password is loaded. Each of these is a property
// of the invocation itself, so reporting them precisely is more useful than
// failing on the credential first.
func prepareShareDownload(f *cmdutil.Factory, shareURL, outputPath string, o sharePasswordOpts, overwrite bool) (*parsedShareURL, string, *output.ExitError) {
	cfg, err := f.Config()
	if err != nil {
		if ee := output.AsExitError(err); ee != nil {
			return nil, "", ee
		}
		return nil, "", output.ErrValidation(err.Error(), "")
	}
	parsed, perr := parseShareURL(cfg, shareURL)
	if perr != nil {
		return nil, "", perr
	}
	if parsed.kind == shareKindDoc {
		return nil, "", output.ErrWithHint("validation", "NOT_DOWNLOADABLE",
			"an online document has no downloadable bytes",
			"use `octo-cli drive share access` to resolve the target, or open the link in a browser")
	}
	if outputPath == "" {
		return nil, "", output.ErrValidation("--output is required", "pass -o with a destination file path")
	}
	if werr := assertWritableTarget(outputPath, overwrite); werr != nil {
		return nil, "", werr
	}
	password, perr := resolveSharePassword(f, o.password, o.passwordFile)
	if perr != nil {
		return nil, "", perr
	}
	return parsed, password, nil
}

// sharePasswordOpts is the password surface of the two commands that consume a
// share link. Both accept the value on argv (--password) or off it
// (--password-file), the latter mirroring `auth login --token-file`.
type sharePasswordOpts struct {
	password     string
	passwordFile string
}

func addSharePasswordFlags(cmd *cobra.Command, o *sharePasswordOpts) {
	cmd.Flags().StringVar(&o.password, "password", "", "share password, for a password-protected blob share. Visible in ps/argv — prefer --password-file")
	addSharePasswordFileFlag(cmd, &o.passwordFile)
	cmd.MarkFlagsMutuallyExclusive("password", "password-file")
}

// passwordBody returns the optional password body, or nil so no body is sent
// when there is no password (the backend treats an empty body as "no password").
func passwordBody(password string) any {
	if password == "" {
		return nil
	}
	return map[string]any{"password": password}
}

// secretList collects the non-empty values that must be masked in verbose and
// dry-run output.
func secretList(values ...string) []string {
	var out []string
	for _, v := range values {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// redactedShareBody renders a share-create body for --dry-run with the password
// replaced by a boolean, so a dry run is safe to paste anywhere. The input map is
// left untouched — it is the body that goes on the wire.
func redactedShareBody(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for k, v := range body {
		out[k] = v
	}
	if _, ok := out["password"]; ok {
		delete(out, "password")
		out["password_set"] = true
	}
	return out
}
