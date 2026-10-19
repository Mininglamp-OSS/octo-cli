package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

const (
	maxPortableExportAttachments = 100
	maxPortableAttachmentBytes   = 20 << 20
)

func registerDocsExcalidrawExportCmd(root *cobra.Command, f *cmdutil.Factory) {
	export := commandAt(root, "docs", "scene", "export")
	if export == nil {
		return
	}
	originalRunE := export.RunE
	export.Long += `

Pass --image-format excalidraw with --output/-o to save a round-trippable
version-2 Excalidraw file. Referenced attachments are embedded as dataURL values.
This format reads the live scene even during --dry-run.`
	if imageFormat := export.Flags().Lookup("image-format"); imageFormat != nil {
		imageFormat.Usage = "image format (one of: png, svg, excalidraw); default png"
	}
	if output := export.Flags().Lookup("output"); output != nil {
		output.Usage = "write PNG, SVG, or portable Excalidraw output to this file path"
	}
	export.RunE = func(cmd *cobra.Command, args []string) error {
		imageFormat, err := cmd.Flags().GetString("image-format")
		if err != nil {
			return err
		}
		if imageFormat != "excalidraw" {
			return originalRunE(cmd, args)
		}
		outputPath, err := cmd.Flags().GetString("output")
		if err != nil {
			return err
		}
		if strings.TrimSpace(outputPath) == "" {
			return errors.New("--output is required when --image-format is excalidraw")
		}
		return runDocsExcalidrawExport(cmd, f, args[0], outputPath)
	}
}

func runDocsExcalidrawExport(cmd *cobra.Command, f *cmdutil.Factory, docID, outputPath string) error {
	if strings.ToLower(filepath.Ext(outputPath)) != ".excalidraw" {
		return errors.New("--output must have the .excalidraw extension")
	}
	s, err := getScene(cmd, f, docID)
	if err != nil {
		return err
	}

	// clearElementsForExport: drop tombstoned elements and force linear elements'
	// lastCommittedPoint to null; everything else passes through verbatim (unlike
	// getVisibleElements, invisibly-small elements are NOT stripped on export).
	elements := clearElementsForExport(s.Elements)

	// filterOutDeletedFiles: only keep file entries referenced by a surviving
	// (non-deleted) element via its fileId.
	referenced := referencedFileIDs(elements)
	attachments, err := validatePortableAttachmentRefs(s.Files, referenced)
	if err != nil {
		return err
	}

	if f.Globals != nil && f.Globals.DryRun {
		preview, err := json.Marshal(map[string]any{
			"dry_run":     true,
			"output":      outputPath,
			"elements":    len(elements),
			"files":       len(referenced),
			"appState":    exportedAppStateKeys(s.AppState),
			"baseVersion": s.BaseVersion,
		})
		if err != nil {
			return err
		}
		return f.EmitSuccess(preview)
	}

	appState := cleanAppStateForExport(s.AppState)
	files, err := embedPortableAttachments(cmd, f, docID, elements, appState, attachments)
	if err != nil {
		return err
	}

	data, err := marshalExcalidrawEnvelope(elements, appState, files)
	if err != nil {
		return err
	}
	// The exported file promises immediate compatibility with `docs import`,
	// whose request/file limit applies to the final JSON bytes after base64
	// expansion, not to the original attachment byte count.
	if int64(len(data)) > maxDocsImportBytes {
		return fmt.Errorf("portable export would be %d bytes and exceed the %d MiB docs import limit", len(data), maxDocsImportBytes>>20)
	}
	if err := client.WriteFileAtomic(outputPath, data, 0o600); err != nil {
		return err
	}
	result, err := json.Marshal(map[string]any{"path": outputPath, "bytes": len(data), "elements": len(elements), "files": len(files), "baseVersion": s.BaseVersion})
	if err != nil {
		return err
	}
	return f.EmitSuccess(result)
}

// excalidrawExportedAppStateKeys is the export allowlist from Excalidraw's
// APP_STATE_STORAGE_CONF (the keys whose `export` flag is true). Only these
// appState fields survive serializeAsJSON; everything else (theme, scroll, zoom,
// selection, name, exportBackground, ...) is stripped so the file stays portable.
var excalidrawExportedAppStateKeys = []string{"gridSize", "gridStep", "gridModeEnabled", "viewBackgroundColor"}

