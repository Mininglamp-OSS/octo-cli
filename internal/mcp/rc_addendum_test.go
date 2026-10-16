package mcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestAddendumFingerprintSafetyDefaults(t *testing.T) {
	reg := registry.MustNew()
	for _, id := range []string{"docs.sheet.split", "docs.sheet.deduplicate"} {
		base, _ := reg.GetOperation(id)
		changed, _ := reg.GetOperation(id)
		prop := changed.RequestBody.Properties["preview"]
		prop.Default = false
		changed.RequestBody.Properties["preview"] = prop
		if schemaFingerprint(base) == schemaFingerprint(changed) {
			t.Fatalf("%s preview default change did not invalidate fingerprint", id)
		}
	}
	base, _ := reg.GetOperation("docs.sheet.split")
	changed, _ := reg.GetOperation("docs.sheet.split")
	prop := changed.RequestBody.Properties["overwrite"]
	prop.Default = true
	changed.RequestBody.Properties["overwrite"] = prop
	if schemaFingerprint(base) == schemaFingerprint(changed) {
		t.Fatal("overwrite consent default change did not invalidate fingerprint")
	}
}

func TestAddendumFingerprintIgnoresDocumentation(t *testing.T) {
	reg := registry.MustNew()
	base, _ := reg.GetOperation("docs.sheet.split")
	changed, _ := reg.GetOperation("docs.sheet.split")
	changed.Summary = "Different prose"
	response := *changed.ResponseSchema
	response.Description = "Different response prose"
	changed.ResponseSchema = &response
	changed.Parameters[0].Description = "Different parameter prose"
	prop := changed.RequestBody.Properties["preview"]
	prop.Description = "Different body prose"
	changed.RequestBody.Properties["preview"] = prop
	if schemaFingerprint(base) != schemaFingerprint(changed) {
		t.Fatal("documentation-only changes invalidated execution fingerprint")
	}
}

func TestAddendumMappingOverrideMustOwnService(t *testing.T) {
	reg := registry.MustNew()
	metas, err := loadSkillMetas()
	if err != nil {
		t.Fatal(err)
	}
	m, err := BuildMapping(reg)
	if err != nil {
		t.Fatal(err)
	}
	mf := &manifestFile{Skills: map[string]manifestSkill{
		"octo-docs": {Operations: map[string]manifestOp{"message.send": {}}},
	}}
	if problems := m.validate(reg, mf, metas); !containsSubstr(problems, "does not own service") {
		t.Fatalf("cross-skill override accepted: %v", problems)
	}
	mf.Skills = map[string]manifestSkill{"octo-messaging": {Operations: map[string]manifestOp{"message.send": {}}}}
	if problems := m.validate(reg, mf, metas); len(problems) != 0 {
		t.Fatalf("legitimate override refused: %v", problems)
	}
}

func TestAddendumDomainReflectionBound(t *testing.T) {
	s, err := NewServer(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := s.searchOps(t.Context(), strings.Repeat("界", 1<<18), "")
	if len(r.Content[0].Text) > 2048 {
		t.Fatalf("unknown domain reflected %d bytes", len(r.Content[0].Text))
	}
}

func TestAddendumNullAuditEnvelope(t *testing.T) {
	for _, env := range []string{"null", "[]", "invalid"} {
		if got := spliceForcedArguments([]byte(env), map[string]bool{"channel_id": true}); !bytes.Equal(got, []byte(env)) {
			t.Fatalf("non-object envelope changed: %s", got)
		}
	}
}

func TestAddendumFrontmatterAndUnicodeAnchors(t *testing.T) {
	if got := parseFrontmatterScalar([]byte("---\nmetadata:\n  disabled: true\n---\n"), "disabled:"); got != "" {
		t.Fatalf("nested key promoted to root: %q", got)
	}
	if got := parseFrontmatterScalar([]byte("ordinary text\n---\nname: pretend\n---\n"), "name:"); got != "" {
		t.Fatalf("body fence treated as frontmatter: %q", got)
	}
	content := []byte("## 发送与撰写 compose\n## Send & receive\n## Send receive\n")
	for _, anchor := range []string{"发送与撰写-compose", "send--receive", "send-receive"} {
		if !headingAnchorExists(content, anchor) {
			t.Errorf("missing anchor %q", anchor)
		}
	}
}

func TestAddendumRankingUsesConstantRows(t *testing.T) {
	var distance int
	allocs := testing.AllocsPerRun(5, func() { distance = levenshtein(strings.Repeat("x", 128), strings.Repeat("y", 128)) })
	if distance != 128 {
		t.Fatalf("distance = %d", distance)
	}
	if allocs > 4 {
		t.Fatalf("distance allocates %.0f times instead of two rows", allocs)
	}
}

func TestAddendumFingerprintNestedDefault(t *testing.T) {
	base := &registry.OperationDetail{RequestBody: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{
		"outer": {Type: "array", Items: &registry.SchemaInfo{Type: "object", OneOf: []registry.SchemaInfo{{Type: "boolean", Default: true}}}},
	}}}
	before := schemaFingerprint(base)
	base.RequestBody.Properties["outer"].Items.OneOf[0].Default = false
	if before == schemaFingerprint(base) {
		t.Fatal("nested default drift ignored")
	}
	base.RequestBody.Properties["outer"].Items.OneOf[0].Default = map[string]any{"description": "business default"}
	before = schemaFingerprint(base)
	base.RequestBody.Properties["outer"].Items.OneOf[0].Default = map[string]any{"description": "changed business default"}
	if before == schemaFingerprint(base) {
		t.Fatal("business default content stripped as documentation")
	}
}

