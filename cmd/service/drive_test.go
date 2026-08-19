package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// The drive domain is spec-driven except for six composite leaves registered in
// package cmd. These tests pin the generated 40 against the octo-drive routes
// verified in the drive worktree, and pin the identity routing (bot mount vs
// user-API-key mount) that x-octo-mount-by-token-kind introduces.

// rootWithDriveToken builds the drive command tree with a specific credential
// token, so the same command can be driven as a bot or as a real person.
func rootWithDriveToken(t *testing.T, token string, handler http.HandlerFunc) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: srv.URL, BotToken: token, Format: "json"}
	tf.SetConfig(cfg)
	cred := &credential.BotCredential{Token: token, Source: "test"}
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	return root, tf
}

// --- registry contract ---

// TestDrive_RegistryShape pins every drive operation's method and path against
// the octo-drive routes. Appendix B of the drive spec is the contract; this is
// the test that fails if a spec edit silently moves an endpoint.
func TestDrive_RegistryShape(t *testing.T) {
	reg := registry.MustNew()

	type want struct{ method, path string }
	cases := map[string]want{
		// space (6)
		"drive.space.create":          {"POST", "/v1/bot/drive/spaces"},
		"drive.space.list":            {"GET", "/v1/bot/drive/spaces"},
		"drive.space.ensure-personal": {"POST", "/v1/bot/drive/spaces/personal"},
		"drive.space.get":             {"GET", "/v1/bot/drive/spaces/{space_id}"},
		"drive.space.rename":          {"PUT", "/v1/bot/drive/spaces/{space_id}"},
		"drive.space.delete":          {"DELETE", "/v1/bot/drive/spaces/{space_id}"},
		// member (4)
		"drive.member.list":     {"GET", "/v1/bot/drive/spaces/{space_id}/members"},
		"drive.member.add":      {"POST", "/v1/bot/drive/spaces/{space_id}/members"},
		"drive.member.set-role": {"PUT", "/v1/bot/drive/spaces/{space_id}/members/{uid}"},
		"drive.member.remove":   {"DELETE", "/v1/bot/drive/spaces/{space_id}/members/{uid}"},
		// browse (1)
		"drive.browse": {"GET", "/v1/bot/drive/browse"},
		// search (1)
		"drive.search": {"POST", "/v1/bot/drive/search"},
		// folder (5)
		"drive.folder.create": {"POST", "/v1/bot/drive/folders"},
		"drive.folder.list":   {"GET", "/v1/bot/drive/folders/{space_id}/{parent_id}"},
		"drive.folder.rename": {"PATCH", "/v1/bot/drive/folders/{folder_id}/rename"},
		"drive.folder.move":   {"PATCH", "/v1/bot/drive/folders/{folder_id}/move"},
		"drive.folder.delete": {"DELETE", "/v1/bot/drive/folders/{folder_id}"},
		// file (4)
		"drive.file.get":    {"GET", "/v1/bot/drive/files/{file_id}"},
		"drive.file.move":   {"POST", "/v1/bot/drive/files/{file_id}/move"},
		"drive.file.copy":   {"POST", "/v1/bot/drive/files/{file_id}/copy"},
		"drive.file.rename": {"POST", "/v1/bot/drive/files/{file_id}/rename"},
		// blob (4)
		"drive.blob.create": {"POST", "/v1/bot/drive/blobs"},
		"drive.blob.get":    {"GET", "/v1/bot/drive/blobs/{blob_id}"},
		"drive.blob.list":   {"GET", "/v1/bot/drive/blobs"},
		"drive.blob.delete": {"DELETE", "/v1/bot/drive/blobs/{blob_id}"},
		// upload + download (4)
		"drive.upload.prepare": {"POST", "/v1/bot/drive/files/prepare-upload"},
		"drive.upload.confirm": {"POST", "/v1/bot/drive/files/{file_id}/confirm-upload"},
		"drive.upload.cancel":  {"POST", "/v1/bot/drive/files/{file_id}/cancel-upload"},
		"drive.download.url":   {"GET", "/v1/bot/drive/files/{file_id}/download"},
		// docs (4)
		"drive.doc.mount":      {"POST", "/v1/bot/drive/docs"},
		"drive.doc.unmount":    {"DELETE", "/v1/bot/drive/docs/{file_id}"},
		"drive.doc.list":       {"GET", "/v1/bot/drive/docs"},
		"drive.doc.candidates": {"GET", "/v1/bot/drive/mountable-docs"},
		// share (5 endpoints; `share create` is a composite with no endpoint of its own)
		"drive.share.blob-create": {"POST", "/v1/bot/drive/shares"},
		"drive.share.list":        {"GET", "/v1/bot/drive/shares"},
		"drive.share.revoke":      {"DELETE", "/v1/bot/drive/shares/{share_id}"},
		"drive.share.access":      {"POST", "/v1/bot/drive/shares/{share_token}/access"},
		"drive.share.download":    {"POST", "/v1/bot/drive/shares/{share_token}/download"},
		// invite (4)
		"drive.invite.create": {"POST", "/v1/bot/drive/spaces/{space_id}/invites"},
		"drive.invite.list":   {"GET", "/v1/bot/drive/spaces/{space_id}/invites"},
		"drive.invite.revoke": {"DELETE", "/v1/bot/drive/spaces/{space_id}/invites/{invite_id}"},
		"drive.invite.accept": {"POST", "/v1/bot/drive/invites/{invite_token}/accept"},
		// im-transfer (1)
		"drive.im-transfer.create": {"POST", "/v1/bot/drive/blobs/transfer-from-im"},
	}

	ops := reg.ListOperations("drive")
	if len(ops) != len(cases) {
		t.Fatalf("drive: got %d operations, want %d (%v)", len(ops), len(cases), driveOperationIDs(ops))
	}
	for id, w := range cases {
		op, ok := reg.GetOperation(id)
		if !ok {
			t.Errorf("%s: not in registry", id)
			continue
		}
		if op.Method != w.method || op.Path != w.path {
			t.Errorf("%s: got %s %s, want %s %s", id, op.Method, op.Path, w.method, w.path)
		}
	}
}

