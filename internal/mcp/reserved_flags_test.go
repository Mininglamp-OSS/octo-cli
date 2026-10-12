package mcp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// --- reserved-flag guard: tests that actually reach reservedFlagNames ---
//
// Every case here passes the earlier guards (the key is a DECLARED query /
// header / multipart property), so the only thing standing between the key and
// a synthesized engine flag is the reserved-flag check. Emptying
// reservedFlagNames turns each of these red.

// multipartUploadOps are the enabled operations whose spec declares a binary
// multipart "file" property, which derives the reserved --file flag.
var multipartUploadOps = []string{"file.upload", "html.asset.add", "attachment.upload"}

func requireDeclaredBodyProperty(t *testing.T, d *registry.OperationDetail, name string) {
	t.Helper()
	if !d.Multipart || d.RequestBody == nil {
		t.Fatalf("%s must be a multipart operation with a request body", d.ID)
	}
	if _, ok := d.RequestBody.Properties[name]; !ok {
		t.Fatalf("precondition: %s must declare body property %q so the declared-only guard does not fire first", d.ID, name)
	}
}

// TestReservedFlag_RealRegistryFileIsRefused uses the real registry: "file" is
// both declared and reserved, so without the reserved-flag guard
// {"file":"/etc/passwd"} becomes --file=/etc/passwd with no file_path, i.e. an
// unconfined local read in the default-deny posture.
func TestReservedFlag_RealRegistryFileIsRefused(t *testing.T) {
	reg := registry.MustNew()
	policies := map[string]execPolicy{
		"http default-deny": {httpMode: true},
		"http confined":     {httpMode: true, uploadRoot: t.TempDir()},
		"stdio default":     {},
		"stdio opt-in":      {allowLocalUpload: true},
	}
	for _, opID := range multipartUploadOps {
		d, ok := reg.GetOperation(opID)
		if !ok {
			t.Fatalf("operation %s missing from registry", opID)
		}
		requireDeclaredBodyProperty(t, d, "file")
		for name, pol := range policies {
			args := map[string]any{"file": "/etc/passwd"}
			for _, p := range extractPathParams(d.Path) {
				args[p] = "x"
			}
			argv, err := buildArgv(d, args, pol)
			if err == nil {
				t.Errorf("%s/%s: declared reserved property \"file\" must be refused, got argv %v", opID, name, argv)
				continue
			}
			if !strings.Contains(err.Error(), "reserved flag --file") || !strings.Contains(err.Error(), "file_path") {
				t.Errorf("%s/%s: error must name the reserved flag and point at file_path, got %v", opID, name, err)
			}
		}
	}
}

// TestReservedFlag_FilePathStillUploads is the legitimate direction on the same
// real operations: the confined MCP-only file_path argument is the one that
// produces --file, resolved inside the upload root.
func TestReservedFlag_FilePathStillUploads(t *testing.T) {
	reg := registry.MustNew()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, opID := range multipartUploadOps {
		d, _ := reg.GetOperation(opID)
		args := map[string]any{"file_path": "a.txt"}
		for _, p := range extractPathParams(d.Path) {
			args[p] = "x"
		}
		for _, httpMode := range []bool{false, true} {
			argv, err := buildArgv(d, args, execPolicy{httpMode: httpMode, uploadRoot: root})
			if err != nil {
				t.Fatalf("%s httpMode=%v: confined file_path must be accepted: %v", opID, httpMode, err)
			}
			var files []string
			for _, a := range argv {
				if strings.HasPrefix(a, "--file=") {
					files = append(files, a)
				}
			}
			if len(files) != 1 || !strings.HasSuffix(files[0], string(os.PathSeparator)+"a.txt") {
				t.Errorf("%s httpMode=%v: want exactly one confined --file, argv=%v", opID, httpMode, argv)
			}
		}
	}
}