// cleanAppStateForExport keeps only the allowlisted appState keys actually present
// in the live snapshot, mirroring cleanAppStateForExport / _clearAppStateForStorage.
func cleanAppStateForExport(appState map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range excalidrawExportedAppStateKeys {
		if v, ok := appState[key]; ok {
			out[key] = v
		}
	}
	return out
}

// exportedAppStateKeys returns the allowlisted keys present in appState, sorted, for
// the dry-run preview (no values, just which fields the export would carry).
func exportedAppStateKeys(appState map[string]any) []string {
	keys := []string{}
	for _, key := range excalidrawExportedAppStateKeys {
		if _, ok := appState[key]; ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// clearElementsForExport mirrors Excalidraw clearElementsForExport: drop deleted
// elements and null out lastCommittedPoint on linear (arrow/line) elements, in the
// original z-order. Non-deleted, non-linear elements are returned unchanged.
func clearElementsForExport(elements []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(elements))
	for _, e := range elements {
		if e == nil || e["isDeleted"] == true {
			continue
		}
		if t, _ := e["type"].(string); t == "arrow" || t == "line" {
			clone := cloneMap(e)
			clone["lastCommittedPoint"] = nil
			out = append(out, clone)
			continue
		}
		out = append(out, e)
	}
	return out
}

// referencedFileIDs collects the fileId of every element that carries one, so only
// files still referenced by a surviving element are embedded (filterOutDeletedFiles).
func referencedFileIDs(elements []map[string]any) map[string]bool {
	ids := map[string]bool{}
	for _, e := range elements {
		if fileID, ok := e["fileId"].(string); ok && fileID != "" {
			ids[fileID] = true
		}
	}
	return ids
}

// marshalExcalidrawEnvelope emits the version-2 envelope with the same top-level
// key order Excalidraw's serializeAsJSON uses (type, version, source, elements,
// appState, files), 2-space indentation, and HTML escaping disabled so characters
// like < > & in element text or URLs are written literally as JSON.stringify does.
func marshalExcalidrawEnvelope(elements []map[string]any, appState, files map[string]any) ([]byte, error) {
	type envelope struct {
		Type     string           `json:"type"`
		Version  int              `json:"version"`
		Source   string           `json:"source"`
		Elements []map[string]any `json:"elements"`
		AppState map[string]any   `json:"appState"`
		Files    map[string]any   `json:"files"`
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope{
		Type:     "excalidraw",
		Version:  2,
		Source:   "octo-cli",
		Elements: elements,
		AppState: appState,
		Files:    files,
	}); err != nil {
		return nil, err
	}
	// json.Encoder appends a trailing newline, matching the exporter's own append.
	return buf.Bytes(), nil
}

type portableAttachmentRef struct {
	fileID   string
	attachID string
	raw      map[string]any
}

func validatePortableAttachmentRefs(files map[string]any, referenced map[string]bool) ([]portableAttachmentRef, error) {
	if len(referenced) > maxPortableExportAttachments {
		return nil, fmt.Errorf("portable export references %d attachments; limit is %d", len(referenced), maxPortableExportAttachments)
	}
	ids := make([]string, 0, len(referenced))
	for fileID := range referenced {
		ids = append(ids, fileID)
	}
	sort.Strings(ids)
	refs := make([]portableAttachmentRef, 0, len(ids))
	for _, fileID := range ids {
		value, exists := files[fileID]
		if !exists {
			return nil, fmt.Errorf("portable attachment %q is referenced by an element but missing from scene files", fileID)
		}
		raw, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("portable attachment %q has invalid file metadata", fileID)
		}
		attachID, ok := raw["attachId"].(string)
		if !ok || strings.TrimSpace(attachID) == "" {
			return nil, fmt.Errorf("portable attachment %q is missing attachId", fileID)
		}
		if _, err := opaquePathSegment(attachID, "attachId"); err != nil {
			return nil, fmt.Errorf("portable attachment %q: %w", fileID, err)
		}
		refs = append(refs, portableAttachmentRef{fileID: fileID, attachID: attachID, raw: raw})
	}
	return refs, nil
}

