package service

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestMarketplaceRegistryShape(t *testing.T) {
	r := registry.MustNew()
	wants := map[string]struct {
		method string
		path   string
	}{
		"skill_category.list":      {http.MethodGet, "/market/api/v1/skill_categories"},
		"mcp_category.list":        {http.MethodGet, "/market/api/v1/mcp_categories"},
		"skill.list":               {http.MethodGet, "/market/api/v1/skills"},
		"skill.mine.list":          {http.MethodGet, "/market/api/v1/skills/mine"},
		"skill.tag.list":           {http.MethodGet, "/market/api/v1/skills/tags"},
		"skill.publish":            {http.MethodPost, "/market/api/v1/bot/skills/publish"},
		"skill.create":             {http.MethodPost, "/market/api/v1/skills"},
		"skill.get":                {http.MethodGet, "/market/api/v1/skills/{skill_id}"},
		"skill.update":             {http.MethodPatch, "/market/api/v1/skills/{skill_id}"},
		"skill.delete":             {http.MethodDelete, "/market/api/v1/skills/{skill_id}"},
		"skill.skillmd.get":        {http.MethodGet, "/market/api/v1/skills/{skill_id}/skill_md"},
		"skill.download":           {http.MethodGet, "/market/api/v1/skills/{skill_id}/download"},
		"skill.reupload.create":    {http.MethodPost, "/market/api/v1/skills/{skill_id}/reuploads"},
		"skill.version.list":       {http.MethodGet, "/market/api/v1/skills/{skill_id}/versions"},
		"skill_upload.create":      {http.MethodPost, "/market/api/v1/skill_uploads"},
		"skill_upload.parse":       {http.MethodPost, "/market/api/v1/skill_uploads/{skill_upload_id}/parse"},
		"skill_parse_task.get":     {http.MethodGet, "/market/api/v1/skill_parse_tasks/{skill_parse_task_id}"},
		"skill_icon_upload.create": {http.MethodPost, "/market/api/v1/skill_icon_uploads"},
		"mcp.list":                 {http.MethodGet, "/market/api/v1/mcps"},
		"mcp.mine.list":            {http.MethodGet, "/market/api/v1/mcps/mine"},
		"mcp.create":               {http.MethodPost, "/market/api/v1/mcps"},
		"mcp.probe":                {http.MethodPost, "/market/api/v1/mcps/_probe"},
		"mcp.get":                  {http.MethodGet, "/market/api/v1/mcps/{mcp_id}"},
		"mcp.update":               {http.MethodPatch, "/market/api/v1/mcps/{mcp_id}"},
		"mcp.delete":               {http.MethodDelete, "/market/api/v1/mcps/{mcp_id}"},
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
}

func TestMarketplaceSkillSearchRequest(t *testing.T) {
	var gotPath, gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"has_more":false}}`))
	})

	root.SetArgs([]string{"marketplace", "skill", "list", "--q", "deep miner", "--tag", "cli", "--sort", "latest", "--page-size", "20"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/skills" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"q=deep+miner", "tag=cli", "sort=latest", "page_size=20"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, want %q", gotQuery, want)
		}
	}
	if strings.Contains(gotQuery, "offset=") {
		t.Errorf("query = %q, must not use removed offset pagination", gotQuery)
	}
}

func TestMarketplaceMCPSearchFiltersRequest(t *testing.T) {
	var gotQuery map[string][]string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
	})

	root.SetArgs([]string{"marketplace", "mcp", "list", "--transport", "stdio", "--visibility", "public", "--source", "space", "--created-by-type", "bot", "--tag", "cli", "--tag", "devops", "--sort", "relevance"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := map[string][]string{
		"transport":       {"stdio"},
		"visibility":      {"public"},
		"source":          {"space"},
		"created_by_type": {"bot"},
		"tag":             {"cli", "devops"},
		"sort":            {"relevance"},
	}
	if !reflect.DeepEqual(gotQuery, want) {
		t.Errorf("query = %#v, want %#v", gotQuery, want)
	}
}

func TestMarketplaceMCPCategoryFiltersRequest(t *testing.T) {
	var gotQuery string
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	root.SetArgs([]string{"marketplace", "mcp-category", "list", "--mode", "mine", "--created-by-type", "bot"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"mode=mine", "created_by_type=bot"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q, want %q", gotQuery, want)
		}
	}
}

func TestMarketplaceSkillPublishRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"skill_id":"skill-1"}}`))
	})

	root.SetArgs([]string{"marketplace", "skill", "create", "--parse-task-id", "task-1", "--visibility", "space"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/skills" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["parse_task_id"] != "task-1" || gotBody["visibility"] != "space" {
		t.Errorf("body = %#v", gotBody)
	}
}

