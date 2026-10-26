package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// XIN-2424: approved Bot API coverage for the MCP facades. These tests close
// the loop between the coverage manifest (docs/mcp-bot-api-coverage.json), the
// MCP exposure table (exposure.json) and the embedded registry, and exercise
// the new operations through execute against isolated fake backends.

type coverageManifest struct {
	Summary struct {
		Rows          int            `json:"rows"`
		ByOriginalGap map[string]int `json:"by_original_gap"`
	} `json:"summary"`
	Rows []struct {
		RowID        string   `json:"row_id"`
		Method       string   `json:"method"`
		Path         string   `json:"path"`
		OriginalGap  string   `json:"original_gap"`
		FinalStatus  string   `json:"final_status"`
		OperationIDs []string `json:"operation_ids"`
		Evidence     string   `json:"source_evidence"`
		Notes        string   `json:"notes"`
		Tests        []string `json:"tests"`
	} `json:"rows"`
	Excluded []struct {
		RowID  string `json:"row_id"`
		Method string `json:"method"`
		Path   string `json:"path"`
	} `json:"excluded_rows"`
}

func loadCoverageManifest(t *testing.T) coverageManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "mcp-bot-api-coverage.json"))
	if err != nil {
		t.Fatalf("read coverage manifest: %v", err)
	}
	var m coverageManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode coverage manifest: %v", err)
	}
	return m
}

// xin2424ApprovedRows is the exact approved row set: 105 true-missing Bot
// wrapper rows, 10 partial/conditional rows and 3 alias-only rows from the
// XIN-2419 report (section 9). Listing it here, rather than deriving it from
// the manifest, means a row dropped from the manifest fails the test.
var xin2424ApprovedRows = map[string][]string{
	"true_missing": {
		"core#10", "core#11", "core#12", "core#14", "core#22", "core#23", "core#25", "core#26", "core#27", "core#28", "core#29",
		"core#41", "core#42", "core#43", "core#44", "core#45", "core#46", "core#47", "core#48", "core#49", "core#50", "core#51",
		"core#66", "core#72", "core#73", "core#74", "core#75",
		"docs#1", "docs#2", "docs#6", "docs#8", "docs#11", "docs#12", "docs#13", "docs#15", "docs#25", "docs#26", "docs#27",
		"docs#28", "docs#29", "docs#30", "docs#31", "docs#32", "docs#34", "docs#37", "docs#38", "docs#39", "docs#40", "docs#41",
		"docs#42", "docs#43", "docs#44", "docs#58", "docs#63", "docs#73", "docs#75", "docs#76", "docs#77", "docs#78",
		"html#85", "html#86", "html#87", "html#101", "html#102", "html#103", "html#107", "html#108", "html#109", "html#110",
		"html#111", "html#112", "html#113",
		"loop#90", "loop#91", "loop#92", "loop#97", "loop#99", "loop#101", "loop#103", "loop#122", "loop#125", "loop#126",
		"loop#127", "loop#128", "loop#160", "loop#162", "loop#163",
		"drive#57", "drive#59", "drive#61", "drive#75", "drive#81", "drive#83", "drive#93", "drive#94", "drive#95", "drive#96",
		"drive#98", "drive#99", "drive#100", "drive#102", "drive#103", "drive#104", "drive#105",
		"summary#1",
	},
	"partial_or_conditional": {"core#16", "core#37", "core#38", "docs#5", "docs#74", "html#96", "loop#33", "loop#41", "loop#44", "loop#45"},
	"alias_only":             {"core#17", "core#30", "core#31"},
}

var routeMatchedStatuses = map[string]bool{
	"implemented": true, "exposed": true, "fixed": true, "already_covered_upstream": true, "documented_config_requirement": true,
}

var knownFinalStatuses = map[string]bool{
	"implemented": true, "exposed": true, "fixed": true, "already_covered_upstream": true, "documented_config_requirement": true,
	"already_covered": true, "alias_of": true, "covered_by_equivalent": true, "discrepancy_not_implemented": true,
}