func TestAddendumFingerprintRejectsBeforeBackend(t *testing.T) {
	be := &backend{}
	srv := be.server(t)
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(srv.URL, "bf_test")
	s.WithFacade(facadeTwo)
	d, _ := s.reg.GetOperation("docs.sheet.split")
	p := d.RequestBody.Properties["preview"]
	p.Default = !p.Default.(bool)
	d.RequestBody.Properties["preview"] = p
	r := s.execute(t.Context(), d.ID, map[string]any{"docId": "fixture", "sheetId": "sheet-one"}, false, schemaFingerprint(d))
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out["status"] != "schema_drift" || be.path != "" {
		t.Fatalf("default drift must refuse before backend: %s path=%s", r.Content[0].Text, be.path)
	}
}

func TestAddendumDiscoveryPaginationBothFacades(t *testing.T) {
	for _, facade := range []facadeMode{facadeThree, facadeTwo} {
		s := facadeServer(t, facade)
		for _, domain := range s.reg.EnabledServices() {
			want := map[string]bool{}
			for _, op := range s.reg.ListOperations(domain) {
				if _, withheld := divergentMCPOps()[op.ID]; !withheld {
					want[op.ID] = true
				}
			}
			seen := map[string]bool{}
			cursor := ""
			for page := 0; ; page++ {
				if page > 20 {
					t.Fatal("cursor did not terminate")
				}
				tool := "search_ops"
				args := map[string]any{"domain": domain, "cursor": cursor}
				if facade == facadeTwo {
					tool = "get_skill"
					args["intent"] = "search"
				}
				res := callTool(t, s, tool, args)
				if res.IsError {
					t.Fatalf("page failed: %s", res.Content[0].Text)
				}
				out := toolText(t, res)
				ops := out["operations"].([]any)
				if len(ops) > searchOpsPageLimit {
					t.Fatal("page exceeds cap")
				}
				for _, raw := range ops {
					id := raw.(map[string]any)["id"].(string)
					if seen[id] || !want[id] {
						t.Fatalf("duplicate or withheld operation %s", id)
					}
					seen[id] = true
				}
				cursor, _ = out["next_cursor"].(string)
				if cursor == "" {
					break
				}
			}
			if len(seen) != len(want) {
				t.Fatalf("%s facade=%s discovered %d/%d", domain, facade, len(seen), len(want))
			}
		}
	}
}

func TestAddendumDiscoveryCursorValidation(t *testing.T) {
	s := newTestServer(t)
	first := toolText(t, s.searchOps(t.Context(), "docs", ""))
	cursor := first["next_cursor"].(string)
	for _, args := range [][3]string{{"docs", "", "garbage"}, {"docs", "", strings.Repeat("x", 3000)}, {"loop", "", cursor}, {"docs", "sheet", cursor}, {"", "", cursor}, {"unknown", "", cursor}, {"matter", "", cursor}} {
		if !s.searchOps(t.Context(), args[0], args[1], args[2]).IsError {
			t.Fatalf("invalid cursor accepted: domain=%q query=%q", args[0], args[1])
		}
	}
	second := toolText(t, s.searchOps(t.Context(), "docs", "", cursor))
	if len(second["operations"].([]any)) == 0 {
		t.Fatal("valid continuation returned no operations")
	}
	for _, facade := range []facadeMode{facadeThree, facadeTwo} {
		ss := facadeServer(t, facade)
		for _, arg := range []any{true, 1, nil} {
			tool := "search_ops"
			a := map[string]any{"domain": "docs", "cursor": arg}
			if facade == facadeTwo {
				tool = "get_skill"
				a["intent"] = "search"
			}
			raw, _ := json.Marshal(a)
			resp := rawToolCall(t, ss, `{"name":"`+tool+`","arguments":`+string(raw)+`}`)
			if resp.Error == nil {
				t.Fatalf("invalid cursor type accepted: %v", arg)
			}
		}
	}
}

func TestAddendumSharedHTTPHandlerConcurrentRequests(t *testing.T) {
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeBoth)
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search_ops","arguments":{"domain":"docs"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_skill","arguments":{"intent":"describe","operation_id":"message.send"}}}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"execute","arguments":{"operation_id":"message.send","dry_run":true}}}`,
	}
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(payload string) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "http://localhost/", strings.NewReader(payload))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK || !json.Valid(w.Body.Bytes()) {
				t.Errorf("concurrent request failed: %d %s", w.Code, w.Body.String())
			}
		}(requests[i%len(requests)])
	}
	wg.Wait()
}

func TestAddendumSecretMetadataBoundary(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		d, _ := reg.GetOperation(op.ID)
		if _, withheld := divergentMCPOps()[op.ID]; withheld {
			continue
		}
		if d.Multipart {
			for _, p := range d.Parameters {
				if p.In == "path" && p.Secret {
					t.Fatalf("multipart secret path needs masked planning: %s %s", op.ID, p.Name)
				}
			}
		}
		if d.RequestBody != nil {
			for name, p := range d.RequestBody.Properties {
				if p.Secret {
					t.Fatalf("MCP body secret needs merged-data masking: %s %s", op.ID, name)
				}
			}
		}
	}
}