func TestMarketplaceBotSkillPublishRequest(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"skill_id":"skill-1"}}`))
	})

	root.SetArgs([]string{"marketplace", "skill", "publish", "--skill-upload-id", "upload-1", "--visibility", "space", "--changelog", "Initial release"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/bot/skills/publish" {
		t.Errorf("path = %q", gotPath)
	}
	if gotBody["skill_upload_id"] != "upload-1" || gotBody["visibility"] != "space" || gotBody["changelog"] != "Initial release" {
		t.Errorf("body = %#v", gotBody)
	}
}

func TestMarketplaceMCPUpdateRequest(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"mcp_id":"mcp-1"}}`))
	})

	root.SetArgs([]string{"marketplace", "mcp", "update", "mcp-1", "--slogan", "Updated"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPatch || gotPath != "/market/api/v1/mcps/mcp-1" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if gotBody["slogan"] != "Updated" {
		t.Errorf("body = %#v", gotBody)
	}
}

func TestMarketplaceCommandTree(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {})
	marketplace := findCmd(root, "marketplace")
	if marketplace == nil {
		t.Fatal("missing marketplace command")
	}
	domains := map[string][]string{
		"skill-category":    {"list"},
		"mcp-category":      {"list"},
		"skill":             {"list", "mine", "tag", "publish", "create", "get", "update", "delete", "download", "reupload", "version", "skillmd"},
		"skill-upload":      {"create", "parse"},
		"skill-parse-task":  {"get"},
		"skill-icon-upload": {"create"},
		"mcp":               {"list", "mine", "create", "probe", "get", "update", "delete"},
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
	if got := childNames(marketplace); len(got) != len(domains) {
		t.Errorf("marketplace domains = %v, want %v", got, domains)
	}
}

func TestMarketplaceSkillTagsAreStrings(t *testing.T) {
	r := registry.MustNew()
	for _, id := range []string{"skill.publish", "skill.create", "skill.update"} {
		op, ok := r.GetOperation(id)
		if !ok || op.RequestBody == nil {
			t.Fatalf("%s request body not found", id)
		}
		tags, ok := op.RequestBody.Properties["tags"]
		if !ok || tags.Type != "array" || tags.Items == nil || tags.Items.Type != "string" {
			t.Errorf("%s tags schema = %+v, want string array", id, tags)
		}
	}
}

func TestMarketplaceVisibilityEnumsMatchBackend(t *testing.T) {
	r := registry.MustNew()
	for _, tc := range []struct {
		id   string
		want []string
	}{
		{"skill.create", []string{"public", "private", "space"}},
		{"skill.update", []string{"public", "private", "space"}},
		{"skill.publish", []string{"public", "private", "space"}},
		{"mcp.create", []string{"public", "private"}},
		{"mcp.update", []string{"public", "private"}},
	} {
		op, ok := r.GetOperation(tc.id)
		if !ok || op.RequestBody == nil {
			t.Fatalf("%s request body not found", tc.id)
		}
		visibility, ok := op.RequestBody.Properties["visibility"]
		if !ok {
			t.Fatalf("%s visibility schema not found", tc.id)
		}
		got := make([]string, 0, len(visibility.Enum))
		for _, value := range visibility.Enum {
			got = append(got, value.(string))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s visibility enum = %v, want %v", tc.id, visibility.Enum, tc.want)
		}
	}
}

func TestMarketplaceDownloadRequest(t *testing.T) {
	var gotPath, gotQuery, gotSpace string
	root, _, _ := rootWithServiceSpaced(t, "space-1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotSpace = r.Header.Get("X-Space-Id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"download_url":"https://example.invalid/skill.zip","file_sha256":"abc"}}`))
	})

	root.SetArgs([]string{"marketplace", "skill", "download", "skill-1", "--response-format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotPath != "/market/api/v1/skills/skill-1/download" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "format=json") {
		t.Errorf("query = %q, want format=json", gotQuery)
	}
	if gotSpace != "space-1" {
		t.Errorf("X-Space-Id = %q, want space-1", gotSpace)
	}
}