// routeMatches compares a report route with a registry path segment by
// segment. A report placeholder matches any registry segment (html#111 lists
// /{kind}, which the registry splits into the export and fork literals); a
// literal must match exactly.
func routeMatches(reportPath, opPath string) bool {
	rp := strings.Split(strings.Trim(reportPath, "/"), "/")
	op := strings.Split(strings.Trim(opPath, "/"), "/")
	if len(rp) != len(op) {
		return false
	}
	for i := range rp {
		if strings.HasPrefix(rp[i], "{") && strings.HasSuffix(rp[i], "}") {
			continue
		}
		if strings.HasPrefix(op[i], "{") && strings.HasSuffix(op[i], "}") {
			return false
		}
		if rp[i] != op[i] {
			return false
		}
	}
	return true
}

func TestXIN2424CoverageManifestRowsResolve(t *testing.T) {
	m := loadCoverageManifest(t)
	reg := registry.MustNew()

	want := map[string]string{}
	for gap, ids := range xin2424ApprovedRows {
		for _, id := range ids {
			want[id] = gap
		}
	}
	if got := len(xin2424ApprovedRows["true_missing"]); got != 105 {
		t.Fatalf("approved true-missing rows = %d, want 105", got)
	}
	if got := len(xin2424ApprovedRows["partial_or_conditional"]); got != 10 {
		t.Fatalf("approved partial rows = %d, want 10", got)
	}
	if m.Summary.Rows != 118 || len(m.Rows) != 118 {
		t.Fatalf("manifest rows = %d/%d, want 118", m.Summary.Rows, len(m.Rows))
	}

	seen := map[string]bool{}
	testFuncs := map[string]bool{}
	for _, row := range m.Rows {
		if seen[row.RowID] {
			t.Errorf("%s: duplicate manifest row", row.RowID)
		}
		seen[row.RowID] = true
		if gap, ok := want[row.RowID]; !ok {
			t.Errorf("%s: not an approved row", row.RowID)
		} else if gap != row.OriginalGap {
			t.Errorf("%s: original gap %q, want %q", row.RowID, row.OriginalGap, gap)
		}
		if !knownFinalStatuses[row.FinalStatus] {
			t.Errorf("%s: unknown final status %q", row.RowID, row.FinalStatus)
		}
		if strings.TrimSpace(row.Evidence) == "" {
			t.Errorf("%s: no source evidence", row.RowID)
		}
		if row.FinalStatus == "discrepancy_not_implemented" {
			if len(row.OperationIDs) != 0 || row.Notes == "" {
				t.Errorf("%s: a discrepancy row must carry no operation and explain why", row.RowID)
			}
			continue
		}
		if len(row.OperationIDs) == 0 {
			t.Errorf("%s: %s row names no operation", row.RowID, row.FinalStatus)
		}
		matched := false
		for _, id := range row.OperationIDs {
			d, ok := reg.GetOperation(id)
			if !ok {
				t.Errorf("%s: operation %q not in the registry", row.RowID, id)
				continue
			}
			if reg.ServiceDisabled(d.Service) {
				t.Errorf("%s: operation %q belongs to a disabled service", row.RowID, id)
			}
			if !mcpExposed(id) {
				t.Errorf("%s: operation %q is not classified for MCP exposure", row.RowID, id)
			}
			if strings.EqualFold(d.Method, row.Method) && routeMatches(row.Path, d.Path) {
				matched = true
			}
		}
		if routeMatchedStatuses[row.FinalStatus] && !matched {
			t.Errorf("%s: no operation in %v serves %s %s", row.RowID, row.OperationIDs, row.Method, row.Path)
		}
		if len(row.Tests) == 0 {
			t.Errorf("%s: no test reference", row.RowID)
		}
		for _, ref := range row.Tests {
			testFuncs[ref] = true
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: approved row missing from the manifest", id)
		}
	}

	// Every test reference must name a real file and, when given, a real test.
	for ref := range testFuncs {
		file, fn, _ := strings.Cut(ref, ":")
		src, err := os.ReadFile(filepath.Join("..", "..", file))
		if err != nil {
			t.Errorf("test reference %q: %v", ref, err)
			continue
		}
		if fn == "" {
			continue
		}
		fn, _, _ = strings.Cut(fn, "/")
		if !bytes.Contains(src, []byte("func "+fn+"(")) {
			t.Errorf("test reference %q: %s has no func %s", ref, file, fn)
		}
	}
}

