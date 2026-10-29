package service

import (
	"sort"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Per-row fixtures for the drive operations added for XIN-2424, verified
// against octo-drive@aad189f (bot mount /v1/bot/drive). Each row pins the
// method, mount-neutral path, required parameters and body fields, risk,
// secret/writeOnly marking and binary/multipart flags.

type xin2424DriveParam struct {
	name     string
	in       string
	required bool
	secret   bool
	flag     string
}

type xin2424DriveRow struct {
	row          string
	id           string
	method       string
	path         string
	risk         string
	params       []xin2424DriveParam
	bodyRequired []string // nil = no request body declared
	bodyNeeded   bool
	writeOnly    []string
	lossless     []string
}

func xin2424DriveRows() []xin2424DriveRow {
	fileID := xin2424DriveParam{name: "file_id", in: "path", required: true}
	shareToken := xin2424DriveParam{name: "share_token", in: "path", required: true, secret: true, flag: "share-token"}
	return []xin2424DriveRow{
		{row: "drive#57", id: "drive.upload.parts_presign", method: "POST", path: "/v1/bot/drive/files/{file_id}/upload/parts-presign", risk: "write",
			params: []xin2424DriveParam{fileID}, bodyRequired: []string{"part_numbers", "upload_id"}, bodyNeeded: true},
		{row: "drive#59", id: "drive.upload.complete", method: "POST", path: "/v1/bot/drive/files/{file_id}/upload/complete", risk: "write",
			params: []xin2424DriveParam{fileID}, bodyRequired: []string{"parts", "upload_id"}, bodyNeeded: true,
			lossless: []string{"file.id", "file.parent_id"}},
		{row: "drive#61", id: "drive.upload.abort", method: "POST", path: "/v1/bot/drive/files/{file_id}/upload/abort", risk: "write",
			params: []xin2424DriveParam{fileID}, bodyRequired: []string{"upload_id"}, bodyNeeded: true},
		{row: "drive#93", id: "drive.hello", method: "GET", path: "/v1/bot/drive/hello", risk: "read"},
		{row: "drive#94", id: "drive.file.ancestors", method: "GET", path: "/v1/bot/drive/files/{file_id}/ancestors", risk: "read",
			params: []xin2424DriveParam{fileID}, lossless: []string{"ancestors[].id"}},
		{row: "drive#95", id: "drive.doc.mount-by-role", method: "POST", path: "/v1/bot/drive/docs/mount", risk: "write",
			params:       []xin2424DriveParam{{name: "X-Space-Id", in: "header", required: true, flag: "octo-space-id"}},
			bodyRequired: []string{"doc_id", "space_id"}, bodyNeeded: true, lossless: []string{"id", "parent_id"}},
		{row: "drive#96", id: "drive.file.doc-alive", method: "GET", path: "/v1/bot/drive/files/{file_id}/doc-alive", risk: "read",
			params: []xin2424DriveParam{fileID}},
		{row: "drive#98", id: "drive.im-transfer.lookup", method: "POST", path: "/v1/bot/drive/blobs/im-transferred/batch", risk: "write",
			bodyRequired: []string{}, bodyNeeded: true},
		{row: "drive#99", id: "drive.share.transfer", method: "POST", path: "/v1/bot/drive/shares/{share_token}/transfer", risk: "write",
			params: []xin2424DriveParam{shareToken}, bodyRequired: []string{}, writeOnly: []string{"password"},
			lossless: []string{"file_id", "parent_id"}},
		{row: "drive#100", id: "drive.share.existing-copies", method: "GET", path: "/v1/bot/drive/shares/{share_token}/existing-copies", risk: "read",
			params: []xin2424DriveParam{shareToken}, lossless: []string{"existing_copies[].file_id"}},
		{row: "drive#103", id: "drive.trash.list", method: "GET", path: "/v1/bot/drive/trash", risk: "read",
			params: []xin2424DriveParam{
				{name: "scope", in: "query"}, {name: "space_id", in: "query"},
				{name: "cursor", in: "query"}, {name: "limit", in: "query"},
			},
			lossless: []string{"items[].id", "items[].parent_id"}},
		{row: "drive#104", id: "drive.trash.restore", method: "POST", path: "/v1/bot/drive/trash/{file_id}/restore", risk: "write",
			params: []xin2424DriveParam{fileID}, lossless: []string{"id", "parent_id"}},
		{row: "drive#105", id: "drive.trash.delete", method: "DELETE", path: "/v1/bot/drive/trash/{file_id}", risk: "high-risk-write",
			params: []xin2424DriveParam{fileID}},
	}
}

func TestXIN2424DriveNewOperations(t *testing.T) {
	reg := registry.MustNew()
	for _, row := range xin2424DriveRows() {
		t.Run(row.row, func(t *testing.T) {
			op, ok := reg.GetOperation(row.id)
			if !ok {
				t.Fatalf("%s: operation %s not in registry", row.row, row.id)
			}
			if op.Method != row.method || op.Path != row.path {
				t.Errorf("got %s %s, want %s %s", op.Method, op.Path, row.method, row.path)
			}
			if op.Risk != row.risk {
				t.Errorf("risk: got %q, want %q", op.Risk, row.risk)
			}
			if op.BinaryResponse || op.Multipart || op.BinaryBody {
				t.Errorf("JSON operation must not be binary/multipart: binary=%v multipart=%v binaryBody=%v",
					op.BinaryResponse, op.Multipart, op.BinaryBody)
			}
			if op.MountByTokenKind["user_key"] != "/v1/user/drive" || op.MountByTokenKind["user_bot"] != "/v1/bot/drive" {
				t.Errorf("mount table not inherited: %v", op.MountByTokenKind)
			}
			if len(op.Parameters) != len(row.params) {
				t.Errorf("parameters: got %d, want %d (%+v)", len(op.Parameters), len(row.params), op.Parameters)
			}
			for _, want := range row.params {
				var got *registry.ParamInfo
				for i := range op.Parameters {
					if op.Parameters[i].Name == want.name {
						got = &op.Parameters[i]
					}
				}
				if got == nil {
					t.Errorf("parameter %s missing", want.name)
					continue
				}
				if got.In != want.in || got.Required != want.required || got.Secret != want.secret || got.FlagName != want.flag {
					t.Errorf("parameter %s: got in=%s required=%v secret=%v flag=%q, want in=%s required=%v secret=%v flag=%q",
						want.name, got.In, got.Required, got.Secret, got.FlagName, want.in, want.required, want.secret, want.flag)
				}
			}
			switch {
			case row.bodyRequired == nil:
				if op.RequestBody != nil {
					t.Errorf("unexpected request body %+v", op.RequestBody)
				}
			case op.RequestBody == nil:
				t.Errorf("request body missing")
			default:
				got := append([]string{}, op.RequestBody.Required...)
				sort.Strings(got)
				if strings.Join(got, ",") != strings.Join(row.bodyRequired, ",") {
					t.Errorf("body required: got %v, want %v", got, row.bodyRequired)
				}
				if op.RequestBodyRequired != row.bodyNeeded {
					t.Errorf("requestBody.required: got %v, want %v", op.RequestBodyRequired, row.bodyNeeded)
				}
				for name, prop := range op.RequestBody.Properties {
					if prop.Secret {
						t.Errorf("body field %s uses x-octo-secret; generated leaves must use writeOnly", name)
					}
				}
				for _, name := range row.writeOnly {
					if !op.RequestBody.Properties[name].WriteOnly {
						t.Errorf("body field %s must be writeOnly", name)
					}
				}
			}
			if strings.Join(op.LosslessIDFields, ",") != strings.Join(row.lossless, ",") {
				t.Errorf("lossless id fields: got %v, want %v", op.LosslessIDFields, row.lossless)
			}
		})
	}
}

// TestXIN2424DriveUploadPartLimits pins the multipart bounds enforced by
// octo-drive internal/modules/upload/service.go (maxPartsPerPresign=20,
// maxPartCount=10000).
func TestXIN2424DriveUploadPartLimits(t *testing.T) {
	reg := registry.MustNew()
	presign, _ := reg.GetOperation("drive.upload.parts_presign")
	parts := presign.RequestBody.Properties["part_numbers"]
	if parts.Type != "array" || parts.MinItems != 1 || parts.MaxItems != 20 || parts.Items == nil ||
		parts.Items.Minimum == nil || *parts.Items.Minimum != 1 || parts.Items.Maximum == nil || *parts.Items.Maximum != 10000 {
		t.Errorf("part_numbers bounds drifted: %+v", parts)
	}
	complete, _ := reg.GetOperation("drive.upload.complete")
	list := complete.RequestBody.Properties["parts"]
	if list.Type != "array" || list.MinItems != 1 || list.MaxItems != 10000 || list.Items == nil {
		t.Fatalf("parts bounds drifted: %+v", list)
	}
	got := append([]string{}, list.Items.Required...)
	sort.Strings(got)
	if strings.Join(got, ",") != "etag,part_number" {
		t.Errorf("parts[] required: got %v", got)
	}
}

// TestXIN2424DriveExistingShareContracts pins the corrections made to the
// already-declared share operations (rows 75, 81, 83).
func TestXIN2424DriveExistingShareContracts(t *testing.T) {
	reg := registry.MustNew()

	create, ok := reg.GetOperation("drive.share.blob-create")
	if !ok {
		t.Fatal("drive.share.blob-create missing")
	}
	perm := create.RequestBody.Properties["permission"]
	if perm.Default != "view" {
		t.Errorf("permission default: got %v, want view (share/service.go CreateShare stores view when omitted)", perm.Default)
	}
	if exp := create.RequestBody.Properties["expires_in_seconds"]; exp.Minimum == nil || *exp.Minimum != 0 {
		t.Errorf("expires_in_seconds must declare minimum 0")
	}
	if pw := create.RequestBody.Properties["password"]; !pw.Secret || pw.MaxLength != 72 {
		t.Errorf("password must stay secret with maxLength 72: %+v", pw)
	}

	access, ok := reg.GetOperation("drive.share.access")
	if !ok {
		t.Fatal("drive.share.access missing")
	}
	if strings.Join(access.LosslessIDFields, ",") != "file_id,existing_copies[].file_id" {
		t.Errorf("access lossless fields: %v", access.LosslessIDFields)
	}
	if access.ResponseSchema == nil {
		t.Fatal("access response schema missing")
	}
	if _, ok := access.ResponseSchema.Properties["existing_copies"]; !ok {
		t.Errorf("access response must declare existing_copies")
	}

	download, ok := reg.GetOperation("drive.share.download")
	if !ok {
		t.Fatal("drive.share.download missing")
	}
	if download.Method != "POST" || download.Path != "/v1/bot/drive/shares/{share_token}/download" {
		t.Errorf("download route drifted: %s %s", download.Method, download.Path)
	}
	if len(download.Parameters) != 1 || !download.Parameters[0].Secret {
		t.Errorf("download share_token must be secret: %+v", download.Parameters)
	}
}

// TestXIN2424DriveOrgMembersNotAdded records that row 102 stays out of the
// spec: TestDrive_NoOrgCommands forbids drive.org.* and the bot roster is
// already served by bot.space-members.
func TestXIN2424DriveOrgMembersNotAdded(t *testing.T) {
	reg := registry.MustNew()
	if _, ok := reg.GetOperation("drive.org.members"); ok {
		t.Error("drive.org.members must not be registered")
	}
	if _, ok := reg.GetOperation("bot.space-members"); !ok {
		t.Error("bot.space-members missing")
	}
}