// TestReservedFlag_EveryReservedNameRefusedInEveryPosition pins each entry of
// the map independently in all three positions the guard protects: a declared
// multipart property, a declared query parameter, and a declared header
// parameter whose flag_name collides. No live spec has a query/header
// collision today, so these synthetic operations are the only pin for those
// branches.
func TestReservedFlag_EveryReservedNameRefusedInEveryPosition(t *testing.T) {
	if len(reservedFlagNames) == 0 {
		t.Fatal("reservedFlagNames must not be empty")
	}
	for _, name := range []string{"format", "profile", "bot-id", "space", "data", "file", "verbose", "dry-run", "help"} {
		if !reservedFlagNames[name] {
			t.Errorf("%q must be a reserved flag name", name)
		}
	}
	names := make([]string, 0, len(reservedFlagNames))
	for n := range reservedFlagNames {
		names = append(names, n)
	}
	sort.Strings(names)
	pol := execPolicy{allowLocalUpload: true}
	for _, fn := range names {
		key := strings.ReplaceAll(fn, "-", "_")
		cases := map[string]*registry.OperationDetail{
			"multipart": {
				OperationInfo: registry.OperationInfo{ID: "synthetic.upload", Method: "POST", Path: "/v1/x"},
				Multipart:     true,
				RequestBody:   &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{key: {Type: "string"}}},
			},
			"query": {
				OperationInfo: registry.OperationInfo{ID: "synthetic.query", Method: "GET", Path: "/v1/x"},
				Parameters:    []registry.ParamInfo{{Name: key, In: "query", Type: "string"}},
			},
			"header": {
				OperationInfo: registry.OperationInfo{ID: "synthetic.header", Method: "GET", Path: "/v1/x"},
				Parameters:    []registry.ParamInfo{{Name: "X-Synthetic", In: "header", Type: "string", FlagName: fn}},
			},
		}
		if key == "page_all" {
			// page_all is the MCP pagination argument, consumed before any
			// position lookup; only the header flag_name collision can reach
			// the guard for --page-all.
			delete(cases, "multipart")
			delete(cases, "query")
		}
		for pos, d := range cases {
			argKey := key
			if pos == "header" {
				argKey = "X-Synthetic"
			}
			argv, err := buildArgv(d, map[string]any{argKey: "evil"}, pol)
			if err == nil || !strings.Contains(err.Error(), "reserved flag --"+fn) {
				t.Errorf("%s %q: must be refused by the reserved-flag guard, argv=%v err=%v", pos, fn, argv, err)
			}
		}
	}
	// Control: a non-reserved declared query/header still maps to its flag, so
	// the refusals above are the guard and not a blanket rejection.
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "synthetic.ok", Method: "GET", Path: "/v1/x"},
		Parameters: []registry.ParamInfo{
			{Name: "limit", In: "query", Type: "string"},
			{Name: "X-Workspace-ID", In: "header", Type: "string", FlagName: "workspace-id"},
		},
	}
	argv, err := buildArgv(d, map[string]any{"limit": "5", "X-Workspace-ID": "w"}, pol)
	if err != nil || !hasArg(argv, "--limit=5") || !hasArg(argv, "--workspace-id=w") {
		t.Errorf("non-reserved query/header must map to flags, argv=%v err=%v", argv, err)
	}
}

// TestReservedFlag_CallOpFileNeverReadsOutsideRoot drives call_op end to end on
// both transports: a model that supplies the raw "file" property with an
// outside canary is refused before any request is sent.
func TestReservedFlag_CallOpFileNeverReadsOutsideRoot(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(canary, []byte("OUTSIDE-CANARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			hits := 0
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"file_id":"1"}`))
			}))
			defer backend.Close()
			t.Setenv("OCTO_API_BASE_URL", backend.URL)
			args := map[string]any{"operation_id": "file.upload", "arguments": map[string]any{"file": canary}}
			res := uploadRPC(t, transport, backend.URL, uploadRPCConfig{root: t.TempDir()}, "call_op", args)
			if !res.IsError {
				t.Fatalf("raw file argument must be refused: %s", res.Content[0].Text)
			}
			text := res.Content[0].Text
			if !strings.Contains(text, "reserved flag --file") || !strings.Contains(text, "file_path") {
				t.Errorf("refusal must name the reserved flag and the file_path alternative: %s", text)
			}
			if hits != 0 {
				t.Errorf("no request may reach the backend, got %d", hits)
			}
		})
	}
}

// --- reverse authz-table consistency ---

// TestAuthzTable_EveryForcedFieldIsDeclared is the reverse of the coverage
// guard: every argument in overridableParams must be a field its operation
// actually declares (path, query, header, or body). A typo'd or stale key would
// otherwise force nothing and fail open silently.
func TestAuthzTable_EveryForcedFieldIsDeclared(t *testing.T) {
	reg := registry.MustNew()
	for opID, table := range overridableParams {
		d, ok := reg.GetOperation(opID)
		if !ok {
			t.Errorf("overridableParams references unknown operation %q", opID)
			continue
		}
		if reg.ServiceDisabled(d.Service) {
			t.Errorf("overridableParams references operation %q of a disabled service", opID)
		}
		if _, div := divergentMCPOps[opID]; div {
			t.Errorf("overridableParams references divergent (uncallable) operation %q", opID)
		}
		declared := declaredArgumentNames(d)
		for field := range table {
			if !declared[field] {
				t.Errorf("overridableParams[%q][%q] is not declared by the operation; forcing it would be a silent no-op", opID, field)
			}
		}
	}
}

func declaredArgumentNames(d *registry.OperationDetail) map[string]bool {
	out := map[string]bool{}
	for _, p := range extractPathParams(d.Path) {
		out[p] = true
	}
	for i := range d.Parameters {
		out[d.Parameters[i].Name] = true
	}
	if d.RequestBody != nil {
		for name := range d.RequestBody.Properties {
			out[name] = true
		}
	}
	return out
}

// TestAuthzTable_ReverseGuardDetectsTypo proves the reverse guard is not
// vacuous: a misspelled forced field is reported as undeclared.
func TestAuthzTable_ReverseGuardDetectsTypo(t *testing.T) {
	d, ok := registry.MustNew().GetOperation("message.send")
	if !ok {
		t.Fatal("message.send missing")
	}
	declared := declaredArgumentNames(d)
	if !declared["channel_id"] {
		t.Fatal("message.send must declare channel_id")
	}
	if declared["chanel_id"] {
		t.Fatal("typo must not be treated as declared")
	}
}