// TestDrive_NoOrgCommands asserts the org member/search endpoints are absent.
// The product's member picker is a frontend filter over the space roster, not a
// backend search, so the CLI must not pretend to offer one.
func TestDrive_NoOrgCommands(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.ListOperations("drive") {
		if got := op.ID; got == "drive.org.members" || got == "drive.org.search" {
			t.Errorf("drive: org command %q must not be registered", got)
		}
		if op.Path == "/v1/bot/drive/org/members" || op.Path == "/v1/bot/drive/org/search" {
			t.Errorf("drive: org path %q must not be registered", op.Path)
		}
	}
	root, _ := rootWithDriveToken(t, "bf_test", func(w http.ResponseWriter, r *http.Request) {})
	if org := findCmd(findCmd(root, "drive"), "org"); org != nil {
		t.Error("drive: the org subtree must not exist")
	}
}

// TestDrive_SpecIdentityMetadata pins the routing metadata itself: the mount
// table and the allowed-kind list must cover exactly the same token kinds, or a
// credential could pass the gate and then find no mount.
func TestDrive_SpecIdentityMetadata(t *testing.T) {
	reg := registry.MustNew()
	op, ok := reg.GetOperation("drive.space.list")
	if !ok {
		t.Fatal("drive.space.list missing")
	}
	wantMounts := map[string]string{
		"user_key": "/v1/user/drive",
		"user_bot": "/v1/bot/drive",
		"app_bot":  "/v1/bot/drive",
	}
	if len(op.MountByTokenKind) != len(wantMounts) {
		t.Fatalf("mount table: got %v, want %v", op.MountByTokenKind, wantMounts)
	}
	for kind, mount := range wantMounts {
		if op.MountByTokenKind[kind] != mount {
			t.Errorf("mount[%s]: got %q, want %q", kind, op.MountByTokenKind[kind], mount)
		}
	}
	if len(op.AllowedTokenKinds) != len(wantMounts) {
		t.Fatalf("allowed kinds: got %v, want the mount table's keys", op.AllowedTokenKinds)
	}
	for _, kind := range op.AllowedTokenKinds {
		if _, ok := wantMounts[kind]; !ok {
			t.Errorf("allowed kind %q has no mount entry", kind)
		}
	}
	// Every drive path must be written against one of the declared mounts,
	// otherwise swapMount cannot rewrite it and the request would fail at runtime.
	for _, info := range reg.ListOperations("drive") {
		matched := false
		for _, mount := range wantMounts {
			if len(info.Path) > len(mount) && info.Path[:len(mount)] == mount {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s: path %q does not start with a declared mount", info.ID, info.Path)
		}
	}
}

// TestDrive_OnlyDriveDeclaresIdentityRouting keeps the new extensions opt-in.
// If another domain starts declaring them, that is a deliberate decision that
// must update this test — it must not happen by copy-paste.
func TestDrive_OnlyDriveDeclaresIdentityRouting(t *testing.T) {
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		ops := reg.ListOperations(svc)
		if len(ops) == 0 {
			continue
		}
		op, ok := reg.GetOperation(ops[0].ID)
		if !ok {
			continue
		}
		declares := len(op.MountByTokenKind) > 0 || len(op.AllowedTokenKinds) > 0
		if svc == "drive" && !declares {
			t.Error("drive must declare the identity-routing extensions")
		}
		if svc != "drive" && declares {
			t.Errorf("%s must not declare identity-routing extensions (mounts=%v kinds=%v)",
				svc, op.MountByTokenKind, op.AllowedTokenKinds)
		}
	}
}

