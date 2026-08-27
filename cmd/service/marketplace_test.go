package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// TestMarketplaceRegistryShape pins every unified-plugin operationId to its
// method+path and the marketplace-wide invariants (OCTO_API_BASE_URL, space
// header). The legacy per-type endpoints (/experts, /squads, /mcps, /skills)
// are intentionally gone: they read frozen pre-migration tables, so the CLI now
// drives the unified /plugins/* surface. Only the type-agnostic helper
// pipelines (probe, skill upload/parse, icon presign) are retained verbatim.
func TestMarketplaceRegistryShape(t *testing.T) {
	r := registry.MustNew()
	wants := map[string]struct {
		method string
		path   string
	}{
		"plugin.list":          {http.MethodGet, "/market/api/v1/plugins"},
		"plugin.get":           {http.MethodGet, "/market/api/v1/plugins/detail"},
		"plugin.version.list":  {http.MethodGet, "/market/api/v1/plugins/versions"},
		"plugin.skillmd":       {http.MethodGet, "/market/api/v1/plugins/skill_md"},
		"plugin.download":      {http.MethodGet, "/market/api/v1/plugins/download"},
		"plugin_category.list": {http.MethodGet, "/market/api/v1/plugin_categories"},
		"plugin_tag.list":      {http.MethodGet, "/market/api/v1/plugin_tags"},
		"plugin.upsert":        {http.MethodPost, "/market/api/v1/plugins/upsert"},
		"plugin.delete":        {http.MethodPost, "/market/api/v1/plugins/delete"},
		"plugin.publish":       {http.MethodPost, "/market/api/v1/plugins/publish"},
		"plugin.install":       {http.MethodPost, "/market/api/v1/plugins/install"},
		"plugin.import":        {http.MethodPost, "/market/api/v1/plugins/import"},

		// Retained helper pipelines (unchanged endpoints).
		"mcp.probe":                {http.MethodPost, "/market/api/v1/mcps/_probe"},
		"skill_upload.create":      {http.MethodPost, "/market/api/v1/skill_uploads"},
		"skill_upload.parse":       {http.MethodPost, "/market/api/v1/skill_uploads/{skill_upload_id}/parse"},
		"skill_parse_task.get":     {http.MethodGet, "/market/api/v1/skill_parse_tasks/{skill_parse_task_id}"},
		"skill_icon_upload.create": {http.MethodPost, "/market/api/v1/skill_icon_uploads"},
		"mcp_icon_upload.create":   {http.MethodPost, "/market/api/v1/mcp_icon_uploads"},
	}

	if got := len(r.ListOperations("marketplace")); got != len(wants) {
		t.Fatalf("marketplace operation count = %d, want %d", got, len(wants))
	}
	for id, want := range wants {
		op, ok := r.GetOperation(id)
		if !ok {
			t.Fatalf("operation %q not found", id)
		}
		if op.Method != want.method || op.Path != want.path {
			t.Errorf("%s = %s %s, want %s %s", id, op.Method, op.Path, want.method, want.path)
		}
		if op.BaseURLEnv != "OCTO_API_BASE_URL" {
			t.Errorf("%s base URL env = %q, want OCTO_API_BASE_URL", id, op.BaseURLEnv)
		}
		if !op.SpaceHeader {
			t.Errorf("%s must send the space header", id)
		}
	}

	// The retired legacy operations must be gone so the CLI can never read the
	// frozen per-type tables again.
	for _, gone := range []string{
		"skill.list", "skill.get", "skill.download", "skill.publish",
		"mcp.list", "mcp.create", "marketplace.expert.list", "marketplace.squad.create",
	} {
		if _, ok := r.GetOperation(gone); ok {
			t.Errorf("legacy operation %q must be retired", gone)
		}
	}
}

