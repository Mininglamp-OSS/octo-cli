package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"

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
}

func addShareCreateFlags(cmd *cobra.Command, o *shareCreateOpts) {
	cmd.Flags().StringVar(&o.permission, "permission", "download", "what the receiver may do: view or download (blob shares only)")
	cmd.Flags().IntVar(&o.expiresInSeconds, "expires-in-seconds", 0, "share lifetime; 0 uses the backend default of 7 days (blob shares only)")
	cmd.Flags().StringVar(&o.password, "password", "", "optional share password; hand it over out of band, never inside the URL (blob shares only)")
}

func (o shareCreateOpts) body(fileID uint64) map[string]any {
	body := map[string]any{
		"file_id":            output.Uint64JSONNumber(fileID),
		"permission":         o.permission,
		"expires_in_seconds": o.expiresInSeconds,
	}
	if o.password != "" {
		body["password"] = o.password
	}
	return body
}

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

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":    true,
			"operations": []string{"drive.file.get", "drive.share.blob-create"},
			"lookup":     map[string]any{"method": http.MethodGet, "path": "{mount}/files/" + fileID},
			"blob_share": map[string]any{"method": http.MethodPost, "path": "{mount}/shares", "body": redactedShareBody(o, id)},
			"note":       "dry run stops here: the node is not looked up and no share is created",
		})
	}

	mount, err := service.MountForOperation(f, "drive.file.get")
	if err != nil {
		return failErr(f, err)
	}
	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodGet,
		Path:                mount + "/files/" + fileID,
		SuppressSpaceHeader: true,
	})
	if err != nil {
		return failErr(f, err)
	}
	var entry driveEntryResponse
	if derr := decodeLossless(raw, &entry); derr != nil {
		return failErr(f, derr)
	}

	switch entry.Type {
	case shareKindDoc:
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

	case shareKindBlob:
		share, serr := createBlobShare(cmd, f, cli, id, o)
		if serr != nil {
			return failErr(f, serr)
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
	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":   true,
			"method":    http.MethodPost,
			"operation": "drive.share.blob-create",
			"path":      "{mount}/shares",
			"body":      redactedShareBody(o, id),
		})
	}
	cli, err := f.Client()
	if err != nil {
		return failErr(f, err)
	}
	share, serr := createBlobShare(cmd, f, cli, id, o)
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

// createBlobShare posts the share record. The password is marked as a secret so
// it is masked in verbose traces.
func createBlobShare(cmd *cobra.Command, f *cmdutil.Factory, cli *client.Client, fileID uint64, o shareCreateOpts) (*shareResponse, error) {
	mount, err := service.MountForOperation(f, "drive.share.blob-create")
	if err != nil {
		return nil, err
	}
	raw, err := cli.Do(cmd.Context(), &client.Request{
		Method:              http.MethodPost,
		Path:                mount + "/shares",
		Body:                o.body(fileID),
		SuppressSpaceHeader: true,
		SecretValues:        secretList(o.password),
	})
	if err != nil {
		return nil, err
	}
	var share shareResponse
	if derr := decodeLossless(raw, &share); derr != nil {
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
	var password string
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
			return runDriveShareAccess(cmd, f, args[0], password)
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "share password, for a password-protected blob share")
	return cmd
}

func runDriveShareAccess(cmd *cobra.Command, f *cmdutil.Factory, shareURL, password string) error {
	cfg, err := f.Config()
	if err != nil {
		return failErr(f, err)
	}
	parsed, perr := parseShareURL(cfg, shareURL)
	if perr != nil {
		return failErr(f, perr)
	}

	// A document link carries its own target; there is nothing to ask the
	// backend, and the link conveys no permission of its own.
	if parsed.kind == shareKindDoc {
		return emitJSON(f, shareTarget{
			Kind:         shareKindDoc,
			ShareURL:     parsed.canonical,
			Downloadable: false,
			DocID:        parsed.docID,
			DocSpaceID:   parsed.docSpaceID,
			Permission:   docPermission,
		})
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":      true,
			"method":       http.MethodPost,
			"operation":    "drive.share.access",
			"path":         "{mount}/shares/***REDACTED***/access",
			"share_url":    parsed.canonical,
			"password_set": password != "",
		})
	}

	mount, err := service.MountForOperation(f, "drive.share.access")
	if err != nil {
		return failErr(f, err)
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
	if derr := decodeLossless(raw, &access); derr != nil {
		return failErr(f, derr)
	}
	return emitJSON(f, shareTarget{
		Kind:         shareKindBlob,
		ShareURL:     parsed.canonical,
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
	var outputPath, password string
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

The bytes are written to "<output>.part" and renamed into place only after a
complete transfer, on an HTTP client that carries no Octo credential. An existing
destination is refused unless --overwrite is set.

Underlying operation: drive.share.download.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDriveShareDownload(cmd, f, args[0], outputPath, password, overwrite)
		},
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "destination file path (required)")
	cmd.Flags().StringVar(&password, "password", "", "share password, for a password-protected blob share")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace the destination file if it already exists")
	_ = cmd.MarkFlagRequired("output") //nolint:errcheck // static flag name
	return cmd
}

func runDriveShareDownload(cmd *cobra.Command, f *cmdutil.Factory, shareURL, outputPath, password string, overwrite bool) error {
	cfg, err := f.Config()
	if err != nil {
		return failErr(f, err)
	}
	parsed, perr := parseShareURL(cfg, shareURL)
	if perr != nil {
		return failErr(f, perr)
	}
	if parsed.kind == shareKindDoc {
		return failErr(f, output.ErrWithHint("validation", "NOT_DOWNLOADABLE",
			"an online document has no downloadable bytes",
			"use `octo-cli drive share access` to resolve the target, or open the link in a browser"))
	}
	if outputPath == "" {
		return failErr(f, output.ErrValidation("--output is required", "pass -o with a destination file path"))
	}
	if werr := assertWritableTarget(outputPath, overwrite); werr != nil {
		return failErr(f, werr)
	}

	if f.Globals != nil && f.Globals.DryRun {
		return emitJSON(f, map[string]any{
			"dry_run":      true,
			"method":       http.MethodPost,
			"operation":    "drive.share.download",
			"path":         "{mount}/shares/***REDACTED***/download",
			"share_url":    parsed.canonical,
			"output":       outputPath,
			"overwrite":    overwrite,
			"password_set": password != "",
			"note":         "dry run stops here: no URL is fetched and nothing is written to disk",
		})
	}

	mount, err := service.MountForOperation(f, "drive.share.download")
	if err != nil {
		return failErr(f, err)
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
	if derr := decodeLossless(raw, &signed); derr != nil {
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
	result.ShareURL = parsed.canonical
	return emitJSON(f, result)
}

// --- shared helpers ---

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

// redactedShareBody renders the share-create body for --dry-run with the
// password replaced by a boolean, so a dry run is safe to paste anywhere.
func redactedShareBody(o shareCreateOpts, fileID uint64) map[string]any {
	body := o.body(fileID)
	if _, ok := body["password"]; ok {
		delete(body, "password")
		body["password_set"] = true
	}
	return body
}