// TestXIN2424ExcludedRowsStayUnexposed pins the exclusions: no operation added
// or exposed by this work serves a route the report excluded (implicit owner,
// legacy compatibility, user-key-only, Bot-rejected or non-Octo rows).
func TestXIN2424ExcludedRowsStayUnexposed(t *testing.T) {
	m := loadCoverageManifest(t)
	if len(m.Excluded) != 151 {
		t.Fatalf("excluded rows = %d, want 151 (52 implicit owner + 35 legacy + 64 removed)", len(m.Excluded))
	}
	reg := registry.MustNew()
	var f exposureFile
	if err := json.Unmarshal(exposureJSON, &f); err != nil {
		t.Fatal(err)
	}
	for id := range f.Approved {
		d, ok := reg.GetOperation(id)
		if !ok {
			t.Errorf("approved operation %q not in the registry", id)
			continue
		}
		for _, ex := range m.Excluded {
			if strings.EqualFold(ex.Method, d.Method) && routeMatches(ex.Path, d.Path) {
				t.Errorf("approved operation %q serves excluded row %s (%s %s)", id, ex.RowID, ex.Method, ex.Path)
			}
		}
	}
	if !reg.ServiceDisabled("summary") {
		t.Error("the summary service must stay disabled: its approved row is an open discrepancy and its other rows are excluded")
	}
	for _, op := range reg.EnabledOperations() {
		for _, prefix := range []string{"/v1/robots/", "/v1/bot/voice/", "/v1/bot/secrets/"} {
			if strings.HasPrefix(op.Path, prefix) {
				t.Errorf("%s serves excluded route family %s", op.ID, prefix)
			}
		}
	}
}

// TestExposure_EveryEnabledOperationIsClassified is the default-deny guard: an
// enabled registry operation that is neither in the baseline nor approved
// would be refused by MCP, so CI fails until it is classified on purpose.
func TestExposure_EveryEnabledOperationIsClassified(t *testing.T) {
	reg := registry.MustNew()
	var f exposureFile
	if err := json.Unmarshal(exposureJSON, &f); err != nil {
		t.Fatalf("exposure.json: %v", err)
	}
	enabled := map[string]bool{}
	for _, op := range reg.EnabledOperations() {
		enabled[op.ID] = true
		if !mcpExposed(op.ID) {
			t.Errorf("enabled operation %q is not classified for MCP exposure", op.ID)
		}
	}
	for _, id := range f.Baseline {
		if !enabled[id] {
			t.Errorf("baseline operation %q is not an enabled registry operation", id)
		}
		if _, dup := f.Approved[id]; dup {
			t.Errorf("operation %q is both baseline and approved", id)
		}
	}
	m := loadCoverageManifest(t)
	rowsByOp := map[string][]string{}
	for _, row := range m.Rows {
		for _, id := range row.OperationIDs {
			rowsByOp[id] = append(rowsByOp[id], row.RowID)
		}
	}
	for id, rows := range f.Approved {
		if !enabled[id] {
			t.Errorf("approved operation %q is not an enabled registry operation", id)
		}
		got := append([]string(nil), rowsByOp[id]...)
		sort.Strings(got)
		if strings.Join(got, ",") != strings.Join(rows, ",") {
			t.Errorf("approved operation %q rows = %v, manifest says %v", id, rows, got)
		}
	}
}

func TestExposure_UnclassifiedOperationIsDenied(t *testing.T) {
	const opID = "bot.card-profile"
	saved := exposedOps
	exposedOps = map[string]bool{}
	for id, v := range saved {
		if id != opID {
			exposedOps[id] = v
		}
	}
	defer func() { exposedOps = saved }()

	s := facadeServer(t, facadeBoth)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	search := toolText(t, callTool(t, s, "search_ops", map[string]any{"domain": "bot"}))
	for _, o := range search["operations"].([]any) {
		if o.(map[string]any)["id"] == opID {
			t.Fatalf("unclassified %s must not be advertised", opID)
		}
	}
	for _, tool := range []string{"describe_op", "call_op", "execute"} {
		args := map[string]any{"operation_id": opID}
		if tool == "describe_op" {
			args = map[string]any{"operation_id": opID}
		}
		res := callTool(t, s, tool, args)
		if !res.IsError || !strings.Contains(res.Content[0].Text, "not classified for MCP exposure") {
			t.Errorf("%s(%s) must be refused as unclassified, got %s", tool, opID, res.Content[0].Text)
		}
	}
	d := callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": opID})
	if !d.IsError {
		t.Errorf("get_skill describe of an unclassified op must be refused")
	}
	if hits != 0 {
		t.Errorf("an unclassified operation reached the backend %d times", hits)
	}
}