// TestMarketplacePluginListRequest pins the unified list path, the required
// scene_code+plugin_type query flags, and the {data:[...],pagination} envelope
// flattening (items at .data, pagination at ._pagination).
func TestMarketplacePluginListRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"plugin_id":"p1"}],"pagination":{"total":1,"page":2,"page_size":20}}`))
	})

	root.SetArgs([]string{
		"marketplace", "plugin", "list",
		"--scene-code", "default", "--plugin-type", "skill",
		"--q", "deep miner", "--tag", "cli", "--category-id", "dev-tools",
		"--sort", "newest", "--page", "2", "--page-size", "20",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/market/api/v1/plugins" {
		t.Errorf("got %s %s, want GET /market/api/v1/plugins", gotMethod, gotPath)
	}
	for _, want := range []string{"scene_code=default", "plugin_type=skill", "q=deep+miner", "tag=cli", "category_id=dev-tools", "sort=newest", "page=2", "page_size=20"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, want %q", gotQuery, want)
		}
	}

	var env struct {
		Data       []map[string]any `json:"data"`
		Pagination map[string]any   `json:"_pagination"`
	}
	if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
		t.Fatalf("parse envelope: %v -- out=%s", err, tf.Out.String())
	}
	if len(env.Data) != 1 || env.Data[0]["plugin_id"] != "p1" {
		t.Errorf(".data must be the item array, got %s", tf.Out.String())
	}
	if env.Pagination["total"] == nil {
		t.Errorf("._pagination must carry backend pagination, got %s", tf.Out.String())
	}
}

// TestMarketplacePluginListRequiresSceneAndType pins that the backend-required
// scene_code and plugin_type are enforced locally before any request is sent.
func TestMarketplacePluginListRequiresSceneAndType(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("request must not be sent when required flags are missing")
	})
	root.SetArgs([]string{"marketplace", "plugin", "list", "--plugin-type", "skill"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "scene-code") {
		t.Fatalf("execute error = %v, want a required scene-code error", err)
	}
}

// TestMarketplacePluginGetRequest pins the detail path and its query flags.
func TestMarketplacePluginGetRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"plugin":{"plugin_id":"p1"}}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "get", "--plugin-id", "p1", "--include-relations"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/market/api/v1/plugins/detail" {
		t.Errorf("got %s %s, want GET /market/api/v1/plugins/detail", gotMethod, gotPath)
	}
	for _, want := range []string{"plugin_id=p1", "include_relations=true"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, want %q", gotQuery, want)
		}
	}
}

// TestMarketplacePluginImportRequest pins the skill finalize path and that the
// promoted body flags (parse_task_id etc.) reach the JSON body.
func TestMarketplacePluginImportRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"plugin":{"plugin_id":"p1"}}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "import", "--parse-task-id", "task-1", "--name", "全栈清单", "--version", "1.0.0"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/market/api/v1/plugins/import" {
		t.Errorf("got %s %s, want POST /market/api/v1/plugins/import", gotMethod, gotPath)
	}
	if gotBody["parse_task_id"] != "task-1" || gotBody["name"] != "全栈清单" || gotBody["version"] != "1.0.0" {
		t.Errorf("body = %#v", gotBody)
	}
}

// TestMarketplacePluginInstallRequest pins the install body wiring.
func TestMarketplacePluginInstallRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"agent_id":"a1"}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "install", "--plugin-id", "p1", "--workspace-id", "ws", "--runtime-id", "rt"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/plugins/install" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["plugin_id"] != "p1" || gotBody["workspace_id"] != "ws" || gotBody["runtime_id"] != "rt" {
		t.Errorf("body = %#v", gotBody)
	}
}

// TestMarketplacePluginPublishRequest pins the publish body wiring.
func TestMarketplacePluginPublishRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"version":"1.0.0"}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "publish", "--plugin-id", "p1", "--version", "1.0.0", "--changelog", "init"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/plugins/publish" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["plugin_id"] != "p1" || gotBody["version"] != "1.0.0" || gotBody["changelog"] != "init" {
		t.Errorf("body = %#v", gotBody)
	}
}

// TestMarketplacePluginUpsertRequest pins the unified write path: a full
// document passed via --data reaches POST /plugins/upsert with the plugin
// object intact, and the newly-typed nested enums gate visibility/plugin_type
// client-side so the retired `public` value and unknown types never leave.
func TestMarketplacePluginUpsertRequest(t *testing.T) {
	// Happy path: the full document is forwarded and the plugin sub-document
	// survives verbatim.
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"plugin":{"plugin_id":"p1"}}}`))
	})
	root.SetArgs([]string{
		"marketplace", "plugin", "upsert",
		"--data", `{"plugin":{"plugin_name":"deep miner","plugin_type":"connector","visibility":"private"}}`,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/market/api/v1/plugins/upsert" {
		t.Errorf("got %s %s, want POST /market/api/v1/plugins/upsert", gotMethod, gotPath)
	}
	plugin, ok := gotBody["plugin"].(map[string]any)
	if !ok || plugin["plugin_name"] != "deep miner" || plugin["plugin_type"] != "connector" || plugin["visibility"] != "private" {
		t.Errorf("body.plugin = %#v", gotBody["plugin"])
	}

	// Each of these documents must be rejected locally before any request is
	// sent: the retired `public` visibility (the gate must not be bypassable
	// from upsert the way it is blocked on import), an unknown plugin_type, and
	// a document missing a required nested field.
	for _, tc := range []struct {
		name string
		data string
	}{
		{"public visibility", `{"plugin":{"plugin_name":"x","plugin_type":"connector","visibility":"public"}}`},
		{"unknown plugin_type", `{"plugin":{"plugin_name":"x","plugin_type":"nonsense","visibility":"private"}}`},
		{"missing plugin_type", `{"plugin":{"plugin_name":"x","visibility":"private"}}`},
	} {
		sent := false
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			sent = true
			t.Errorf("%s: request must not be sent", tc.name)
		})
		root.SetArgs([]string{"marketplace", "plugin", "upsert", "--data", tc.data})
		if err := root.Execute(); err == nil {
			t.Errorf("%s: expected a local validation error, got nil", tc.name)
		}
		if sent {
			t.Errorf("%s: no request should have left the client", tc.name)
		}
	}
}

