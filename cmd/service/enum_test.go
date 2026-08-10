package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- canonical comparison ---

// TestEnum_CanonicalComparison pins the type-tolerance the enum gate needs: the
// same wire value arrives as different Go types depending on whether it came
// from a promoted flag (int), from --data (float64) or from a uint64 id flag
// (json.Number), while every spec enum entry is whatever encoding/json produced
// for the spec literal. A plain == would reject valid input on the --data path.
func TestEnum_CanonicalComparison(t *testing.T) {
	intEnum := []any{float64(1), float64(2), float64(5)}
	strEnum := []any{"view", "download"}
	boolEnum := []any{true}

	allowed := []struct {
		name  string
		value any
		enum  []any
	}{
		{"promoted int flag", 2, intEnum},
		{"--data float", float64(2), intEnum},
		{"--data integral float written as 2.0", 2.0, intEnum},
		{"json.Number", json.Number("5"), intEnum},
		{"int64", int64(1), intEnum},
		{"uint64", uint64(1), intEnum},
		{"string", "download", strEnum},
		{"bool", true, boolEnum},
		{"empty enum accepts anything", "whatever", nil},
	}
	for _, tc := range allowed {
		t.Run("allow/"+tc.name, func(t *testing.T) {
			if err := checkEnum("--x", tc.value, tc.enum); err != nil {
				t.Errorf("checkEnum(%#v) = %v, want nil", tc.value, err)
			}
		})
	}

	rejected := []struct {
		name  string
		value any
		enum  []any
	}{
		{"int outside the set", 9, intEnum},
		{"zero is not a member", 0, intEnum},
		{"negative", -1, intEnum},
		{"float outside the set", float64(3), intEnum},
		{"non-integral float", 1.5, intEnum},
		{"string is not a number", "1", intEnum},
		{"number is not a string", float64(1), strEnum},
		{"string outside the set", "edit", strEnum},
		{"empty string", "", strEnum},
		{"bool outside the set", false, boolEnum},
	}
	for _, tc := range rejected {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			err := checkEnum("--x", tc.value, tc.enum)
			if err == nil {
				t.Fatalf("checkEnum(%#v) = nil, want rejection", tc.value)
			}
			if err.Type != "validation" || err.Code != enumNotAllowed {
				t.Errorf("err = %s/%s, want validation/%s", err.Type, err.Code, enumNotAllowed)
			}
			if err.ExitCode() != 2 {
				t.Errorf("exit code = %d, want 2", err.ExitCode())
			}
			if !strings.Contains(err.Message, "--x") {
				t.Errorf("message %q should name the flag", err.Message)
			}
		})
	}
}

// TestEnum_NonScalarIsRejected pins that an object / array / null where the
// schema declares a scalar vocabulary fails locally. Forwarding it is what let
// `--data '{"im_channel_type":[1]}'` reach the backend and come back as an
// internal decode error naming a server struct — the same leak the scalar cases
// close.
func TestEnum_NonScalarIsRejected(t *testing.T) {
	for _, value := range []any{
		map[string]any{"a": 1},
		[]any{"view"},
		nil,
	} {
		err := checkEnum("--x", value, []any{"view", "download"})
		if err == nil {
			t.Errorf("checkEnum(%#v) = nil, want rejection", value)
			continue
		}
		if err.Code != enumNotAllowed {
			t.Errorf("checkEnum(%#v) code = %s, want %s", value, err.Code, enumNotAllowed)
		}
	}
}

// --- schema walk: kinds the specs do and do not exercise today ---