// --- request assembly for every approved operation ---

func sampleValue(s *registry.SchemaInfo) any {
	if s == nil {
		return "x"
	}
	if s.Const != nil {
		return s.Const
	}
	if len(s.Enum) > 0 {
		for _, e := range s.Enum {
			if e != nil && e != "" {
				return e
			}
		}
		return s.Enum[0]
	}
	if len(s.OneOf) > 0 {
		return sampleValue(&s.OneOf[0])
	}
	if len(s.AnyOf) > 0 && s.Type == "" {
		return sampleValue(&s.AnyOf[0])
	}
	switch s.Type {
	case "integer", "number":
		if s.Format == "uint64" {
			return json.Number("1")
		}
		if s.Minimum != nil && *s.Minimum > 1 {
			return json.Number(fmt.Sprintf("%v", *s.Minimum))
		}
		return json.Number("1")
	case "boolean":
		return true
	case "array":
		n := s.MinItems
		if n < 1 {
			n = 1
		}
		out := make([]any, n)
		for i := range out {
			v := sampleValue(s.Items)
			if num, ok := v.(json.Number); ok && n > 1 {
				v = json.Number(fmt.Sprintf("%d", i+1))
				_ = num
			}
			out[i] = v
		}
		return out
	case "object":
		obj := map[string]any{}
		for _, name := range s.Required {
			p := s.Properties[name]
			obj[name] = sampleValue(&p)
		}
		if len(s.AnyOf) > 0 {
			if extra, ok := sampleValue(&s.AnyOf[0]).(map[string]any); ok {
				for k, v := range extra {
					obj[k] = v
				}
			}
		}
		if len(obj) < s.MinProperties || len(obj) == 0 {
			names := make([]string, 0, len(s.Properties))
			for n := range s.Properties {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if len(obj) >= s.MinProperties && len(obj) > 0 {
					break
				}
				p := s.Properties[n]
				obj[n] = sampleValue(&p)
			}
		}
		return obj
	}
	switch s.Format {
	case "uuid":
		return "00000000-0000-4000-8000-000000000001"
	case "date-time":
		return "2026-01-01T00:00:00Z"
	case "uint64":
		return "1"
	}
	n := s.MinLength
	if n < 2 {
		n = 2
	}
	return strings.Repeat("a", n)
}

// xin2424ArgOverrides supplies arguments a schema walk cannot derive (a body
// whose validity depends on cross-field rules the backend owns).
var xin2424ArgOverrides = map[string]map[string]any{
	"capability.install": {
		"definition": map[string]any{"schema_version": "1.0", "name": "pack", "skills": []any{map[string]any{"name": "s", "content": "c"}}},
	},
	"workspace.member.add": {"principal_type": "bot", "principal_id": "00000000-0000-4000-8000-000000000002", "role": "member"},
}

func sampleArguments(d *registry.OperationDetail) map[string]any {
	args := map[string]any{}
	for _, name := range extractPathParams(d.Path) {
		args[name] = "p-" + strings.ReplaceAll(name, "_", "")
		if p := pathParam(d, name); p != nil && (p.Format == "uint64" || p.Type == "integer") {
			args[name] = "12"
		}
	}
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.In == "path" || !p.Required {
			continue
		}
		sch := registry.SchemaInfo{Type: p.Type, Format: p.Format, Enum: p.Enum, MinLength: p.MinLength}
		v := sampleValue(&sch)
		if p.Name == "Idempotency-Key" {
			v = "key-1"
		}
		args[p.Name] = v
	}
	if d.RequestBody != nil && !d.Multipart && !d.RawBody {
		if body, ok := sampleValue(d.RequestBody).(map[string]any); ok {
			for k, v := range body {
				args[k] = v
			}
		}
	}
	for k, v := range xin2424ArgOverrides[d.ID] {
		args[k] = v
	}
	return args
}