// TestMarketplaceSkillmdRequest pins the unified skill_md endpoint keyed by
// plugin_id (experts/teams resolve the relation target first).
func TestMarketplaceSkillmdRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"content":"# SKILL"}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "skillmd", "--plugin-id", "p1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/market/api/v1/plugins/skill_md" {
		t.Errorf("got %s %s, want GET /market/api/v1/plugins/skill_md", gotMethod, gotPath)
	}
	if !strings.Contains(gotQuery, "plugin_id=p1") {
		t.Errorf("query = %q, want plugin_id=p1", gotQuery)
	}
}

// TestMarketplaceDownloadRequest pins the unified download path, the plugin_id
// query, and the space header on the binary-response endpoint.
func TestMarketplaceDownloadRequest(t *testing.T) {
	var gotPath, gotQuery, gotSpace string
	root, _, _ := rootWithServiceSpaced(t, "space-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotSpace = r.Header.Get("X-Space-Id")
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("PK\x03\x04"))
	})

	root.SetArgs([]string{"marketplace", "plugin", "download", "--plugin-id", "p1"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/plugins/download" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "plugin_id=p1") {
		t.Errorf("query = %q, want plugin_id=p1", gotQuery)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q, want space-1", gotSpace)
	}
}

// TestMarketplaceLocalPrefixOverride pins the local-dev prefix rewrite:
// /market/api/v1/... maps to the standalone service's /api/v1/... routes.
func TestMarketplaceLocalPrefixOverride(t *testing.T) {
	t.Setenv("OCTO_MARKETPLACE_API_PREFIX", "")
	var gotPath string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
	})

	root.SetArgs([]string{"marketplace", "plugin", "list", "--scene-code", "default", "--plugin-type", "connector"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/api/v1/plugins" {
		t.Fatalf("path = %q, want /api/v1/plugins", gotPath)
	}
}

func TestMarketplaceCommandTree(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {})
	marketplace := findCmd(root, "marketplace")
	if marketplace == nil {
		t.Fatal("missing marketplace command")
	}
	domains := map[string][]string{
		"plugin":            {"list", "get", "skillmd", "download", "upsert", "delete", "publish", "install", "import"},
		"plugin-category":   {"list"},
		"plugin-tag":        {"list"},
		"mcp":               {"probe"},
		"skill-upload":      {"create", "parse"},
		"skill-parse-task":  {"get"},
		"skill-icon-upload": {"create"},
		"mcp-icon-upload":   {"create"},
	}
	for domain, children := range domains {
		domainCmd := findCmd(marketplace, domain)
		if domainCmd == nil {
			t.Errorf("missing marketplace %s command group", domain)
			continue
		}
		for _, name := range children {
			if findCmd(domainCmd, name) == nil {
				t.Errorf("missing marketplace %s %s command", domain, name)
			}
		}
	}
	// `plugin version list` is a nested subgroup, checked separately.
	if pv := findCmd(findCmd(marketplace, "plugin"), "version"); pv == nil || findCmd(pv, "list") == nil {
		t.Error("missing marketplace plugin version list command")
	}
	if got := childNames(marketplace); len(got) != len(domains) {
		t.Errorf("marketplace domains = %v, want %d groups", got, len(domains))
	}
}

// TestMarketplacePluginTypeEnum pins the plugin_type enum the list/detail
// filters accept, matching the backend's four unified types.
func TestMarketplacePluginTypeEnum(t *testing.T) {
	r := registry.MustNew()
	op, ok := r.GetOperation("plugin.list")
	if !ok {
		t.Fatal("plugin.list not found")
	}
	var enum []any
	for i := range op.Parameters {
		if op.Parameters[i].Name == "plugin_type" {
			enum = op.Parameters[i].Enum
		}
	}
	if enum == nil {
		t.Fatal("plugin_type parameter not found")
	}
	got := make([]string, 0, len(enum))
	for _, v := range enum {
		got = append(got, v.(string))
	}
	want := map[string]bool{"skill": true, "connector": true, "expert": true, "expert_team": true}
	if len(got) != len(want) {
		t.Fatalf("plugin_type enum = %v, want the four unified types", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected plugin_type %q", g)
		}
	}
}
