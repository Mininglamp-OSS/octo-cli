package service

import (
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- reserved flag names ---

// TestFlags_GlobalFlagNamesAreReserved asserts every global flag name is
// actually in the set the four registration paths consult. The companion test in
// package cmd checks the list still matches the flags root registers; this one
// checks the list is wired to the guard at all, which is the half a
// list-versus-root comparison cannot see.
func TestFlags_GlobalFlagNamesAreReserved(t *testing.T) {
	for _, name := range ReservedGlobalFlagNames() {
		if !reservedFlagNames[name] {
			t.Errorf("--%s is listed as a reserved global but the guard does not consult it; "+
				"a spec param of that name would silently shadow the global for its leaf", name)
		}
	}
	for _, name := range engineFlagNames {
		if !reservedFlagNames[name] {
			t.Errorf("--%s is an engine flag but the guard does not consult it; "+
				"a spec param of that name would panic pflag at startup", name)
		}
	}
	// The `-q` shorthand of `--jq` must stay out: pflag keeps shorthands in a
	// separate namespace, so a spec param named `q` (matter.list,
	// marketplace skill.list) does not shadow it and must remain registrable.
	if reservedFlagNames["q"] {
		t.Error(`"q" must not be reserved: it is --jq's shorthand, and matter.list / skill.list ` +
			`declare a query param named q that would lose its flag`)
	}
}

// --- dot segments in path values ---

// TestPathSegments_DotSegmentsRejected covers the gap that let a dot segment
// reach the URL: url.PathEscape leaves "." and ".." untouched, so
// `share revoke --share-id ..` produced DELETE /v1/user/drive/shares/.. and any
// gateway that normalises dot segments would have turned that into a DELETE
// against the collection.
func TestPathSegments_DotSegmentsRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"dot-dot via a path flag on a DELETE", []string{"drive", "share", "revoke", "--share-id", ".."}},
		{"single dot via a path flag on a DELETE", []string{"drive", "share", "revoke", "--share-id", "."}},
		{"dot-dot as a positional", []string{"drive", "space", "get", ".."}},
		{"single dot as a positional", []string{"drive", "space", "get", "."}},
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
				t.Error("a dot-segment path value must not reach the backend")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
				t.Errorf("taxonomy: got %v, want a validation error", err)
			}
		})
	}
}

