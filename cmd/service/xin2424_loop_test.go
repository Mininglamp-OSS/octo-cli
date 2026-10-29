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
		rowIDs: []string{"125"}, operationID: "workspace.member.add", method: "POST",
		path: "/fleet/api/v1/workspaces/{workspace_id}/members", risk: "high-risk-write",
		requiredHeaders: []string{"X-Workspace-ID"}, pathParams: []string{"workspace_id"},
		bodyRequired: true, requiredFields: []string{}, oneOfVariants: 2, successStatuses: []string{"200", "201"},
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

	op, _ := r.GetOperation("capability.install")
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
}

// XIN-2432: the ten Loop workspace and runtime management operations stay in
// the embedded spec but x-octo-cli-hidden, by owner decision, so no registry
// lookup, listing or generated command can reach them.
func TestXIN2432LoopManagementOperationsStayHidden(t *testing.T) {
	r := registry.MustNew()
	hidden := map[string]bool{
		"workspace.create": true, "workspace.update": true, "workspace.delete": true,
		"runtime.update": true, "runtime.delete": true, "runtime.archive_and_delete": true,
		"runtime.update_request.create": true, "runtime.model_request.create": true,
		"runtime.local_skill_request.create": true, "runtime.local_skill_import.create": true,
	}
	found := map[string]bool{}
	for _, item := range r.GetSpec("loop")["paths"].(map[string]any) {
		for _, raw := range item.(map[string]any) {
			op, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			id, _ := op["operationId"].(string)
			if !hidden[id] {
				continue
			}
			found[id] = true
			if op["x-octo-cli-hidden"] != true {
				t.Errorf("%s must carry x-octo-cli-hidden", id)
			}
		}
	}
	for id := range hidden {
		if !found[id] {
			t.Errorf("%s must stay in the embedded spec", id)
		}
		if _, ok := r.GetOperation(id); ok {
			t.Errorf("%s must not be exposed by the registry", id)
		}
	}
	for _, op := range r.ListAllOperations() {
		if hidden[op.ID] {
			t.Errorf("%s must not be listed", op.ID)
		}
	}
}

// XIN-2432 (review B1): expert_team.evaluate needs the team leader's agent and
// task execution, which Fleet reads from X-Agent-ID / X-Task-ID. Fleet does
// not strip those headers from a bf_ request, so modelling them as caller
// parameters would let a Bot claim an agent identity. No operation may declare
// them; task credentials supply them out of band.
func TestXIN2432AgentIdentityHeadersAreNeverCallerParameters(t *testing.T) {
	r := registry.MustNew()
	for _, info := range r.ListAllOperations() {
		op, ok := r.GetOperation(info.ID)
		if !ok {
			continue
		}
		for _, p := range op.Parameters {
			if strings.EqualFold(p.Name, "X-Agent-ID") || strings.EqualFold(p.Name, "X-Task-ID") {
				t.Errorf("%s declares %s %s as a caller parameter", op.ID, p.In, p.Name)
			}
		}
	}
	op, ok := r.GetOperation("expert_team.evaluate")
	if !ok {
		t.Fatal("expert_team.evaluate missing")
	}
	if !strings.Contains(op.Description, "Task-credential only") || !strings.Contains(op.Description, "Bot token cannot use this operation") {
		t.Errorf("expert_team.evaluate must say it is task-credential only: %q", op.Description)
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