type attachmentDataURL struct {
	dataURL  string
	mimeType string
}

func embedPortableAttachments(cmd *cobra.Command, f *cmdutil.Factory, docID string, elements []map[string]any, appState map[string]any, attachments []portableAttachmentRef) (map[string]any, error) {
	files := map[string]any{}
	baseEnvelope, err := marshalExcalidrawEnvelope(elements, appState, files)
	if err != nil {
		return nil, err
	}
	projectedBytes := int64(len(baseEnvelope))
	for _, attachment := range attachments {
		resolved, resolveErr := resolveAttachmentDataURL(cmd, f, docID, attachment.attachID, maxDocsImportBytes-projectedBytes)
		if resolveErr != nil {
			return nil, fmt.Errorf("portable attachment %q: %w", attachment.fileID, resolveErr)
		}
		entry := map[string]any{"id": attachment.fileID, "dataURL": resolved.dataURL, "mimeType": resolved.mimeType, "created": 0, "lastRetrieved": 0}
		if created, ok := attachment.raw["createdAt"].(float64); ok {
			entry["created"] = created
		}
		files[attachment.fileID] = entry
		encodedID, marshalErr := json.Marshal(attachment.fileID)
		if marshalErr != nil {
			return nil, marshalErr
		}
		encodedEntry, marshalErr := json.Marshal(entry)
		if marshalErr != nil {
			return nil, marshalErr
		}
		// O(N) lower-bound estimate used only to stop obviously oversized downloads.
		// Pretty-print whitespace is intentionally omitted so this cannot reject an
		// otherwise valid file; the final envelope is checked exactly before writing.
		projectedBytes += int64(len(encodedID) + len(encodedEntry) + 1)
	}
	return files, nil
}

func resolveAttachmentDataURL(cmd *cobra.Command, f *cmdutil.Factory, docID, attachID string, remainingBytes int64) (attachmentDataURL, error) {
	payload, err := resolveAttachmentPayload(cmd, f, docID, attachID)
	if err != nil {
		return attachmentDataURL{}, err
	}
	signedURL, declaredMIME := attachmentLocation(payload)
	if signedURL == "" {
		return attachmentDataURL{}, errors.New("attachment resolve response omitted a download URL")
	}
	return downloadPortableAttachment(cmd, f, signedURL, declaredMIME, remainingBytes)
}