// TestPathSegments_DotsInsideAnIDStillWork guards the narrowness of the check:
// a dot is a legal character inside an Octo id, so only a segment that is
// entirely "." or ".." may be refused.
func TestPathSegments_DotsInsideAnIDStillWork(t *testing.T) {
	var gotPath string
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"id":"a.b"}`))
	})
	root.SetArgs([]string{"drive", "space", "get", "a.b..c"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := "/v1/bot/drive/spaces/a.b..c"; gotPath != want {
		t.Errorf("path: got %q, want %q", gotPath, want)
	}
}

// --- pagination × output transforms ---

// TestPagination_TransformConflictFailsLoud drives the guard with a synthetic
// operation, since no embedded spec pairs the two extensions. runPaginated
// merges raw per-page items and emits them without normalizeResponse, so an
// operation declaring both would answer the same call with two different
// response contracts depending on --page-all.
func TestPagination_TransformConflictFailsLoud(t *testing.T) {
	cases := []struct {
		name   string
		detail *registry.OperationDetail
	}{
		{
			name: "response field aliases",
			detail: &registry.OperationDetail{
				OperationInfo: registry.OperationInfo{ID: "probe.list", Service: "probe", Method: "GET", Path: "/v1/probe"},
				Pagination:    &registry.PaginationInfo{},
				ResponseFieldAliases: map[string][]string{
					"id": {"share_id"},
				},
			},
		},
		{
			name: "lossless id fields",
			detail: &registry.OperationDetail{
				OperationInfo:    registry.OperationInfo{ID: "probe.list", Service: "probe", Method: "GET", Path: "/v1/probe"},
				Pagination:       &registry.PaginationInfo{},
				LosslessIDFields: []string{"id"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePaginationTransforms(&operationRuntime{detail: tc.detail})
			if err == nil {
				t.Fatal("expected PAGINATION_TRANSFORM_CONFLICT")
			}
			if err.Code != "PAGINATION_TRANSFORM_CONFLICT" {
				t.Errorf("code: got %q, want PAGINATION_TRANSFORM_CONFLICT", err.Code)
			}
		})
	}
	// A paginated op declaring neither transform is the shape every real spec
	// uses and must stay usable.
	plain := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "probe.list", Service: "probe", Method: "GET", Path: "/v1/probe"},
		Pagination:    &registry.PaginationInfo{},
	}
	if err := validatePaginationTransforms(&operationRuntime{detail: plain}); err != nil {
		t.Errorf("a paginated op with no transform must be allowed, got %v", err)
	}
}

// TestPagination_NoSpecPairsWithOutputTransforms is the development-time half:
// the runtime guard turns the combination into an error, and this turns it into
// a build failure at the moment a spec author writes it.
func TestPagination_NoSpecPairsWithOutputTransforms(t *testing.T) {
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok || d.Pagination == nil {
				continue
			}
			if len(d.ResponseFieldAliases) > 0 || len(d.LosslessIDFields) > 0 {
				t.Errorf("%s declares x-octo-pagination together with an output transform; "+
					"--page-all bypasses normalizeResponse, so the two output paths would diverge",
					info.ID)
			}
		}
	}
}

// --- enum coverage boundary ---

// TestEnum_NoHeaderOrPathParamDeclaresEnum records where the local enum gate
// stops. checkEnum runs on query and body parameters; buildHeaders and
// validatePathArgs do not call it. That is deliberate only for as long as no
// spec declares such an enum — this test is the tripwire, and its failure means
// the asymmetry has to be closed rather than documented.
func TestEnum_NoHeaderOrPathParamDeclaresEnum(t *testing.T) {
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if p.In != "header" && p.In != "path" {
					continue
				}
				if len(p.Enum) > 0 || (p.Items != nil && len(p.Items.Enum) > 0) {
					t.Errorf("%s: %s param %q declares an enum, but the local enum gate covers "+
						"query and body only — extend buildHeaders / validatePathArgs before shipping it",
						info.ID, p.In, p.Name)
				}
			}
		}
	}
}

// --- enum numbers compare by exact wire text ---

// TestEnum_NonIntegralNumbersDoNotSatisfyAnIntegerVocabulary closes a gap in the
// canonical comparison: it used to fall back to ParseFloat and collapse any value
// equal to its own truncation to an integer, so `1.0` matched the entry `1`,
// passed the gate, and was then sent verbatim — the body keeps the caller's
// original json.Number. A Go int field rejects `1.0` on decode, so the request
// failed at the backend with exactly the internal-struct-name error the local
// gate exists to prevent, on a value the gate had just called valid.
//
// `1.0` is not exotic: it is what a jq expression or any JSON-producing agent
// emits for an arithmetic result.
func TestEnum_NonIntegralNumbersDoNotSatisfyAnIntegerVocabulary(t *testing.T) {
	cases := []struct {
		value string
		// accepted reports whether the value should satisfy the vocabulary.
		accepted bool
	}{
		{"1", true},
		{"2", true},
		{"5", true},
		{"1.0", false},
		{"1e0", false},
		{"1E0", false},
		{"1.00000000000000000001", false},
		{"9", false}, // control: outside the vocabulary entirely
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			var called bool
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
				_, _ = w.Write([]byte(`{}`))
			})
			data := `{"im_group_no":"g","im_channel_type":` + tc.value +
				`,"im_msg_id":"m","target_space_id":"s"}`
			root.SetArgs([]string{"drive", "im-transfer", "create", "--data", data})
			err := root.Execute()
			if tc.accepted {
				if err != nil {
					t.Fatalf("%s is an integer in the vocabulary and must be accepted: %v", tc.value, err)
				}
				if !called {
					t.Error("an accepted value must still reach the backend")
				}
				return
			}
			if err == nil {
				t.Fatalf("%s is not an integer on the wire and must be refused", tc.value)
			}
			if called {
				t.Error("a refused value must not reach the backend")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Code != "ENUM_NOT_ALLOWED" {
				t.Errorf("code: got %v, want ENUM_NOT_ALLOWED", err)
			}
		})
	}
}

// --- empty path values ---

// TestPathSegments_EmptyPathValueRejected asserts an empty path value is refused
// on the positional form as well as the flag form.
//
// The check used to live in resolvePathValues and covered only flag-supplied
// values, while `drive share revoke "$SHARE_ID"` with an unset variable — the
// spelling the help text leads with — sent DELETE to the collection URL. Both
// spellings now fail, for every path param in every domain, on the same reasoning
// the dot-segment refusal uses: whether the backend routes a DELETE at the
// collection is a question the CLI should not be asking.
func TestPathSegments_EmptyPathValueRejected(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"empty positional on a DELETE", []string{"drive", "share", "revoke", ""}},
		{"empty path flag on a DELETE", []string{"drive", "share", "revoke", "--share-id", ""}},
		{"empty positional on a GET", []string{"drive", "space", "get", ""}},
		{"empty positional in a two-slot path", []string{"drive", "folder", "list", "s1", ""}},
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
				t.Errorf("an empty path value must not reach the backend")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
				t.Errorf("taxonomy: got %v, want a validation error", err)
			}
		})
	}
}