// --- identity routing at runtime ---

// TestDrive_MountByTokenKind drives the same command with each token kind and
// asserts the URL the backend actually receives.
func TestDrive_MountByTokenKind(t *testing.T) {
	cases := []struct {
		name, token, wantPath string
	}{
		{"user key routes to the user mount", "uk_person", "/v1/user/drive/spaces"},
		{"user bot stays on the bot mount", "bf_bot", "/v1/bot/drive/spaces"},
		{"app bot stays on the bot mount", "app_bot", "/v1/bot/drive/spaces"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotAuth string
			root, _ := rootWithDriveToken(t, tc.token, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotAuth = r.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{"spaces":[]}`))
			})
			root.SetArgs([]string{"drive", "space", "list"})
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tc.wantPath)
			}
			if gotAuth != "Bearer "+tc.token {
				t.Errorf("Authorization: got %q, want the active credential", gotAuth)
			}
		})
	}
}

// TestDrive_NoSpaceHeader confirms drive never sends X-Space-Id: the mount's
// auth middleware derives the tenant from the verified identity, and a
// client-supplied header that disagrees is rejected server-side (403).
func TestDrive_NoSpaceHeader(t *testing.T) {
	srv := httptest.NewServer(nil)
	srv.Close()

	var sawHeader bool
	tf := cmdutil.NewTestFactory()
	handler := func(w http.ResponseWriter, r *http.Request) {
		_, sawHeader = r.Header["X-Space-Id"]
		_, _ = w.Write([]byte(`{"spaces":[]}`))
	}
	live := httptest.NewServer(http.HandlerFunc(handler))
	defer live.Close()

	cfg := &config.Config{APIBaseURL: live.URL, BotToken: "bf_bot", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_bot", SpaceID: "octo-space-1", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.SetClient(client.New(cfg, cred, client.Options{ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	root.SetArgs([]string{"drive", "space", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if sawHeader {
		t.Error("drive must not send X-Space-Id even when the credential carries a space")
	}
}

// TestDrive_UnsupportedTokenKind pins the gate's taxonomy: an incompatible
// credential is a validation error (exit 2) telling the caller to switch
// credentials, matching the existing message-search gate. It must fail before
// any request goes out.
func TestDrive_UnsupportedTokenKind(t *testing.T) {
	var called bool
	root, tf := rootWithDriveToken(t, "session_web_token", func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	root.SetArgs([]string{"drive", "space", "list"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unsupported token kind")
	}
	if called {
		t.Error("no request may be sent when the token kind is rejected")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected an ExitError, got %T", err)
	}
	if ee.Code != "TOKEN_KIND_NOT_ALLOWED" {
		t.Errorf("code: got %q, want TOKEN_KIND_NOT_ALLOWED", ee.Code)
	}
	if ee.Type != "validation" || ee.ExitCode() != 2 {
		t.Errorf("taxonomy: got %s/%d, want validation/2", ee.Type, ee.ExitCode())
	}
	if tf.ErrOut.Len() == 0 {
		t.Error("expected an error envelope on stderr")
	}
}

// TestDrive_ExistingDomainsUnaffected is the regression guard for the routing
// change: a domain that declares no mount table must reach the byte-identical
// path it did before, with no credential-kind gate.
func TestDrive_ExistingDomainsUnaffected(t *testing.T) {
	cases := []struct {
		name, token string
		args        []string
		wantPath    string
	}{
		{"docs list as user key", "uk_person", []string{"docs", "list"}, "/v1/bot/docs"},
		{"docs list as app bot", "app_bot", []string{"docs", "list"}, "/v1/bot/docs"},
		{"group list as app bot", "app_bot", []string{"group", "list"}, "/v1/bot/groups"},
		{"event list as user key", "uk_person", []string{"event", "list"}, "/v1/bot/events"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			root, _ := rootWithDriveToken(t, tc.token, func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{}`))
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

// --- request shape ---

// TestDrive_RequestShapes checks that the generated leaves send exactly the
// body / query octo-drive binds, including the uint64 ids that go out as JSON
// integers even though their flags are decimal strings.
func TestDrive_RequestShapes(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantPath  string
		wantQuery map[string]string
		wantBody  string
	}{
		{
			name:     "folder create promotes body fields",
			args:     []string{"drive", "folder", "create", "--space-id", "shared:s1", "--parent-id", "7", "--name", "contracts"},
			wantPath: "/v1/bot/drive/folders",
			wantBody: `{"name":"contracts","parent_id":7,"space_id":"shared:s1"}`,
		},
		{
			name:     "member add sends uid and role",
			args:     []string{"drive", "member", "add", "shared:s1", "--uid", "u1", "--role", "editor"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/members",
			wantBody: `{"role":"editor","uid":"u1"}`,
		},
		{
			name:      "browse sends every filter as a query param",
			args:      []string{"drive", "browse", "--space-id", "shared:s1", "--parent-id", "0", "--type", "blob", "--source", "user-upload"},
			wantPath:  "/v1/bot/drive/browse",
			wantQuery: map[string]string{"space_id": "shared:s1", "parent_id": "0", "type": "blob", "source": "user-upload"},
		},
		{
			name:     "doc mount does not accept or send doc_title",
			args:     []string{"drive", "doc", "mount", "--space-id", "shared:s1", "--doc-id", "d_1", "--parent-id", "3"},
			wantPath: "/v1/bot/drive/docs",
			wantBody: `{"doc_id":"d_1","parent_id":3,"space_id":"shared:s1"}`,
		},
		{
			name:     "upload prepare sends the exact byte count",
			args:     []string{"drive", "upload", "prepare", "--space-id", "shared:s1", "--name", "a.pdf", "--size", "1048576", "--content-type", "application/pdf"},
			wantPath: "/v1/bot/drive/files/prepare-upload",
			wantBody: `{"content_type":"application/pdf","name":"a.pdf","size":1048576,"space_id":"shared:s1"}`,
		},
		{
			name:     "im-transfer keeps the message id a string",
			args:     []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "2", "--im-msg-id", "9007199254740993", "--target-space-id", "shared:s1"},
			wantPath: "/v1/bot/drive/blobs/transfer-from-im",
			wantBody: `{"im_channel_type":2,"im_group_no":"g1","im_msg_id":"9007199254740993","target_space_id":"shared:s1"}`,
		},
		{
			name:     "invite create sends role and expiry",
			args:     []string{"drive", "invite", "create", "shared:s1", "--role", "editor", "--expires-in-seconds", "3600"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/invites",
			wantBody: `{"expires_in_seconds":3600,"role":"editor"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotBody string
			var gotQuery map[string]string
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				gotQuery = map[string]string{}
				for k, v := range r.URL.Query() {
					gotQuery[k] = v[0]
				}
				w.WriteHeader(http.StatusNoContent)
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tc.wantPath)
			}
			if tc.wantBody != "" && gotBody != tc.wantBody {
				t.Errorf("body:\n got %s\nwant %s", gotBody, tc.wantBody)
			}
			for k, v := range tc.wantQuery {
				if gotQuery[k] != v {
					t.Errorf("query[%s]: got %q, want %q", k, gotQuery[k], v)
				}
			}
		})
	}
}

// TestDrive_IMTransferRequiresChannelType guards the required-field contract:
// im_channel_type picks the route the backend reads the source message through,
// so omitting it would send a body the backend decodes as the uint8 zero value —
// not a channel kind at all. The field must fail locally rather than reach the
// backend; this test fails if it is ever demoted to optional.
//
// It is NOT the transfer idempotency key: dedup is (target space, type=blob,
// object path), so a wrong-but-accepted value cannot produce a duplicate file.
// What a wrong value does break is the already-transferred lookup
// (POST {mount}/blobs/im-transferred/batch), which matches on the persisted
// source_key "channelType#channelID#msgID" — and, across the 1 vs 2/5
// boundary, the message lookup itself, since 1 uses the DM route while 2 and 5
// share the group route (octo-drive internal/octoserver/client.go imMessageURL,
// internal/modules/imtransfer/service.go buildSourceKey).
func TestDrive_IMTransferRequiresChannelType(t *testing.T) {
	var backendHits atomic.Int32
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		backendHits.Add(1)
	})
	root.SetArgs([]string{"drive", "im-transfer", "create",
		"--im-group-no", "g1", "--im-msg-id", "9007199254740993", "--target-space-id", "shared:s1"})

	err := root.Execute()
	if err == nil {
		t.Fatal("im-transfer create without --im-channel-type must fail validation locally")
	}
	if !strings.Contains(err.Error(), "im_channel_type") {
		t.Errorf("error should name the missing field, got %q", err)
	}
	if hits := backendHits.Load(); hits != 0 {
		t.Errorf("request reached backend %d time(s); a partial transfer must never be sent", hits)
	}
}

// --- lossless uint64 ids ---

// TestDrive_Uint64RoundTrip is the end-to-end precision guard: the maximum
// uint64 survives flag → JSON integer on the wire → decimal string in the
// envelope, with no rounding anywhere.
func TestDrive_Uint64RoundTrip(t *testing.T) {
	const maxU64 = "18446744073709551615"

	var gotBody string
	root, tf := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		_, _ = w.Write([]byte(`{"id":` + maxU64 + `,"parent_id":` + maxU64 + `,"name":"f"}`))
	})
	root.SetArgs([]string{"drive", "folder", "create", "--space-id", "s1", "--parent-id", maxU64, "--name", "f"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Request: a bare JSON integer, not a string and not a rounded float.
	if want := `"parent_id":` + maxU64; !strings.Contains(gotBody, want) {
		t.Errorf("request body: got %s, want it to contain %s", gotBody, want)
	}
	// Response: decimal strings, so an Agent's JSON parser cannot round them.
	var env struct {
		Data struct {
			ID       string `json:"id"`
			ParentID string `json:"parent_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v (%s)", err, tf.Out.String())
	}
	if env.Data.ID != maxU64 {
		t.Errorf("data.id: got %q, want %q", env.Data.ID, maxU64)
	}
	if env.Data.ParentID != maxU64 {
		t.Errorf("data.parent_id: got %q, want %q", env.Data.ParentID, maxU64)
	}
}

// TestDrive_Uint64Rejected fails malformed and out-of-range ids locally, before
// a request is sent, so a mangled id can never address a different row.
func TestDrive_Uint64Rejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"body flag above the uint64 max", []string{"drive", "folder", "create", "--space-id", "s1", "--name", "f", "--parent-id", "18446744073709551616"}},
		{"body flag with a decimal point", []string{"drive", "folder", "create", "--space-id", "s1", "--name", "f", "--parent-id", "1.5"}},
		{"body flag in exponent form", []string{"drive", "folder", "create", "--space-id", "s1", "--name", "f", "--parent-id", "1e3"}},
		{"negative body flag", []string{"drive", "folder", "create", "--space-id", "s1", "--name", "f", "--parent-id", "-1"}},
		{"hex body flag", []string{"drive", "folder", "create", "--space-id", "s1", "--name", "f", "--parent-id", "0xff"}},
		{"path arg above the uint64 max", []string{"drive", "file", "get", "18446744073709551616"}},
		{"non-numeric path arg", []string{"drive", "file", "get", "abc"}},
		{"query flag above the uint64 max", []string{"drive", "blob", "list", "--space-id", "s1", "--parent-id", "18446744073709551616"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatal("expected a local validation error")
			}
			if called {
				t.Error("the request must not be sent when an id fails validation")
			}
			ee := output.AsExitError(err)
			if ee == nil || ee.Type != "validation" {
				t.Errorf("taxonomy: got %v, want a validation error", err)
			}
		})
	}
}