func resolveAttachmentPayload(cmd *cobra.Command, f *cmdutil.Factory, docID, attachID string) (map[string]any, error) {
	docSegment, err := opaquePathSegment(docID, "docId")
	if err != nil {
		return nil, err
	}
	attachSegment, err := opaquePathSegment(attachID, "attachId")
	if err != nil {
		return nil, err
	}
	cli, err := f.Client()
	if err != nil {
		return nil, err
	}
	result, err := cli.Do(cmd.Context(), &client.Request{Method: http.MethodGet, Path: "/v1/bot/docs/" + docSegment + "/attachments/" + attachSegment, SuppressSpaceHeader: true})
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func attachmentLocation(payload map[string]any) (signedURL, mime string) {
	signedURL = firstString(payload, "url", "downloadUrl", "download_url")
	mime = firstString(payload, "mime", "mimeType")
	if data, ok := payload["data"].(map[string]any); ok {
		if signedURL == "" {
			signedURL = firstString(data, "url", "downloadUrl", "download_url")
		}
		if mime == "" {
			mime = firstString(data, "mime", "mimeType")
		}
	}
	return signedURL, normalizeMIME(mime)
}

func downloadPortableAttachment(cmd *cobra.Command, f *cmdutil.Factory, signedURL, declaredMIME string, remainingBytes int64) (attachmentDataURL, error) {
	loopbackAPI := apiOriginIsLoopback(f)
	parsed, safeErr := assertSafeTransferURL("attachment download URL", signedURL, loopbackAPI)
	if safeErr != nil {
		return attachmentDataURL{}, safeErr
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, parsed.String(), http.NoBody)
	if err != nil {
		return attachmentDataURL{}, errors.New("attachment resolve returned an unusable download URL")
	}
	resp, err := transferClient("attachment download URL", loopbackAPI, nil, verboseNoter(f)).Do(req)
	if err != nil {
		return attachmentDataURL{}, transferNetworkError("attachment download", parsed, err)
	}
	body, readErr := readPortableAttachmentBody(resp, remainingBytes, declaredMIME)
	if readErr != nil {
		return attachmentDataURL{}, readErr
	}
	mime, err := verifiedPortableMIME(body, declaredMIME, resp.Header.Get("Content-Type"))
	if err != nil {
		return attachmentDataURL{}, err
	}
	return attachmentDataURL{dataURL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(body), mimeType: mime}, nil
}

func readPortableAttachmentBody(resp *http.Response, remainingBytes int64, declaredMIME string) ([]byte, error) {
	if resp.StatusCode != http.StatusOK {
		closeErr := resp.Body.Close()
		return nil, primaryPortableAttachmentError(fmt.Errorf("attachment download returned HTTP %d", resp.StatusCode), closeErr)
	}
	predictedMIME := declaredMIME
	if predictedMIME == "" {
		predictedMIME = resp.Header.Get("Content-Type")
	}
	if remainingBytes <= 0 || predictedDataURLBytes(resp.ContentLength, predictedMIME) > remainingBytes {
		closeErr := resp.Body.Close()
		return nil, primaryPortableAttachmentError(fmt.Errorf("base64-expanded attachment data exceeds the %d MiB docs import limit", maxDocsImportBytes>>20), closeErr)
	}
	readLimit := int64(maxPortableAttachmentBytes)
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, readLimit+1))
	closeErr := resp.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if err := assertCompleteBody(int64(len(body)), resp.ContentLength); err != nil {
		return nil, err
	}
	if int64(len(body)) > readLimit {
		return nil, fmt.Errorf("attachment exceeds the %d MiB portable export limit", maxPortableAttachmentBytes>>20)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close attachment download: %w", closeErr)
	}
	return body, nil
}

func primaryPortableAttachmentError(primary, closeErr error) error {
	if closeErr == nil {
		return primary
	}
	return fmt.Errorf("%w (also failed to close attachment download: %v)", primary, closeErr)
}

func predictedDataURLBytes(contentLength int64, contentType string) int64 {
	if contentLength < 0 {
		return 0
	}
	mime := normalizeMIME(contentType)
	return int64(len("data:"+mime+";base64,")) + (contentLength+2)/3*4
}

func verifiedPortableMIME(body []byte, declared, header string) (string, error) {
	if len(body) == 0 {
		return "", errors.New("attachment download returned an empty body")
	}
	declared = normalizeMIME(declared)
	if declared == "" {
		declared = normalizeMIME(header)
	}
	detected := detectPortableMIME(body)
	if !supportedPortableMIME(declared) {
		return "", fmt.Errorf("unsupported portable attachment MIME %q", declared)
	}
	if detected != declared {
		return "", fmt.Errorf("portable attachment MIME %q does not match downloaded bytes (%q)", declared, detected)
	}
	return declared, nil
}

func detectPortableMIME(body []byte) string {
	if hasSVGRoot(body) {
		return "image/svg+xml"
	}
	return normalizeMIME(http.DetectContentType(body))
}

func supportedPortableMIME(mime string) bool {
	return mime == "image/png" || mime == "image/jpeg" || mime == "image/gif" || mime == "image/svg+xml"
}

func normalizeMIME(value string) string {
	mime := strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	switch mime {
	case "image/jpg", "image/pjpeg":
		return "image/jpeg"
	case "image/x-png":
		return "image/png"
	}
	return mime
}

func hasSVGRoot(body []byte) bool {
	body = bytes.TrimPrefix(body, []byte{0xef, 0xbb, 0xbf})
	decoder := xml.NewDecoder(bytes.NewReader(body))
	// Illustrator SVGs commonly reference entities declared in the DOCTYPE
	// from attributes on the root element. encoding/xml does not populate its
	// entity map from the internal subset, so strict tokenization rejects them.
	decoder.Strict = false
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if start, ok := token.(xml.StartElement); ok {
			return start.Name.Local == "svg"
		}
	}
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}