// TestEnum_BodySchemaWalkCoversEveryKind drives the validator against a
// synthetic schema so int / string / bool enums, array-item enums and enums
// nested inside an array of objects are all covered. bool enums have no spec
// site today; this is what keeps the branch honest if one lands.
func TestEnum_BodySchemaWalkCoversEveryKind(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"mode":    {Type: "string", Enum: []any{"fast", "slow"}},
			"level":   {Type: "integer", Enum: []any{float64(1), float64(2)}},
			"strict":  {Type: "boolean", Enum: []any{true}},
			"tags":    {Type: "array", Items: &registry.SchemaInfo{Type: "string", Enum: []any{"a", "b"}}},
			"sources": {Type: "array", Items: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{"kind": {Type: "integer", Enum: []any{float64(1), float64(3)}}, "mode": {Type: "string", Enum: []any{"fast", "slow"}}}}},
		},
	}
	v := bodySchemaValidator{flagFor: map[string]string{"mode": "mode", "level": "level"}}

	ok := map[string]any{
		"mode": "fast", "level": 2, "strict": true,
		"tags":    []any{"a", "b"},
		"sources": []any{map[string]any{"kind": float64(3)}},
	}
	if err := v.validate(schema, ok, "", ""); err != nil {
		t.Fatalf("valid body rejected: %v", err)
	}

	cases := []struct {
		name      string
		body      map[string]any
		wantLabel string
	}{
		{"string enum", map[string]any{"mode": "sideways"}, "--mode"},
		{"int enum", map[string]any{"level": 7}, "--level"},
		{"bool enum", map[string]any{"strict": false}, "field strict"},
		{"array item enum", map[string]any{"tags": []any{"a", "z"}}, "field tags"},
		{"enum nested in an array of objects", map[string]any{"sources": []any{map[string]any{"kind": float64(9)}}}, "--data field sources[0].kind"},
		// A nested field sharing its name with a top-level promoted flag must NOT
		// borrow that flag's label: telling an agent to fix --mode when the bad
		// value is at sources[0].mode sends it to the wrong input.
		{"nested field shadowing a top-level flag name", map[string]any{"sources": []any{map[string]any{"mode": "sideways"}}}, "--data field sources[0].mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v.validate(schema, tc.body, "", "")
			if err == nil {
				t.Fatalf("body %#v was accepted, want rejection", tc.body)
			}
			if err.Code != enumNotAllowed {
				t.Fatalf("code = %s, want %s", err.Code, enumNotAllowed)
			}
			if !strings.Contains(err.Message, tc.wantLabel) {
				t.Errorf("message %q should reference %q", err.Message, tc.wantLabel)
			}
		})
	}
}

// TestEnum_StructuralErrorsKeepTheirCode pins that adding the enum gate did not
// rewrite the pre-existing required-field / shape envelope: those stay
// VALIDATION_ERROR so nothing branching on the old code breaks.
func TestEnum_StructuralErrorsKeepTheirCode(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type:     "object",
		Required: []string{"q"},
		Properties: map[string]registry.SchemaInfo{
			"q":     {Type: "string"},
			"items": {Type: "array", MinItems: 1, Items: &registry.SchemaInfo{Type: "string"}},
		},
	}
	v := bodySchemaValidator{}
	err := v.validate(schema, map[string]any{}, "", "")
	if err == nil || err.Code != "VALIDATION_ERROR" {
		t.Fatalf("missing required field = %v, want VALIDATION_ERROR", err)
	}
	err = v.validate(schema, map[string]any{"q": "x", "items": []any{}}, "", "")
	if err == nil || err.Code != "VALIDATION_ERROR" {
		t.Fatalf("minItems violation = %v, want VALIDATION_ERROR", err)
	}
}

