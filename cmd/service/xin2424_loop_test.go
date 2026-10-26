package service

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// xin2424LoopRow pins one Fleet public operation to the backend contract at
// octo-fleet 064c97e (route, handler decode, success status).
type xin2424LoopRow struct {
	rowIDs          []string
	operationID     string
	method          string
	path            string
	risk            string
	requiredHeaders []string
	pathParams      []string
	bodyRequired    bool     // requestBody.required
	requiredFields  []string // top-level required body fields (nil: no body declared)
	closedBody      bool     // additionalProperties: false at the top level
	oneOfVariants   int
	successStatuses []string
}

var xin2424LoopRows = []xin2424LoopRow{
	{
		rowIDs: []string{"90"}, operationID: "runtime.update", method: "PATCH",
		path: "/fleet/api/v1/runtimes/{runtime_id}", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		bodyRequired: true, requiredFields: []string{}, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"91"}, operationID: "runtime.delete", method: "DELETE",
		path: "/fleet/api/v1/runtimes/{runtime_id}", risk: "high-risk-write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"92"}, operationID: "runtime.archive_and_delete", method: "POST",
		path: "/fleet/api/v1/runtimes/{runtime_id}/archive_agents_and_delete", risk: "high-risk-write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		bodyRequired: true, requiredFields: []string{"expected_active_agent_ids"}, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"97"}, operationID: "runtime.update_request.create", method: "POST",
		path: "/fleet/api/v1/runtimes/{runtime_id}/updates", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		bodyRequired: true, requiredFields: []string{"target_version"}, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"99"}, operationID: "runtime.model_request.create", method: "POST",
		path: "/fleet/api/v1/runtimes/{runtime_id}/model_requests", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"101"}, operationID: "runtime.local_skill_request.create", method: "POST",
		path: "/fleet/api/v1/runtimes/{runtime_id}/local_skill_requests", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"103"}, operationID: "runtime.local_skill_import.create", method: "POST",
		path: "/fleet/api/v1/runtimes/{runtime_id}/local_skill_imports", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"runtime_id"},
		bodyRequired: true, requiredFields: []string{"skill_key"}, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"122", "160"}, operationID: "workspace.create", method: "POST",
		path: "/fleet/api/v1/workspaces", risk: "write",
		bodyRequired: true, requiredFields: []string{"name", "slug"}, successStatuses: []string{"201"},
	},
	{
		rowIDs: []string{"125"}, operationID: "workspace.member.add", method: "POST",
		path: "/fleet/api/v1/workspaces/{workspace_id}/members", risk: "high-risk-write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"workspace_id"},
		bodyRequired: true, requiredFields: []string{}, oneOfVariants: 2, successStatuses: []string{"200", "201"},
	},
	{
		rowIDs: []string{"126", "162"}, operationID: "workspace.update", method: "PATCH",
		path: "/fleet/api/v1/workspaces/{workspace_id}", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"workspace_id"},
		bodyRequired: true, requiredFields: []string{}, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"127", "163"}, operationID: "workspace.delete", method: "DELETE",
		path: "/fleet/api/v1/workspaces/{workspace_id}", risk: "high-risk-write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"workspace_id"},
		successStatuses: []string{"204"},
	},
	{
		rowIDs: []string{"128"}, operationID: "capability.install", method: "POST",
		path: "/fleet/api/v1/capabilities/install", risk: "write",
		requiredHeaders: []string{"Idempotency-Key", "X-Workspace-ID"},
		bodyRequired:    true, requiredFields: []string{"definition"}, closedBody: true, successStatuses: []string{"200"},
	},
	{
		rowIDs: []string{"33"}, operationID: "expert_team.evaluate", method: "POST",
		path: "/fleet/api/v1/tasks/{task_id}/expert_team_evaluations", risk: "write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"task_id"},
		bodyRequired: true, requiredFields: []string{"outcome"}, closedBody: true, successStatuses: []string{"201"},
	},
}