// --- response field aliases ---

// TestDrive_ShareListFieldAliases pins the share DTO split. The backend returns
// one opaque `id` that is both the management handle and the access token; the
// CLI must surface both meanings by name so a caller never has to guess, and
// must not leave the ambiguous `id` behind.
func TestDrive_ShareListFieldAliases(t *testing.T) {
	root, tf := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"shares":[{"id":"tok-1","file_id":18446744073709551615,"permission":"download"}]}`))
	})
	root.SetArgs([]string{"drive", "share", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var env struct {
		Data struct {
			Shares []map[string]any `json:"shares"`
		} `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v (%s)", err, tf.Out.String())
	}
	if len(env.Data.Shares) != 1 {
		t.Fatalf("shares: got %v", env.Data.Shares)
	}
	share := env.Data.Shares[0]
	if share["share_id"] != "tok-1" {
		t.Errorf("share_id: got %v, want tok-1", share["share_id"])
	}
	if share["share_token"] != "tok-1" {
		t.Errorf("share_token: got %v, want tok-1", share["share_token"])
	}
	if _, ok := share["id"]; ok {
		t.Error("the ambiguous `id` must be replaced by share_id / share_token")
	}
	if share["drive_file_id"] != "18446744073709551615" {
		t.Errorf("drive_file_id: got %v, want the decimal string", share["drive_file_id"])
	}
	if _, ok := share["file_id"]; ok {
		t.Error("file_id must be renamed to drive_file_id")
	}
}

