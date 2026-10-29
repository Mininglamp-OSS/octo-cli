package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// planUploadDryRun describes a multipart operation's planned request WITHOUT any
// filesystem or network side effect (B1): the file at file_path is never opened
// or read, its bytes never appear in the result, and the multipart body is not
// materialized. Secret-flagged form fields and parameters are masked. The
// credential and the operation's token-kind gate are resolved and the lexical
// upload policy is checked during planning; descriptor confinement and
// transport run only on a real call. The limitation is stated in the note
// rather than silently implied.
func planUploadDryRun(ctx context.Context, f *cmdutil.Factory, detail *registry.OperationDetail, args map[string]any, pol execPolicy) ([]byte, bool, *output.ExitError) {
	if err := validateMultipartDryRun(ctx, detail, args, pol); err != nil {
		ee := output.AsExitError(cmdutil.WrapCLIError(err))
		return synthErrorEnvelope(ee), false, ee
	}
	if err := service.ResolveOperationIdentity(f, detail); err != nil {
		ee := output.AsExitError(err)
		if ee == nil {
			ee = output.ErrAuth(err.Error(), "")
		}
		return synthErrorEnvelope(ee), false, ee
	}

	pathSet := map[string]bool{}
	for _, n := range extractPathParams(detail.Path) {
		pathSet[n] = true
	}
	params := map[string]*registry.ParamInfo{}
	for i := range detail.Parameters {
		if p := &detail.Parameters[i]; p.In == "query" || p.In == "header" {
			params[p.Name] = p
		}
	}
	fileField := binaryFileField(detail)
	formFields := map[string]any{}
	queryFields := map[string]any{}
	headerFields := map[string]any{}
	var filePath string
	for _, k := range sortedKeys(args) {
		if k == "file_path" {
			filePath = fmt.Sprint(args[k]) // the PATH string only; contents are never read
			continue
		}
		if pathSet[k] || k == "page_all" {
			continue
		}
		v := args[k]
		if p := params[k]; p != nil {
			if v == nil {
				continue // a null query/header argument is omitted, as on a real call
			}
			if p.Secret {
				v = "***"
			}
			if p.In == "query" {
				queryFields[k] = v
			} else {
				headerFields[k] = v
			}
			continue
		}
		if detail.RequestBody != nil {
			prop := detail.RequestBody.Properties[k]
			if registry.RequestSensitive(prop.Secret, prop.WriteOnly) {
				v = "***"
			}
		}
		formFields[k] = v
	}
	data := map[string]any{
		"dry_run": true,
		"method":  detail.Method,
		"path":    fillPathParams(detail.Path, args),
	}
	if len(queryFields) > 0 {
		data["query"] = queryFields
	}
	if len(headerFields) > 0 {
		data["headers"] = headerFields
	}
	if detail.RawBody {
		// The file is the whole body: describe its media type and limit, never
		// its bytes. Header arguments (e.g. X-Media-Type) are listed as such.
		data["raw_body"] = true
		data["content_type"] = detail.RawBodyContentType
		if detail.RawBodyMaxBytes > 0 {
			data["max_bytes"] = detail.RawBodyMaxBytes
		}
		if filePath != "" {
			data["file_path"] = filePath
		}
		note := "raw-body dry-run: metadata-only. The credential and token-kind gate were resolved, and required path/query/header arguments and the file binding were validated; file_path is NOT opened or read and its bytes are never returned; nothing is sent. Descriptor-based upload-root containment, the size limit, and transport are enforced only on a real (non-dry-run) call."
		data["dry_run_note"] = note
		return marshalUploadPlan(data)
	}
	data["multipart"] = true
	data["form_fields"] = formFields
	if filePath != "" {
		data["file_field"] = fileField
		data["file_path"] = filePath
	}
	note := "multipart dry-run: metadata-only. The credential and token-kind gate were resolved, and required path/query/header/body fields and the multipart file binding were validated; file_path is NOT opened or read and its bytes are never returned; the multipart body is not materialized and nothing is sent. Descriptor-based upload-root containment and transport are enforced only on a real (non-dry-run) call."
	data["dry_run_note"] = note
	return marshalUploadPlan(data)
}

func marshalUploadPlan(data map[string]any) (envelope []byte, ok bool, exitErr *output.ExitError) {
	raw, err := json.Marshal(map[string]any{"ok": true, "data": data})
	if err != nil {
		ee := output.ErrValidation("encode upload plan: "+err.Error(), "")
		return synthErrorEnvelope(ee), false, ee
	}
	return raw, true, nil
}