func TestXIN2424LoopOperationContracts(t *testing.T) {
	r := registry.MustNew()
	spec := r.GetSpec("loop")
	paths := spec["paths"].(map[string]any)
	for _, row := range xin2424LoopRows {
		t.Run(row.operationID, func(t *testing.T) {
			op, ok := r.GetOperation(row.operationID)
			if !ok {
				t.Fatalf("rows %v: operation not exposed", row.rowIDs)
			}
			if op.Service != "loop" || op.Method != row.method || op.Path != row.path {
				t.Fatalf("got %s %s %s, want loop %s %s", op.Service, op.Method, op.Path, row.method, row.path)
			}
			if op.Risk != row.risk {
				t.Errorf("risk = %q, want %q", op.Risk, row.risk)
			}
			if op.Multipart || op.BinaryResponse {
				t.Errorf("multipart=%v binary=%v, want JSON operation", op.Multipart, op.BinaryResponse)
			}

			var headers, pathParams []string
			for _, p := range op.Parameters {
				switch {
				case p.In == "header" && p.Required:
					headers = append(headers, p.Name)
				case p.In == "path":
					if !p.Required {
						t.Errorf("path parameter %s must be required", p.Name)
					}
					pathParams = append(pathParams, p.Name)
				}
			}
			sort.Strings(headers)
			if !xin2424EqualStrings(headers, row.requiredHeaders) {
				t.Errorf("required headers = %v, want %v", headers, row.requiredHeaders)
			}
			if !xin2424EqualStrings(pathParams, row.pathParams) {
				t.Errorf("path params = %v, want %v", pathParams, row.pathParams)
			}

			if row.requiredFields == nil {
				if op.RequestBody != nil {
					t.Errorf("handler reads no body, but a request body is declared")
				}
			} else {
				if op.RequestBody == nil || op.RequestBodyRequired != row.bodyRequired {
					t.Fatalf("request body = %+v required=%v, want required=%v", op.RequestBody, op.RequestBodyRequired, row.bodyRequired)
				}
				got := append([]string{}, op.RequestBody.Required...)
				sort.Strings(got)
				want := append([]string{}, row.requiredFields...)
				sort.Strings(want)
				if !xin2424EqualStrings(got, want) {
					t.Errorf("required body fields = %v, want %v", got, want)
				}
				closed := op.RequestBody.AdditionalProperties != nil && !*op.RequestBody.AdditionalProperties
				if closed != row.closedBody {
					t.Errorf("closed body = %v, want %v", closed, row.closedBody)
				}
				if len(op.RequestBody.OneOf) != row.oneOfVariants {
					t.Errorf("oneOf variants = %d, want %d", len(op.RequestBody.OneOf), row.oneOfVariants)
				}
			}

			raw := paths[row.path].(map[string]any)[strings.ToLower(row.method)].(map[string]any)
			if _, hidden := raw["x-octo-cli-hidden"]; hidden {
				t.Errorf("operation must not carry x-octo-cli-hidden")
			}
			var success []string
			for code := range raw["responses"].(map[string]any) {
				if strings.HasPrefix(code, "2") {
					success = append(success, code)
				}
			}
			sort.Strings(success)
			if !xin2424EqualStrings(success, row.successStatuses) {
				t.Errorf("success statuses = %v, want %v", success, row.successStatuses)
			}
		})
	}
}