func approvedOperations(t *testing.T) []string {
	t.Helper()
	var f exposureFile
	if err := json.Unmarshal(exposureJSON, &f); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(f.Approved))
	for id := range f.Approved {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

var pathParamRe = regexp.MustCompile(`\{[^}]+\}`)

func pathParam(d *registry.OperationDetail, name string) *registry.ParamInfo {
	for i := range d.Parameters {
		if d.Parameters[i].In == "path" && d.Parameters[i].Name == name {
			return &d.Parameters[i]
		}
	}
	return nil
}

// TestXIN2424ExecuteDryRunAssemblesEveryApprovedOperation drives every
// approved operation through get_skill describe -> execute(dry_run) and checks
// the planned method, path and masked Bot credential. Dry-run never reaches
// the backend; uploads are described from metadata only.
func TestXIN2424ExecuteDryRunAssemblesEveryApprovedOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("dry-run reached the backend")
	}))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.uploadRoot = t.TempDir()
	reg := registry.MustNew()

	ids := approvedOperations(t)
	if len(ids) < 90 {
		t.Fatalf("approved operations = %d, want the full XIN-2424 set", len(ids))
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			d, _ := reg.GetOperation(id)
			desc := toolText(t, callTool(t, s, "get_skill", map[string]any{"intent": "describe", "operation_id": id}))
			fp, _ := desc["schema_fingerprint"].(string)
			if fp == "" {
				t.Fatalf("describe returned no fingerprint: %v", desc)
			}
			args := sampleArguments(d)
			if d.Multipart || d.RawBody {
				args["file_path"] = filepath.Join(s.uploadRoot, "upload.bin")
			}
			res := callTool(t, s, "execute", map[string]any{"operation_id": id, "arguments": args, "dry_run": true, "schema_fingerprint": fp})
			out := toolText(t, res)
			if out["status"] != "ok" {
				t.Fatalf("dry-run status %v: %s", out["status"], res.Content[0].Text)
			}
			data, _ := out["envelope"].(map[string]any)["data"].(map[string]any)
			if data["method"] != d.Method {
				t.Errorf("method = %v, want %s", data["method"], d.Method)
			}
			wantPath := pathParamRe.ReplaceAllStringFunc(d.Path, func(m string) string {
				name := strings.Trim(m, "{}")
				if p := pathParam(d, name); p != nil && p.Secret {
					return "***REDACTED***" // credential-equivalent path values are masked in the plan
				}
				return url.PathEscape(fmt.Sprint(args[name]))
			})
			gotPath, _ := data["path"].(string)
			if raw, ok := data["url"].(string); ok {
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatalf("dry-run url %q: %v", raw, err)
				}
				gotPath = u.EscapedPath()
				auth, _ := data["headers"].(map[string]any)["Authorization"].(string)
				if !strings.HasPrefix(auth, "Bearer bf_") || !strings.Contains(auth, "***") || strings.Contains(auth, "bf_test") {
					t.Errorf("Authorization must carry the masked Bot bearer, got %q", auth)
				}
			}
			if gotPath != wantPath {
				t.Errorf("path = %q, want %q", gotPath, wantPath)
			}
		})
	}
}

// TestXIN2424ExecuteRejectsMalformedAndUnknownArguments covers argument
// hygiene on new operations: an undeclared key, a bad enum and a missing
// required path argument are refused locally, before any request.
func TestXIN2424ExecuteRejectsMalformedAndUnknownArguments(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	cases := []struct {
		op   string
		args map[string]any
	}{
		{"bot.resolve-targets", map[string]any{"name": "ops", "kind": "everything"}},
		{"thread.delete", map[string]any{"group_no": "g1"}},
		{"docs.attachments.svg-upload", map[string]any{"docId": "d1", "file_path": "x.svg", "surprise": true}},
		{"group.webhook.update", map[string]any{"group_no": "g1", "webhook_id": "w1", "status": "bogus"}},
		{"docs.file.export", map[string]any{"docId": "d1", "format": "exe"}},
	}
	for _, tc := range cases {
		out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": tc.op, "arguments": tc.args}))
		if out["status"] != "validation_error" {
			t.Errorf("%s %v: status = %v, want validation_error", tc.op, tc.args, out["status"])
		}
	}
	if hits != 0 {
		t.Errorf("malformed arguments reached the backend %d times", hits)
	}
}

