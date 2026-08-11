package service

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Flag names are derived from spec metadata, so a spec edit — the documented way
// to add an endpoint — can collide two flags onto one name. pflag panics on a
// redefined flag, and because the entire command tree is built in
// RegisterServiceCommands, that panic kills every command including
// `octo-cli version`. The engine defends by refusing the colliding name, which
// silently drops the spec's flag; these tests make the collision a test failure
// instead, at the only point where it can still be reported.

// TestFlags_NoSpecCollidesWithEngineFlags asserts no operation declares a
// parameter or promoted body field whose flag name is one the engine reserves
// for itself (--data, --file, the pagination flags, --output/-o).
func TestFlags_NoSpecCollidesWithEngineFlags(t *testing.T) {
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if p.In == "path" && p.FlagName == "" {
					continue // positional only, registers no flag
				}
				name := paramFlagName(p)
				if reservedFlagNames[name] {
					t.Errorf("%s: %s param %q would register --%s, which the engine reserves; "+
						"the flag is dropped at runtime — rename it with x-octo-flag",
						info.ID, p.In, p.Name, name)
				}
			}
			if d.RequestBody == nil {
				continue
			}
			for field, prop := range d.RequestBody.Properties {
				// Binary fields are the multipart part itself: registerBodyFlags
				// skips them and the engine's own --file carries them, so
				// `"file": {"format": "binary"}` is the intended shape, not a
				// collision (file.upload, html.asset.add).
				if prop.Format == "binary" {
					continue
				}
				if _, promotable := promotableKind(&prop); !promotable {
					continue
				}
				name := prop.FlagName
				if name == "" {
					name = strings.ReplaceAll(field, "_", "-")
				}
				if reservedFlagNames[name] {
					t.Errorf("%s: body field %q would register --%s, which the engine reserves; "+
						"the flag is dropped at runtime — rename it with x-octo-flag",
						info.ID, field, name)
				}
			}
		}
	}
}

// TestFlags_NoDuplicatePathFlagNames asserts no operation gives two path
// parameters the same x-octo-flag. The second one loses its flag alternative,
// which is exactly the escape hatch a base64url id needs, while the first
// advertises a flag the caller did not mean.
func TestFlags_NoDuplicatePathFlagNames(t *testing.T) {
	reg := registry.MustNew()
	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			seen := map[string]string{}
			for _, name := range extractPathParams(d.Path) {
				p := findParam(d, name, "path")
				if p == nil || p.FlagName == "" {
					continue
				}
				if prev, dup := seen[p.FlagName]; dup {
					t.Errorf("%s: path params %q and %q both declare x-octo-flag %q; "+
						"%q silently loses its flag form", info.ID, prev, name, p.FlagName, name)
				}
				seen[p.FlagName] = name
			}
		}
	}
}

// TestFlags_PathFlagCollisionDoesNotPanic drives the guard directly: a path
// param whose x-octo-flag is engine-reserved, or duplicated within one
// operation, must leave the command usable rather than panic pflag. Both cases
// panicked before the guard, taking the whole binary with them.
func TestFlags_PathFlagCollisionDoesNotPanic(t *testing.T) {
	newDetail := func(flags ...string) *registry.OperationDetail {
		d := &registry.OperationDetail{
			OperationInfo: registry.OperationInfo{
				ID: "probe.op", Service: "probe", Method: "DELETE",
				Path: "/v1/probe/{first}/{second}",
			},
			Parameters: []registry.ParamInfo{
				{Name: "first", In: "path", Required: true, Type: "string"},
				{Name: "second", In: "path", Required: true, Type: "string"},
			},
			// A body makes the engine register --data, the reserved name the
			// first case collides with.
			RequestBody: &registry.SchemaInfo{Type: "object"},
		}
		for i, f := range flags {
			if i < len(d.Parameters) {
				d.Parameters[i].FlagName = f
			}
		}
		return d
	}

	t.Run("engine-reserved name", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("flag registration panicked: %v", r)
			}
		}()
		cmd := buildOperationCmd(nil, newDetail("data"), "op")
		// --data must still be the engine's body flag. Its usage string is the
		// engine's; a path flag would have replaced it with "alternative to the
		// positional <first>".
		f := cmd.Flags().Lookup("data")
		if f == nil {
			t.Fatal("--data disappeared; the engine's own body flag must survive")
		}
		if strings.Contains(f.Usage, "alternative to the positional") {
			t.Errorf("--data was claimed by the path param: %q", f.Usage)
		}
		// The colliding param keeps working positionally: two slots, no flag.
		if cmd.Flags().Lookup("first") != nil {
			t.Error("the colliding path param should register no flag at all")
		}
	})

	t.Run("duplicate within one operation", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("flag registration panicked: %v", r)
			}
		}()
		cmd := buildOperationCmd(nil, newDetail("dup", "dup"), "op")
		if cmd.Flags().Lookup("dup") == nil {
			t.Error("the first path param should still own --dup")
		}
	})
}

// TestFlags_QueryAndHeaderCollisionDoesNotPanic covers the other two
// registration paths. The guard was originally added to the path and body paths
// only, leaving a query or header param named after an engine flag able to panic
// pflag at startup — which kills every command, `octo-cli version` included,
// because the whole tree is built in RegisterServiceCommands. Without this test
// only the registry-wide spec scan would notice a revert, and that pairing is
// what let the gap through the first time.
func TestFlags_QueryAndHeaderCollisionDoesNotPanic(t *testing.T) {
	// Each case names an engine flag and the operation shape that registers it,
	// since the pagination and --output flags register later than the rest.
	cases := []struct {
		flag       string
		paginated  bool
		binaryBody bool
	}{
		{flag: "data"},
		{flag: "page-all", paginated: true},
		{flag: "page-limit", paginated: true},
		{flag: "output", binaryBody: true},
	}
	for _, in := range []string{"query", "header"} {
		for _, tc := range cases {
			t.Run(in+"/"+tc.flag, func(t *testing.T) {
				d := &registry.OperationDetail{
					OperationInfo: registry.OperationInfo{
						ID: "probe.op", Service: "probe", Method: "POST",
						Path: "/v1/probe",
					},
					Parameters: []registry.ParamInfo{
						{Name: tc.flag, In: in, Type: "string"},
					},
					RequestBody: &registry.SchemaInfo{Type: "object"},
					BinaryBody:  tc.binaryBody,
				}
				if tc.paginated {
					d.Pagination = &registry.PaginationInfo{}
				}
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("flag registration panicked: %v", r)
					}
				}()
				cmd := buildOperationCmd(nil, d, "op")
				// The engine's own flag must survive; the spec's colliding one is
				// the one that loses.
				if cmd.Flags().Lookup(tc.flag) == nil {
					t.Fatalf("--%s disappeared; the engine's own flag must survive", tc.flag)
				}
			})
		}
	}
}