func xin2424EqualStrings(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func TestXIN2424LoopEnumsAndFieldTypes(t *testing.T) {
	r := registry.MustNew()
	cases := []struct {
		operationID, field string
		enum               []any
	}{
		{"runtime.update", "visibility", []any{"private", "workspace"}},
		{"runtime.local_skill_import.create", "action", []any{"", "overwrite"}},
		{"workspace.member.add", "principal_type", []any{"user", "bot"}},
		{"workspace.member.add", "role", []any{"admin", "member"}},
		{"expert_team.evaluate", "outcome", []any{"action", "no_action", "failed"}},
	}
	for _, tc := range cases {
		op, ok := r.GetOperation(tc.operationID)
		if !ok {
			t.Fatalf("%s: not found", tc.operationID)
		}
		field, ok := op.RequestBody.Properties[tc.field]
		if !ok {
			t.Fatalf("%s: body field %s missing", tc.operationID, tc.field)
		}
		if !reflect.DeepEqual(field.Enum, tc.enum) {
			t.Errorf("%s.%s enum = %v, want %v", tc.operationID, tc.field, field.Enum, tc.enum)
		}
	}

	op, _ := r.GetOperation("runtime.update")
	if got := op.RequestBody.Properties["custom_name"].MaxLength; got != 100 {
		t.Errorf("runtime.update custom_name maxLength = %d, want 100", got)
	}
	if got := op.RequestBody.Properties["apply_to_machine"].Type; got != "boolean" {
		t.Errorf("runtime.update apply_to_machine type = %q", got)
	}
	op, _ = r.GetOperation("runtime.archive_and_delete")
	ids := op.RequestBody.Properties["expected_active_agent_ids"]
	if ids.Type != "array" || ids.Items == nil || ids.Items.Format != "uuid" {
		t.Errorf("expected_active_agent_ids = %+v, want array of uuid", ids)
	}
	op, _ = r.GetOperation("capability.install")
	definition := op.RequestBody.Properties["definition"]
	if !reflect.DeepEqual(definition.Required, []string{"schema_version", "name"}) || len(definition.AnyOf) != 2 {
		t.Errorf("definition required=%v anyOf=%d", definition.Required, len(definition.AnyOf))
	}
	if definition.Properties["schema_version"].Const != "1.0" {
		t.Errorf("schema_version const = %v", definition.Properties["schema_version"].Const)
	}
	var idempotency *registry.ParamInfo
	for i := range op.Parameters {
		if op.Parameters[i].Name == "Idempotency-Key" {
			idempotency = &op.Parameters[i]
		}
	}
	if idempotency == nil || idempotency.In != "header" || !idempotency.Required ||
		idempotency.MinLength != 1 || idempotency.MaxLength != 200 || idempotency.FlagName != "idempotency-key" {
		t.Errorf("Idempotency-Key parameter = %+v", idempotency)
	}
	op, _ = r.GetOperation("workspace.create")
	for _, p := range op.Parameters {
		if p.Name == "X-Workspace-ID" && (p.Required || p.FlagName != "workspace-id") {
			t.Errorf("workspace.create X-Workspace-ID must be optional with --workspace-id: %+v", p)
		}
	}
}

// The previous singular path was never registered by Fleet.
func TestXIN2424LoopTeamEvaluationSingularPathRemoved(t *testing.T) {
	paths := registry.MustNew().GetSpec("loop")["paths"].(map[string]any)
	if _, exists := paths["/fleet/api/v1/tasks/{task_id}/expert_team_evaluation"]; exists {
		t.Fatal("singular expert_team_evaluation path must not be declared")
	}
	for _, alias := range []string{"/fleet/api/v1/workspaces/", "/fleet/api/v1/workspaces/{workspace_id}/"} {
		if _, exists := paths[alias]; exists {
			t.Fatalf("trailing-slash alias %s must not be declared as a separate path", alias)
		}
	}
}

type xin2424Captured struct {
	method, path, workspace, idempotency string
	body                                 map[string]any
	rawBody                              string
}

func TestXIN2424LoopRequestAssembly(t *testing.T) {
	const ws = "11111111-1111-1111-1111-111111111111"
	const rt = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name     string
		args     []string
		method   string
		path     string
		body     map[string]any
		noBody   bool
		idemKey  string
		response string
	}{
		{
			name: "workspace create", args: []string{"loop", "workspace", "create", "--name", "Demo", "--slug", "demo", "--workspace-id", ws},
			method: "POST", path: "/fleet/api/v1/workspaces", body: map[string]any{"name": "Demo", "slug": "demo"},
			response: `{"id":"` + ws + `","name":"Demo","slug":"demo","issue_prefix":"DEM","created_at":"x","updated_at":"x"}`,
		},
		{
			name: "workspace member principal", args: []string{"loop", "workspace", "member", "add", ws, "--workspace-id", ws, "--principal-type", "bot", "--principal-id", "uid-1"},
			method: "POST", path: "/fleet/api/v1/workspaces/" + ws + "/members", body: map[string]any{"principal_type": "bot", "principal_id": "uid-1"},
			response: `{"id":"m"}`,
		},
		{
			name: "workspace member invitation", args: []string{"loop", "workspace", "member", "add", ws, "--workspace-id", ws, "--email", "a@example.com", "--role", "admin"},
			method: "POST", path: "/fleet/api/v1/workspaces/" + ws + "/members", body: map[string]any{"email": "a@example.com", "role": "admin"},
			response: `{"id":"i"}`,
		},
		{
			name: "runtime archive and delete", args: []string{"loop", "runtime", "archive-and-delete", rt, "--workspace-id", ws, "--data", `{"expected_active_agent_ids":[]}`},
			method: "POST", path: "/fleet/api/v1/runtimes/" + rt + "/archive_agents_and_delete", body: map[string]any{"expected_active_agent_ids": []any{}},
			response: `{"data":{"status":"ok","agents_archived":0,"tasks_cancelled":0}}`,
		},
		{
			name: "runtime model request", args: []string{"loop", "runtime", "model-request", "create", rt, "--workspace-id", ws},
			method: "POST", path: "/fleet/api/v1/runtimes/" + rt + "/model_requests", noBody: true,
			response: `{"data":{"model_request_id":"m","runtime_id":"` + rt + `","status":"pending","supported":true}}`,
		},
		{
			name: "team evaluation", args: []string{"loop", "expert-team", "evaluate", rt, "--workspace-id", ws, "--outcome", "no_action"},
			method: "POST", path: "/fleet/api/v1/tasks/" + rt + "/expert_team_evaluations", body: map[string]any{"outcome": "no_action"},
			response: `{"data":{"id":"a","action":"squad_leader_evaluated","created_at":"x"}}`,
		},
		{
			name: "capability install", args: []string{"loop", "capability", "install", "--workspace-id", ws, "--idempotency-key", "key-1",
				"--data", `{"definition":{"schema_version":"1.0","name":"pack","skills":[{"name":"s","content":"c"}]}}`},
			method: "POST", path: "/fleet/api/v1/capabilities/install", idemKey: "key-1",
			body: map[string]any{"definition": map[string]any{"schema_version": "1.0", "name": "pack",
				"skills": []any{map[string]any{"name": "s", "content": "c"}}}},
			response: `{"data":{"type":"skills","experts":[],"skills":[]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var got xin2424Captured
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				got = xin2424Captured{method: r.Method, path: r.URL.Path, workspace: r.Header.Get("X-Workspace-ID"),
					idempotency: r.Header.Get("Idempotency-Key"), rawBody: string(raw)}
				if len(raw) > 0 {
					_ = json.Unmarshal(raw, &got.body)
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if got.method != tc.method || got.path != tc.path {
				t.Fatalf("request = %s %s, want %s %s", got.method, got.path, tc.method, tc.path)
			}
			if got.workspace != ws {
				t.Errorf("X-Workspace-ID = %q, want %q", got.workspace, ws)
			}
			if got.idempotency != tc.idemKey {
				t.Errorf("Idempotency-Key = %q, want %q", got.idempotency, tc.idemKey)
			}
			if tc.noBody {
				if got.rawBody != "" {
					t.Errorf("body = %q, want none", got.rawBody)
				}
				return
			}
			if !reflect.DeepEqual(got.body, tc.body) {
				t.Errorf("body = %#v, want %#v", got.body, tc.body)
			}
		})
	}
}

func TestXIN2424LoopRejectsInvalidBodiesLocally(t *testing.T) {
	const ws = "11111111-1111-1111-1111-111111111111"
	const rt = "22222222-2222-2222-2222-222222222222"
	cases := []struct {
		name string
		args []string
	}{
		{"member email with principal field", []string{"loop", "workspace", "member", "add", ws, "--workspace-id", ws, "--email", "a@example.com", "--principal-id", "uid-1"}},
		{"member principal without type", []string{"loop", "workspace", "member", "add", ws, "--workspace-id", ws, "--principal-id", "uid-1"}},
		{"member owner role", []string{"loop", "workspace", "member", "add", ws, "--workspace-id", ws, "--email", "a@example.com", "--role", "owner"}},
		{"runtime update without fields", []string{"loop", "runtime", "update", rt, "--workspace-id", ws}},
		{"runtime visibility outside enum", []string{"loop", "runtime", "update", rt, "--workspace-id", ws, "--visibility", "public"}},
		{"archive without snapshot", []string{"loop", "runtime", "archive-and-delete", rt, "--workspace-id", ws}},
		{"update request without version", []string{"loop", "runtime", "update-request", "create", rt, "--workspace-id", ws}},
		{"local skill import without key", []string{"loop", "runtime", "local-skill-import", "create", rt, "--workspace-id", ws}},
		{"workspace create without slug", []string{"loop", "workspace", "create", "--name", "Demo"}},
		{"evaluation outcome outside enum", []string{"loop", "expert-team", "evaluate", rt, "--workspace-id", ws, "--outcome", "done"}},
		{"evaluation unknown field", []string{"loop", "expert-team", "evaluate", rt, "--workspace-id", ws, "--data", `{"outcome":"action","extra":1}`}},
		{"capability unknown field", []string{"loop", "capability", "install", "--workspace-id", ws, "--idempotency-key", "k",
			"--data", `{"definition":{"schema_version":"1.0","name":"p","skills":[{"name":"s","content":"c"}]},"extra":1}`}},
		{"capability empty definition", []string{"loop", "capability", "install", "--workspace-id", ws, "--idempotency-key", "k",
			"--data", `{"definition":{"schema_version":"1.0","name":"p"}}`}},
		{"capability wrong schema version", []string{"loop", "capability", "install", "--workspace-id", ws, "--idempotency-key", "k",
			"--data", `{"definition":{"schema_version":"2.0","name":"p","skills":[{"name":"s","content":"c"}]}}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
				t.Errorf("invalid request must not be sent")
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err == nil {
				t.Fatal("expected local validation error")
			}
		})
	}
}