// TestEnum_DriveVocabulariesMatchTheBackend is the guard the enum gate needs to
// be safe: enforcing an enum locally is only correct if the spec's set is not
// NARROWER than what the backend accepts, otherwise the CLI refuses a call that
// used to work and offers no bypass. These sets are transcribed from octo-drive
// source and must be updated together with it.
//
//	member/invite roles → models.AllRoles / invite.inviteRoleAllowed
//	blob source         → blob.allowedBlobSources
//	doc mount source    → docref.allowedMountSources
//	share permission    → share.CreateShare's permission switch
//
// This caught drive.doc.mount declaring only ["user-mount"] while the backend's
// allowedMountSources also accepts "docs-sync".
func TestEnum_DriveVocabulariesMatchTheBackend(t *testing.T) {
	reg := registry.MustNew()
	cases := []struct {
		op, field, in string
		want          []string
	}{
		{"drive.member.add", "role", "body", []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"}},
		{"drive.member.set-role", "role", "body", []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"}},
		{"drive.invite.create", "role", "body", []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"}},
		{"drive.blob.create", "source", "body", []string{"user-upload", "im-transfer"}},
		{"drive.doc.mount", "source", "body", []string{"user-mount", "docs-sync"}},
		{"drive.share.blob-create", "permission", "body", []string{"view", "download"}},
		{"drive.browse", "type", "query", []string{"all", "doc", "blob", "folder"}},
		{"drive.browse", "source", "query", []string{"all", "user-upload", "im-transfer", "user-mount", "docs-sync"}},
	}
	for _, tc := range cases {
		t.Run(tc.op+"/"+tc.field, func(t *testing.T) {
			d, ok := reg.GetOperation(tc.op)
			if !ok {
				t.Fatalf("operation %s not in the registry", tc.op)
			}
			var got []any
			if tc.in == "query" {
				p := findParam(d, tc.field, "query")
				if p == nil {
					t.Fatalf("%s has no query param %s", tc.op, tc.field)
				}
				got = p.Enum
			} else {
				if d.RequestBody == nil {
					t.Fatalf("%s has no request body", tc.op)
				}
				prop, exists := d.RequestBody.Properties[tc.field]
				if !exists {
					t.Fatalf("%s body has no field %s", tc.op, tc.field)
				}
				got = prop.Enum
			}
			gotStrs := make([]string, 0, len(got))
			for _, v := range got {
				s, ok := v.(string)
				if !ok {
					t.Fatalf("enum member %#v is not a string", v)
				}
				gotStrs = append(gotStrs, s)
			}
			sort.Strings(gotStrs)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(gotStrs, ",") != strings.Join(want, ",") {
				t.Errorf("%s %s enum = %v, want %v — the gate now rejects anything outside this set, so a narrower spec breaks a working call",
					tc.op, tc.field, gotStrs, want)
			}
		})
	}
}

// TestEnum_RejectedBeforeAnyRequest is the contract the E2E round found
// missing: the spec declared im_channel_type's enum, the CLI printed it in
// --help, and then forwarded 0 / 3 / 9 to the backend unchecked. Every rejected
// value must now fail locally with exit 2 and zero HTTP.
func TestEnum_RejectedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantInErr string
	}{
		{
			name:      "body int enum: im_channel_type 0",
			args:      []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "0", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantInErr: "--im-channel-type",
		},
		{
			name:      "body int enum: im_channel_type 3",
			args:      []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "3", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantInErr: "--im-channel-type",
		},
		{
			name:      "body int enum: im_channel_type 9",
			args:      []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "9", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantInErr: "--im-channel-type",
		},
		{
			name: "body int enum: im_channel_type -1 no longer leaks a backend decode error",
			args: []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "-1", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			// The backend used to answer with its internal struct name here.
			wantInErr: "--im-channel-type",
		},
		{
			name:      "body int enum via --data",
			args:      []string{"drive", "im-transfer", "create", "--data", `{"im_group_no":"g1","im_channel_type":9,"im_msg_id":"1","target_space_id":"shared:s1"}`},
			wantInErr: "--im-channel-type",
		},
		{
			name:      "body string enum: member role",
			args:      []string{"drive", "member", "add", "shared:s1", "--uid", "u1", "--role", "owner"},
			wantInErr: "--role",
		},
		{
			name:      "body string enum: share permission",
			args:      []string{"drive", "share", "blob-create", "--file-id", "1", "--permission", "edit"},
			wantInErr: "--permission",
		},
		{
			name:      "query string enum: browse type",
			args:      []string{"drive", "browse", "--space-id", "shared:s1", "--type", "video"},
			wantInErr: "--type",
		},
		{
			name:      "query string enum: browse source",
			args:      []string{"drive", "browse", "--space-id", "shared:s1", "--source", "ftp"},
			wantInErr: "--source",
		},
		{
			name:      "array item enum: docs search doc-type",
			args:      []string{"docs", "search", "--keyword", "q", "--doc-type", "slides"},
			wantInErr: "--doc-type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var backendHits atomic.Int32
			root, tf := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				backendHits.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v was accepted, want local validation failure", tc.args)
			}
			if hits := backendHits.Load(); hits != 0 {
				t.Errorf("request reached the backend %d time(s); enum checks must precede HTTP", hits)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should name %q", err, tc.wantInErr)
			}
			// The envelope carries the typed code and the allowed set, so an agent
			// can correct itself without parsing prose.
			stderr := tf.ErrOut.String()
			if !strings.Contains(stderr, enumNotAllowed) {
				t.Errorf("stderr envelope %q should carry %s", stderr, enumNotAllowed)
			}
			if !strings.Contains(stderr, "pass one of") {
				t.Errorf("stderr envelope %q should list the accepted values", stderr)
			}
		})
	}
}