// TestDrive_InviteFieldAliases pins the invite split: revoke takes the id,
// accept takes the token, and the two must not share a name.
func TestDrive_InviteFieldAliases(t *testing.T) {
	root, tf := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"inv-1","space_id":"shared:s1","token":"secret-tok","role":"editor"}`))
	})
	root.SetArgs([]string{"drive", "invite", "create", "shared:s1", "--role", "editor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v (%s)", err, tf.Out.String())
	}
	for key, want := range map[string]string{
		"invite_id":      "inv-1",
		"drive_space_id": "shared:s1",
		"invite_token":   "secret-tok",
	} {
		if env.Data[key] != want {
			t.Errorf("%s: got %v, want %q", key, env.Data[key], want)
		}
	}
	for _, gone := range []string{"id", "token", "space_id"} {
		if _, ok := env.Data[gone]; ok {
			t.Errorf("%s must be renamed so revoke and accept cannot be confused", gone)
		}
	}
}

// --- secret redaction ---

// TestDrive_SecretRedaction covers x-octo-secret: an invite token in the URL
// path and a share password in the body must be masked in --dry-run output,
// which is the surface most likely to end up pasted into a ticket or a log.
func TestDrive_SecretRedaction(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		secret string
	}{
		{"invite token in the URL path", []string{"drive", "invite", "accept", "super-secret-token"}, "super-secret-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, tf := rootWithDriveDryRun(t, "bf_bot")
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			out := tf.Out.String()
			if strings.Contains(out, tc.secret) {
				t.Errorf("dry-run output leaked the secret: %s", out)
			}
			if !strings.Contains(out, "REDACTED") {
				t.Errorf("dry-run output should show the secret as redacted: %s", out)
			}
		})
	}
}

// rootWithDriveDryRun wires a dry-run client so no request is sent and the
// rendered request description can be inspected.
func rootWithDriveDryRun(t *testing.T, token string) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: "https://octo.test", BotToken: token, Format: "json"}
	cred := &credential.BotCredential{Token: token, Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.Globals.DryRun = true
	tf.SetClient(client.New(cfg, cred, client.Options{DryRun: true, ErrOut: io.Discard}))
	tf.RegistryFunc = registry.MustNew

	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	RegisterServiceCommands(root, tf.Factory)
	return root, tf
}

// driveOperationIDs renders an operation list for a failure message.
func driveOperationIDs(ops []registry.OperationInfo) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, op.ID)
	}
	return out
}