// --- binary results ---

func TestXIN2424ExecuteReturnsBinaryInline(t *testing.T) {
	big := bytes.Repeat([]byte{0xff}, maxInlineBinaryBytes+1)
	media := []byte{0x89, 'P', 'N', 'G', 0, 1, 2}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v/3"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<h1>published</h1>"))
		case strings.Contains(r.URL.Path, "/ppt/media/"):
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(media)
		case strings.HasSuffix(r.URL.Path, "/export/file"):
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(big)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	data := func(op string, args map[string]any) map[string]any {
		t.Helper()
		out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": args}))
		if out["status"] != "ok" {
			t.Fatalf("%s: %v", op, out)
		}
		return out["envelope"].(map[string]any)["data"].(map[string]any)
	}

	page := data("html.page.version", map[string]any{"doc_id": "slug1", "version": json.Number("3")})
	if page["content_text"] != "<h1>published</h1>" || page["inline"] != true {
		t.Errorf("HTML page must be returned as inline text, got %v", page)
	}

	img := data("docs.ppt.media.get", map[string]any{"docId": "d1", "attachId": "a1"})
	decoded, err := base64.StdEncoding.DecodeString(fmt.Sprint(img["content_base64"]))
	if err != nil || !bytes.Equal(decoded, media) {
		t.Errorf("binary media must round-trip through content_base64, got %v", img)
	}
	sum := sha256.Sum256(media)
	if img["sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 = %v", img["sha256"])
	}

	large := data("docs.file.export", map[string]any{"docId": "d1", "format": "pdf"})
	if large["inline"] != false || large["content_base64"] != nil || large["content_text"] != nil {
		t.Errorf("an over-limit body must be described, not inlined: %v", large)
	}
	if size, _ := large["size"].(float64); int(size) != len(big) {
		t.Errorf("size = %v, want %d", large["size"], len(big))
	}
}

// --- raw-body uploads ---