// TestEnum_AcceptedValuesStillReachTheBackend is the other half of the gate:
// every value the spec does list must go through untouched, on the body, the
// query and a repeated array flag alike.
func TestEnum_AcceptedValuesStillReachTheBackend(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantPath string
		wantBody string
	}{
		{
			name:     "im_channel_type 1",
			args:     []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "1", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantPath: "/v1/bot/drive/blobs/transfer-from-im",
			wantBody: `{"im_channel_type":1,"im_group_no":"g1","im_msg_id":"1","target_space_id":"shared:s1"}`,
		},
		{
			name:     "im_channel_type 2",
			args:     []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "2", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantPath: "/v1/bot/drive/blobs/transfer-from-im",
			wantBody: `{"im_channel_type":2,"im_group_no":"g1","im_msg_id":"1","target_space_id":"shared:s1"}`,
		},
		{
			name:     "im_channel_type 5",
			args:     []string{"drive", "im-transfer", "create", "--im-group-no", "g1", "--im-channel-type", "5", "--im-msg-id", "1", "--target-space-id", "shared:s1"},
			wantPath: "/v1/bot/drive/blobs/transfer-from-im",
			wantBody: `{"im_channel_type":5,"im_group_no":"g1","im_msg_id":"1","target_space_id":"shared:s1"}`,
		},
		{
			name:     "im_channel_type 5 supplied as a --data number",
			args:     []string{"drive", "im-transfer", "create", "--data", `{"im_group_no":"g1","im_channel_type":5,"im_msg_id":"1","target_space_id":"shared:s1"}`},
			wantPath: "/v1/bot/drive/blobs/transfer-from-im",
			wantBody: `{"im_channel_type":5,"im_group_no":"g1","im_msg_id":"1","target_space_id":"shared:s1"}`,
		},
		{
			name:     "member role custom is accepted by the enum",
			args:     []string{"drive", "member", "add", "shared:s1", "--uid", "u1", "--role", "custom"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/members",
			wantBody: `{"role":"custom","uid":"u1"}`,
		},
		{
			name:     "repeated array flag with two accepted items",
			args:     []string{"docs", "search", "--keyword", "q", "--doc-type", "doc,sheet"},
			wantPath: "/v1/bot/docs/search",
			wantBody: `{"docType":["doc","sheet"],"q":"q"}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotBody string
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				gotBody = string(raw)
				w.WriteHeader(http.StatusNoContent)
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path: got %q, want %q", gotPath, tc.wantPath)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body:\n got %s\nwant %s", gotBody, tc.wantBody)
			}
		})
	}
}

// TestEnum_QueryAcceptedValuesReachTheBackend covers the query side of the gate
// separately, since query values travel in the URL rather than the body.
func TestEnum_QueryAcceptedValuesReachTheBackend(t *testing.T) {
	var gotQuery map[string]string
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = map[string]string{}
		for k, v := range r.URL.Query() {
			gotQuery[k] = v[0]
		}
		w.WriteHeader(http.StatusNoContent)
	})
	root.SetArgs([]string{"drive", "browse", "--space-id", "shared:s1", "--type", "blob", "--source", "im-transfer"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for k, want := range map[string]string{"type": "blob", "source": "im-transfer"} {
		if gotQuery[k] != want {
			t.Errorf("query %s = %q, want %q", k, gotQuery[k], want)
		}
	}
}

// TestEnum_UnsetFlagsAreNotChecked pins that the gate only inspects flags the
// caller actually set. This matters because a flag's Go zero value is often
// outside its own enum — an int enum flag defaults to 0 — so validating unset
// flags would reject every command that leaves an optional enum field alone.
//
// The end-to-end half asserts the query is EMPTY, not merely that the request
// went out: drive.browse declares `default: "all"` for both enum params, and
// "all" is itself a member of both enums, so a request reaching the backend
// proves nothing about whether unset flags were checked.
func TestEnum_UnsetFlagsAreNotChecked(t *testing.T) {
	var hits atomic.Int32
	var gotQuery url.Values
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotQuery = r.URL.Query()
		w.WriteHeader(http.StatusNoContent)
	})
	root.SetArgs([]string{"drive", "browse", "--space-id", "shared:s1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("backend hits = %d, want 1", hits.Load())
	}
	for _, unset := range []string{"type", "source", "parent_id"} {
		if got := gotQuery.Get(unset); got != "" {
			t.Errorf("unset flag %s was sent as %q; only Changed flags may reach the wire", unset, got)
		}
	}

	// The zero-value hazard itself, on an int enum with no spec default: an
	// unset flag must not be validated, and a set one must. No live spec op
	// pairs an int enum with an absent default today (matter and summary, which
	// do, are withheld behind x-octo-disabled), so drive it directly.
	intEnumParam := registry.ParamInfo{
		Name: "status", In: "query", Type: "integer",
		Enum: []any{float64(1), float64(2)},
	}
	rt := &operationRuntime{
		detail:     &registry.OperationDetail{Parameters: []registry.ParamInfo{intEnumParam}},
		queryFlags: map[string]*queryFlag{},
	}
	cmd := &cobra.Command{Use: "probe"}
	registerQueryFlags(cmd, rt, rt.detail)
	if err := cmd.Flags().Parse(nil); err != nil {
		t.Fatalf("parse no flags: %v", err)
	}
	q, exitErr := buildQuery(cmd, rt)
	if exitErr != nil {
		t.Fatalf("unset int enum flag was validated: %v", exitErr)
	}
	if len(q) != 0 {
		t.Errorf("unset int enum flag produced query %v, want none", q)
	}
	if err := cmd.Flags().Parse([]string{"--status", "0"}); err != nil {
		t.Fatalf("parse --status 0: %v", err)
	}
	if _, exitErr := buildQuery(cmd, rt); exitErr == nil {
		t.Error("--status 0 explicitly passed should be rejected: 0 is not in [1, 2]")
	} else if exitErr.Code != enumNotAllowed {
		t.Errorf("code = %s, want %s", exitErr.Code, enumNotAllowed)
	}
}

// TestEnum_RoleEnumStaysASupersetOfEachSurface pins the D2/D6 decision at the
// CLI layer. `custom` is grantable via member add / set-role but NOT via invite
// create, and `super_admin` is grantable via neither — three different accepted
// subsets behind one shared `Role` schema.
//
// The CLI must therefore NOT enforce the per-surface subset: the enum is the
// union, and the backend is the authority on which member of it each endpoint
// takes. Narrowing the shared enum to invite's subset would break `member add
// --role custom`, and narrowing it per operation would mean maintaining a copy
// of a backend rule in a spec, which is what produced the wrong "super_admin
// and custom cannot be granted" documentation in the first place.
func TestEnum_RoleEnumStaysASupersetOfEachSurface(t *testing.T) {
	// Every role the union accepts must reach the backend on every surface,
	// including the ones a given surface will reject server-side.
	cases := []struct {
		name     string
		args     []string
		wantPath string
		wantBody string
	}{
		{
			name:     "member add custom is allowed through",
			args:     []string{"drive", "member", "add", "shared:s1", "--uid", "u1", "--role", "custom"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/members",
			wantBody: `{"role":"custom","uid":"u1"}`,
		},
		{
			name:     "member set-role custom is allowed through",
			args:     []string{"drive", "member", "set-role", "shared:s1", "u1", "--role", "custom"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/members/u1",
			wantBody: `{"role":"custom"}`,
		},
		{
			// The backend answers invalid_argument here. That rejection is the
			// contract; the CLI must not pre-empt it with a local enum error,
			// or the two would have to be kept in sync forever.
			name:     "invite create custom reaches the backend rather than failing locally",
			args:     []string{"drive", "invite", "create", "shared:s1", "--role", "custom"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/invites",
			wantBody: `{"role":"custom"}`,
		},
		{
			name:     "super_admin also reaches the backend, which refuses it",
			args:     []string{"drive", "member", "add", "shared:s1", "--uid", "u1", "--role", "super_admin"},
			wantPath: "/v1/bot/drive/spaces/shared:s1/members",
			wantBody: `{"role":"super_admin","uid":"u1"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotBody string
			var hits atomic.Int32
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				gotPath = r.URL.Path
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read body: %v", err)
				}
				gotBody = string(raw)
				w.WriteHeader(http.StatusNoContent)
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if hits.Load() != 1 {
				t.Fatalf("backend hits = %d, want 1 — the CLI must not gate the per-surface subset", hits.Load())
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
			if gotBody != tc.wantBody {
				t.Errorf("body = %s, want %s", gotBody, tc.wantBody)
			}
		})
	}

	// And a value in NO surface's vocabulary is still refused locally, so the
	// superset is not a licence to forward anything.
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		t.Error("a role outside the shared enum must not reach the backend")
	})
	root.SetArgs([]string{"drive", "invite", "create", "shared:s1", "--role", "owner"})
	if err := root.Execute(); err == nil {
		t.Error("--role owner was accepted, want ENUM_NOT_ALLOWED")
	}
}