func TestXIN2424ExecuteRawBodyUploadIsConfined(t *testing.T) {
	var mu sync.Mutex
	var gotBody []byte
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotBody, _ = io.ReadAll(r.Body)
		gotHeaders = r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"ref":"ppt-media:a1","attachId":"a1","mime":"image/png","sizeBytes":4}}`))
	}))
	defer srv.Close()

	root := t.TempDir()
	inside := filepath.Join(root, "pic.png")
	if err := os.WriteFile(inside, []byte("PNG!"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := func(p string) map[string]any {
		return map[string]any{"docId": "d1", "X-Media-Type": "image/png", "X-File-Name": "pic.png", "file_path": p}
	}

	// HTTP transport without an operator upload root: default-deny.
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.httpMode = true
	out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": "docs.ppt.media.upload", "arguments": args(inside)}))
	if out["status"] != "validation_error" || gotBody != nil {
		t.Fatalf("HTTP upload without a root must be refused before any request: %v", out)
	}

	// With a root: a file outside it is refused, a file inside is sent raw.
	s.uploadRoot = root
	out = toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": "docs.ppt.media.upload", "arguments": args(outside)}))
	if out["status"] != "validation_error" || gotBody != nil {
		t.Fatalf("a path outside the upload root must be refused: %v", out)
	}
	dry := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": "docs.ppt.media.upload", "arguments": args(inside), "dry_run": true}))
	if dry["status"] != "ok" || gotBody != nil || strings.Contains(fmt.Sprint(dry), "PNG!") {
		t.Fatalf("raw-body dry-run must not read or send the file: %v", dry)
	}
	out = toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": "docs.ppt.media.upload", "arguments": args(inside)}))
	if out["status"] != "ok" {
		t.Fatalf("confined raw upload: %v", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if string(gotBody) != "PNG!" {
		t.Errorf("backend body = %q, want the raw file bytes", gotBody)
	}
	if gotHeaders.Get("Content-Type") != "application/octet-stream" || gotHeaders.Get("X-Media-Type") != "image/png" || gotHeaders.Get("X-File-Name") != "pic.png" {
		t.Errorf("raw upload headers = %v", gotHeaders)
	}
}

// --- forced context and auth on new operations ---

func TestXIN2424ForcedContextWinsOnNewSessionBoundOperations(t *testing.T) {
	var mu sync.Mutex
	var got []*http.Request
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r.Clone(context.Background()))
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.trusted = TrustedContext{SpaceID: "S-forced", ChannelID: "C-forced", ChannelType: "2"}

	call := func(op string, args map[string]any) map[string]any {
		t.Helper()
		return toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": args}))
	}
	call("bot.space-principal", map[string]any{"uid": "u1", "space_id": "S-model"})
	call("message.card-revisions-clear", map[string]any{"channel_id": "C-model", "channel_type": json.Number("1"), "message_id": "m1"})
	call("drive.doc.mount-by-role", map[string]any{"X-Space-Id": "S-model", "space_id": "shared:drive-1", "doc_id": "doc1"})

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("requests = %d, want 3", len(got))
	}
	if q := got[0].URL.Query().Get("space_id"); q != "S-forced" {
		t.Errorf("bot.space-principal space_id = %q, want the forced space", q)
	}
	if !strings.Contains(bodies[1], `"channel_id":"C-forced"`) || !strings.Contains(bodies[1], `"channel_type":2`) {
		t.Errorf("message.card-revisions-clear body = %s, want the forced channel", bodies[1])
	}
	if h := got[2].Header.Get("X-Space-Id"); h != "S-forced" {
		t.Errorf("drive.doc.mount-by-role X-Space-Id = %q, want the forced space", h)
	}
	if !strings.Contains(bodies[2], `"space_id":"shared:drive-1"`) {
		t.Errorf("drive resource space_id must stay model-controlled, body = %s", bodies[2])
	}
}

func TestXIN2424AuthFailureIsClassified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid bot token"}`))
	}))
	defer srv.Close()
	s := facadeServer(t, facadeTwo)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_revoked")
	for _, op := range []string{"bot.card-profile", "thread.delete", "drive.trash.list"} {
		args := map[string]any{}
		if op == "thread.delete" {
			args = map[string]any{"group_no": "g1", "short_id": "s1"}
		}
		out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": args}))
		if out["status"] != "auth_error" || out["retryable"] != false {
			t.Errorf("%s: status = %v retryable = %v, want auth_error/false", op, out["status"], out["retryable"])
		}
	}
}

// --- drive share operations ---

func TestXIN2424DriveShareOpsCallableOverMCP(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"tok","file_id":1,"url":"https://storage.example/o"}`))
	}))
	defer srv.Close()
	s := facadeServer(t, facadeBoth)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")

	search := toolText(t, callTool(t, s, "search_ops", map[string]any{"domain": "drive", "query": "share"}))
	listed := map[string]bool{}
	for _, o := range search["operations"].([]any) {
		listed[fmt.Sprint(o.(map[string]any)["id"])] = true
	}
	for _, op := range []string{"drive.share.blob-create", "drive.share.access", "drive.share.download"} {
		if !listed[op] {
			t.Errorf("%s must be advertised", op)
		}
		if d := callTool(t, s, "describe_op", map[string]any{"operation_id": op}); d.IsError {
			t.Errorf("describe_op(%s) must succeed: %s", op, d.Content[0].Text)
		}
	}
	for _, c := range []struct {
		op   string
		args map[string]any
	}{
		{"drive.share.blob-create", map[string]any{"file_id": json.Number("7"), "permission": "view"}},
		{"drive.share.access", map[string]any{"share_token": "tok-secret", "password": "pw"}},
		{"drive.share.download", map[string]any{"share_token": "tok-secret"}},
	} {
		out := toolText(t, callTool(t, s, "execute", map[string]any{"operation_id": c.op, "arguments": c.args}))
		if out["status"] != "ok" {
			t.Errorf("%s: %v", c.op, out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /v1/bot/drive/shares", "POST /v1/bot/drive/shares/tok-secret/access", "POST /v1/bot/drive/shares/tok-secret/download"}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Errorf("requests = %v, want %v", paths, want)
	}
}
