package registry

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestNewLoadsAllServices(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := r.ListServices()
	want := []string{"bot", "docs", "drive", "event", "file", "group", "html", "loop", "mail", "marketplace", "matter", "message", "summary", "thread"}
	if len(got) != len(want) {
		t.Fatalf("ListServices: got %d services, want %d (%v)", len(got), len(want), got)
	}
	for i, s := range want {
		if got[i] != s {
			t.Errorf("ListServices[%d]: got %q, want %q", i, got[i], s)
		}
	}
}

func TestGetSpecReturnsNilForUnknown(t *testing.T) {
	r := MustNew()
	if spec := r.GetSpec("nosuch"); spec != nil {
		t.Fatalf("GetSpec(nosuch): got non-nil %v", spec)
	}
}

func TestAllDomainOperationCounts(t *testing.T) {
	// Caller-facing operation counts per service. Operations marked
	// x-octo-cli-hidden remain in the embedded backend spec but are excluded
	// here; marketplace skill.create is one such Web-only operation.
	r := MustNew()
	expected := map[string]int{
		"matter":      14,
		"message":     10,
		"group":       9,
		"thread":      8,
		"file":        4,
		"bot":         6,
		"event":       2,
		"docs":        32,
		"drive":       43,
		"html":        21,
		"marketplace": 17,
		"mail":        18,
		"summary":     4,
		"loop":        126,
	}
	totalWant := 0
	for svc, want := range expected {
		totalWant += want
		got := len(r.ListOperations(svc))
		if got != want {
			t.Errorf("%s: got %d ops (%v), want %d", svc, got, operationIDs(r.ListOperations(svc)), want)
		}
	}
	all := r.ListAllOperations()
	if len(all) != totalWant {
		t.Errorf("ListAllOperations: got %d, want %d", len(all), totalWant)
	}
}

func TestLoopWorkspaceManagementOperationsAreHidden(t *testing.T) {
	r := MustNew()
	hidden := []string{"workspace.create", "workspace.update", "workspace.delete"}
	for _, operationID := range hidden {
		if _, ok := r.GetOperation(operationID); ok {
			t.Fatalf("%s must not be exposed by the CLI registry", operationID)
		}
		for _, op := range r.ListOperations("loop") {
			if op.ID == operationID {
				t.Fatalf("%s must not be listed as a Loop operation", operationID)
			}
		}
	}
}

func TestLoopRuntimeMutationOperationsAreHidden(t *testing.T) {
	r := MustNew()
	hidden := []string{
		"runtime.update",
		"runtime.delete",
		"runtime.archive_and_delete",
		"runtime.update_request.create",
		"runtime.model_request.create",
		"runtime.local_skill_request.create",
		"runtime.local_skill_import.create",
	}
	for _, operationID := range hidden {
		if _, ok := r.GetOperation(operationID); ok {
			t.Fatalf("%s must not be exposed by the CLI registry", operationID)
		}
		for _, op := range r.ListOperations("loop") {
			if op.ID == operationID {
				t.Fatalf("%s must not be listed as a Loop operation", operationID)
			}
		}
	}
}

func TestLoopRuntimeListIsSpaceScopedAndExpertsAcceptRuntimeRef(t *testing.T) {
	r := MustNew()
	list, ok := r.GetOperation("runtime.list")
	if !ok {
		t.Fatal("runtime.list not found")
	}
	for _, parameter := range list.Parameters {
		if parameter.In == "header" && parameter.Name == "X-Workspace-ID" {
			t.Fatal("runtime.list must not require a Workspace selector")
		}
	}

	for _, operationID := range []string{"expert.create", "expert.update"} {
		op, ok := r.GetOperation(operationID)
		if !ok || op.RequestBody == nil {
			t.Fatalf("%s request body not found", operationID)
		}
		if _, ok := op.RequestBody.Properties["runtime_ref"]; !ok {
			t.Fatalf("%s must accept the Space-level runtime_ref", operationID)
		}
	}
}

func TestLoopPublicContractResolvesSharedComponents(t *testing.T) {
	r := MustNew()
	create, ok := r.GetOperation("task.create")
	if !ok {
		t.Fatal("task.create: not found")
	}
	if create.Path != "/fleet/api/v1/tasks" || create.BaseURLEnv != "OCTO_API_BASE_URL" {
		t.Fatalf("task.create route = %s (%s)", create.Path, create.BaseURLEnv)
	}
	if create.RequestBody == nil {
		t.Fatal("task.create request body ref was not resolved")
	}
	if _, ok := create.RequestBody.Properties["title"]; !ok {
		t.Fatalf("task.create body properties = %v", create.RequestBody.Properties)
	}

	list, ok := r.GetOperation("task.list")
	if !ok {
		t.Fatal("task.list: not found")
	}
	if len(list.Parameters) < 2 || list.Parameters[0].Name == "" {
		t.Fatalf("task.list parameter refs were not resolved: %+v", list.Parameters)
	}
}

func TestLoopExtendedBusinessContract(t *testing.T) {
	r := MustNew()
	for _, id := range []string{
		"workspace.list",
		"label.create",
		"project.resource.create",
		"comment.reaction.add",
		"attachment.upload",
		"loop.skill.list",
		"runtime.update_request.get",
		"autopilot.delivery.replay",
		"task.team_evaluation.record",
	} {
		op, ok := r.GetOperation(id)
		if !ok {
			t.Errorf("%s: not found", id)
			continue
		}
		if op.Service != "loop" || !strings.HasPrefix(op.Path, "/fleet/api/v1/") {
			t.Errorf("%s: got service=%q path=%q", id, op.Service, op.Path)
		}
	}

	op, ok := r.GetOperation("label.list")
	if !ok {
		t.Fatal("label.list: not found")
	}
	foundWorkspaceHeader := false
	for _, parameter := range op.Parameters {
		if parameter.Name == "X-Workspace-ID" && parameter.In == "header" && parameter.FlagName == "workspace-id" {
			foundWorkspaceHeader = true
		}
	}
	if !foundWorkspaceHeader {
		t.Errorf("label.list parameters = %+v, want X-Workspace-ID workspace-id flag", op.Parameters)
	}
}

func TestLoopTaskUpdateIncludesReviewStatus(t *testing.T) {
	r := MustNew()
	update, ok := r.GetOperation("task.update")
	if !ok || update.RequestBody == nil {
		t.Fatal("task.update typed request body not found")
	}
	status, ok := update.RequestBody.Properties["status"]
	if !ok {
		t.Fatalf("task.update status schema missing: %+v", update.RequestBody.Properties)
	}
	want := []any{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"}
	if !reflect.DeepEqual(status.Enum, want) {
		t.Fatalf("task.update status enum = %#v, want %#v", status.Enum, want)
	}
}

func TestLoopAutopilotUsesPublicTypedContract(t *testing.T) {
	r := MustNew()
	create, ok := r.GetOperation("autopilot.create")
	if !ok || create.RequestBody == nil {
		t.Fatal("autopilot.create typed request body not found")
	}
	for _, field := range []string{"title", "assignee_id", "dispatch_mode", "task_title_template"} {
		property, ok := create.RequestBody.Properties[field]
		if !ok || property.Type != "string" {
			t.Errorf("autopilot.create %s = %+v, want string", field, property)
		}
	}
	dispatch := create.RequestBody.Properties["dispatch_mode"]
	if !reflect.DeepEqual(dispatch.Enum, []any{"create_task", "direct_execution"}) {
		t.Fatalf("dispatch_mode enum = %#v", dispatch.Enum)
	}
	if create.RequestBody.AdditionalProperties == nil || *create.RequestBody.AdditionalProperties {
		t.Fatalf("autopilot.create additionalProperties = %v, want false", create.RequestBody.AdditionalProperties)
	}
	for _, legacy := range []string{"execution_mode", "issue_title_template"} {
		if _, ok := create.RequestBody.Properties[legacy]; ok {
			t.Errorf("autopilot.create exposes legacy field %q", legacy)
		}
	}

	update, ok := r.GetOperation("autopilot.update")
	if !ok || update.RequestBody == nil {
		t.Fatal("autopilot.update typed request body not found")
	}
	if _, ok := update.RequestBody.Properties["dispatch_mode"]; !ok {
		t.Fatalf("autopilot.update request = %+v", update.RequestBody.Properties)
	}
	if update.RequestBody.MinProperties != 1 {
		t.Fatalf("autopilot.update minProperties = %d, want 1", update.RequestBody.MinProperties)
	}

	trigger, ok := r.GetOperation("autopilot.trigger")
	if !ok {
		t.Fatal("autopilot.trigger not found")
	}
	if trigger.RequestBody != nil {
		t.Fatalf("autopilot.trigger must not expose an unused request body: %+v", trigger.RequestBody)
	}

	get, ok := r.GetOperation("autopilot.get")
	if !ok || get.ResponseSchema == nil {
		t.Fatal("autopilot.get typed response not found")
	}
	data, ok := get.ResponseSchema.Properties["data"]
	if !ok {
		t.Fatalf("autopilot.get data schema = %+v", data)
	}
	for _, field := range []string{"autopilot_id", "dispatch_mode", "task_title_template", "triggers"} {
		if _, ok := data.Properties[field]; !ok {
			t.Errorf("autopilot.get data schema missing %s: %+v", field, data.Properties)
		}
	}
}

func TestLoopComposedRequestSchemas(t *testing.T) {
	r := MustNew()
	member, ok := r.GetOperation("expert_team.member.add")
	if !ok || member.RequestBody == nil {
		t.Fatal("expert_team.member.add request body not found")
	}
	for _, field := range []string{"member_type", "member_id", "role"} {
		if _, ok := member.RequestBody.Properties[field]; !ok {
			t.Errorf("composed member request missing %s: %+v", field, member.RequestBody)
		}
	}

	secret, ok := r.GetOperation("autopilot.signing_secret.set")
	if !ok || secret.RequestBody == nil {
		t.Fatal("autopilot.signing_secret.set request body not found")
	}
	property := secret.RequestBody.Properties["signing_secret"]
	if !property.WriteOnly || len(property.AnyOf) != 2 {
		t.Fatalf("signing_secret schema = %+v, want writeOnly anyOf", property)
	}

	quickCreate, ok := r.GetOperation("task.quick_create")
	if !ok || quickCreate.ResponseSchema == nil {
		t.Fatal("task.quick_create referenced 202 response schema not resolved")
	}
}

func TestLoopNestedReferencedRequestSchemasAreResolved(t *testing.T) {
	r := MustNew()
	trigger, ok := r.GetOperation("autopilot.trigger_config.create")
	if !ok || trigger.RequestBody == nil {
		t.Fatal("autopilot.trigger_config.create request body not found")
	}
	eventFilters := trigger.RequestBody.Properties["event_filters"]
	if eventFilters.Items == nil || eventFilters.Items.Ref != "" {
		t.Fatalf("event_filters item schema was not resolved: %+v", eventFilters.Items)
	}
	if eventFilters.Items.AdditionalProperties == nil || *eventFilters.Items.AdditionalProperties {
		t.Fatalf("event_filters items must reject unknown fields: %+v", eventFilters.Items)
	}
	if !reflect.DeepEqual(eventFilters.Items.Required, []string{"event"}) {
		t.Fatalf("event_filters item required fields = %#v", eventFilters.Items.Required)
	}
}

func TestLoopWorkspaceScopedOperationsExposeWorkspaceIDFlag(t *testing.T) {
	r := MustNew()
	for _, info := range r.ListOperations("loop") {
		op, ok := r.GetOperation(info.ID)
		if !ok {
			t.Fatalf("%s: operation disappeared", info.ID)
		}

		workspaceHeaders := 0
		requiredWorkspaceHeaders := 0
		for _, parameter := range op.Parameters {
			if parameter.Name == "X-Workspace-ID" && parameter.In == "header" && parameter.FlagName == "workspace-id" {
				workspaceHeaders++
				if parameter.Required {
					requiredWorkspaceHeaders++
				}
			}
		}

		// Workspace collection and logical Runtime list operations are
		// Space-scoped. Workspace path operations and every other Fleet business
		// route must send the header selector used before member validation.
		wantHeader := strings.HasPrefix(info.Path, "/fleet/api/v1/") &&
			info.Path != "/fleet/api/v1/workspaces" &&
			info.Path != "/fleet/api/v1/runtimes"
		if wantHeader && workspaceHeaders != 1 {
			t.Errorf("%s: workspace headers = %d, want exactly one", info.ID, workspaceHeaders)
		}
		if wantHeader && requiredWorkspaceHeaders != 1 {
			t.Errorf("%s: required workspace headers = %d, want exactly one", info.ID, requiredWorkspaceHeaders)
		}
		if !wantHeader && workspaceHeaders != 0 {
			t.Errorf("%s: workspace headers = %d, want none", info.ID, workspaceHeaders)
		}
	}
}

func TestLoopOperationsDeclareRisk(t *testing.T) {
	r := MustNew()
	for _, info := range r.ListOperations("loop") {
		if info.Risk == "" {
			t.Errorf("%s does not declare x-octo-risk", info.ID)
		}
	}
}

func TestLoopSpecUsesSupportedBearerAuthentication(t *testing.T) {
	raw, err := specsFS.ReadFile("specs/loop.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"octo_session"`)) {
		t.Fatal("Loop CLI contract advertises unsupported octo_session authentication")
	}
}

func TestGetOperationMatterCreate(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("matter.create")
	if !ok {
		t.Fatal("GetOperation(matter.create): not found")
	}
	if op.Method != "POST" {
		t.Errorf("method: got %q, want POST", op.Method)
	}
	if op.Path != "/api/v1/matters" {
		t.Errorf("path: got %q, want /api/v1/matters", op.Path)
	}
	if op.Risk != "write" {
		t.Errorf("risk: got %q, want write", op.Risk)
	}
	if op.BaseURLEnv != "OCTO_API_BASE_URL" {
		t.Errorf("base url env: got %q, want OCTO_API_BASE_URL", op.BaseURLEnv)
	}
	if !op.SpaceHeader {
		t.Error("space header: want true for matter domain")
	}
	if op.RequestBody == nil {
		t.Fatal("request body: nil")
	}
	if _, ok := op.RequestBody.Properties["title"]; !ok {
		t.Errorf("request body: missing title property; got %v", op.RequestBody.Properties)
	}
	hasRequired := false
	for _, r := range op.RequestBody.Required {
		if r == "title" {
			hasRequired = true
			break
		}
	}
	if !hasRequired {
		t.Errorf("request body required: want [title], got %v", op.RequestBody.Required)
	}
}

func TestHTMLOperationsUseDocsHTMLGatewayPrefix(t *testing.T) {
	r := MustNew()
	for _, op := range r.ListOperations("html") {
		if want := "/docs-html/v1/"; !strings.HasPrefix(op.Path, want) {
			t.Errorf("%s: path = %q, want prefix %q", op.ID, op.Path, want)
		}
	}
}

func TestGetOperationMatterList_Pagination(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("matter.list")
	if !ok {
		t.Fatal("matter.list not found")
	}
	if op.Pagination == nil {
		t.Fatal("pagination: nil, want non-nil")
	}
	if op.Pagination.CursorParam != "cursor" || op.Pagination.LimitParam != "limit" {
		t.Errorf("pagination: got %+v", op.Pagination)
	}
	foundStatus := false
	for _, p := range op.Parameters {
		if p.Name == "status" && p.In == "query" {
			foundStatus = true
			if len(p.Enum) != 3 {
				t.Errorf("status enum: got %d values, want 3", len(p.Enum))
			}
		}
	}
	if !foundStatus {
		t.Error("missing status query parameter")
	}
}

func TestHTMLMutationResponsesDeclareJSONObjects(t *testing.T) {
	r := MustNew()
	for _, id := range []string{
		"html.rm", "html.draft.save", "html.unshare", "html.grant.add",
		"html.grant.rm", "html.asset.rm", "html.comment.add", "html.reply",
	} {
		op, ok := r.GetOperation(id)
		if !ok {
			t.Fatalf("%s not found", id)
		}
		if op.ResponseUnwrap != "data" {
			t.Errorf("%s response unwrap = %q, want data", id, op.ResponseUnwrap)
		}
		if op.ResponseSchema == nil || op.ResponseSchema.Type != "object" {
			t.Errorf("%s response schema = %#v, want JSON object", id, op.ResponseSchema)
		}
	}
}

// TestUnwrapRequiredFieldsAreDeclaredStrings guards an assumption made by the
// unwrap guard in cmd/service: it refuses a required unwrapped value that is not
// a string, because every such field today is a document reference. A spec that
// declares a non-string required field under the unwrap path would be refused at
// runtime on a valid response, so this fails here instead.
func TestUnwrapRequiredFieldsAreDeclaredStrings(t *testing.T) {
	r := MustNew()
	checked := 0
	for _, op := range r.ListAllOperations() {
		d, ok := r.GetOperation(op.ID)
		if !ok || len(d.UnwrapRequiredFields) == 0 {
			continue
		}
		if d.ResponseSchema == nil {
			t.Errorf("%s declares unwrap required fields but no success schema", op.ID)
			continue
		}
		payload := *d.ResponseSchema
		if next, ok := payload.Properties[d.ResponseUnwrap]; ok {
			payload = next
		}
		for _, name := range d.UnwrapRequiredFields {
			prop, ok := payload.Properties[name]
			if !ok {
				t.Errorf("%s: required unwrap field %q is not declared under %q", op.ID, name, d.ResponseUnwrap)
				continue
			}
			if prop.Type != "string" {
				t.Errorf("%s: required unwrap field %q is type %q; the runtime guard refuses non-strings", op.ID, name, prop.Type)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no required unwrap fields found; the guard this pins would be unreachable")
	}
}

func TestGetOperationDocsSearch_Pagination(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("docs.search")
	if !ok {
		t.Fatal("docs.search not found")
	}
	if op.Pagination == nil {
		t.Fatal("pagination: nil, want non-nil")
	}
	if op.Pagination.CursorParam != "cursor" || op.Pagination.ItemsField != "items" || op.Pagination.CursorField != "nextCursor" || op.Pagination.HasMoreField != "" || !op.Pagination.InferHasMore || !op.Pagination.RejectCursorRepeats {
		t.Errorf("pagination: got %+v", op.Pagination)
	}
	docType, ok := op.RequestBody.Properties["docType"]
	if !ok || docType.Items == nil {
		t.Fatal("docs.search docType item schema missing")
	}
	// html_ppt is a first-class document kind in octo-docs-backend's DOC_TYPES
	// (src/db/docType.ts, "the SINGLE source of truth"), with its own
	// /api/v1/ppt/** routes. It matters here because the CLI now enforces this
	// enum locally: omitting a kind the backend accepts turns a working call into
	// exit 2. cmd/service's requestSideVocabularies pins the same set with
	// provenance.
	wantTypes := []any{"doc", "sheet", "board", "html", "html_ppt"}
	if !reflect.DeepEqual(docType.Items.Enum, wantTypes) {
		t.Errorf("docs.search docType enum = %#v, want %#v", docType.Items.Enum, wantTypes)
	}
}

func TestGetOperationMessageSend_DMWorkimBase(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("message.send")
	if !ok {
		t.Fatal("message.send not found")
	}
	if op.BaseURLEnv != "OCTO_API_BASE_URL" {
		t.Errorf("base url env: got %q, want OCTO_API_BASE_URL", op.BaseURLEnv)
	}
	// message declares x-octo-space-header:true so the client keeps sending
	// X-Space-Id: sendMessage for a multi-space bot uses the header as the DM
	// multi-space selection hint, and dropping it silently mis-attributes the
	// message.
	if !op.SpaceHeader {
		t.Error("space header: want true for message domain (sendMessage uses X-Space-Id for multi-space DM selection)")
	}
}

// TestServiceSpaceHeaderContract pins the space-header declaration of every
// service spec so an accidental flip is caught. The client suppresses
// X-Space-Id only when a spec explicitly declares x-octo-space-header:false
// (SpaceHeaderSet && !SpaceHeader); the values below are the intended,
// server-verified per-service behaviour:
//   - message / matter / marketplace: true — the server reads X-Space-Id
//     (DM multi-space hint / space-scoped resources), so the client must keep
//     sending it.
//   - docs and the rest: false — those bot mounts server-resolve the space and
//     ignore the header, so the client honestly suppresses it.
func TestServiceSpaceHeaderContract(t *testing.T) {
	r := MustNew()
	cases := []struct {
		service string
		opID    string
		want    bool
	}{
		{"message", "message.send", true},
		{"matter", "matter.create", true},
		{"marketplace", "plugin.get", true},
		{"summary", "summary.list", false},
		{"docs", "docs.create", false},
		{"bot", "bot.register", false},
		{"thread", "thread.create", false},
		{"group", "group.create", false},
		{"file", "file.upload", false},
		{"event", "event.list", false},
		{"html", "html.publish", false},
	}
	for _, c := range cases {
		op, ok := r.GetOperation(c.opID)
		if !ok {
			t.Errorf("%s: operation %q not found", c.service, c.opID)
			continue
		}
		if !op.SpaceHeaderSet {
			t.Errorf("%s: x-octo-space-header must be declared explicitly (SpaceHeaderSet=false)", c.service)
		}
		if op.SpaceHeader != c.want {
			t.Errorf("%s: space header = %v, want %v", c.service, op.SpaceHeader, c.want)
		}
	}
}

func TestGetOperationNotFound(t *testing.T) {
	r := MustNew()
	if _, ok := r.GetOperation("does.not.exist"); ok {
		t.Fatal("GetOperation: expected ok=false for unknown id")
	}
}

// TestHeaderParamWithFlagAlias pins the general spec-declared header capability:
// docs.content.edit declares an If-Match header parameter carrying the
// x-octo-flag alias `base-version`, so the request engine can drive the
// optimistic-concurrency base-version token from a first-class flag onto a
// per-request header — no docs-specific carve-out in the transport.
func TestHeaderParamWithFlagAlias(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("docs.content.edit")
	if !ok {
		t.Fatal("docs.content.edit not found")
	}
	var found *ParamInfo
	for i := range op.Parameters {
		if op.Parameters[i].In == "header" {
			found = &op.Parameters[i]
			break
		}
	}
	if found == nil {
		t.Fatal("docs.content.edit: expected a header parameter (If-Match)")
	}
	if found.Name != "If-Match" {
		t.Errorf("header param name = %q, want If-Match", found.Name)
	}
	if found.FlagName != "base-version" {
		t.Errorf("header param flag alias = %q, want base-version (from x-octo-flag)", found.FlagName)
	}
	if !found.Required {
		t.Error("If-Match header must be required (mandatory base-version guard)")
	}
}

// TestQueryParamFlagAliasAvoidsGlobalCollision pins the docs.scene.export fix:
// its `format` query param carries x-octo-flag "image-format" so the generated
// CLI flag is --image-format (which does not shadow the global persistent
// --format output flag), while the wire query parameter name stays `format`.
func TestQueryParamFlagAliasAvoidsGlobalCollision(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("docs.scene.export")
	if !ok {
		t.Fatal("docs.scene.export not found")
	}
	var found *ParamInfo
	for i := range op.Parameters {
		if op.Parameters[i].In == "query" && op.Parameters[i].Name == "format" {
			found = &op.Parameters[i]
			break
		}
	}
	if found == nil {
		t.Fatal("docs.scene.export: expected a `format` query parameter")
	}
	if found.FlagName != "image-format" {
		t.Errorf("format query param flag alias = %q, want image-format (from x-octo-flag)", found.FlagName)
	}
	if found.Name != "format" {
		t.Errorf("wire query param name = %q, want format (must be preserved)", found.Name)
	}
}

// TestBodyPropertyFlagAlias pins docs.share.set: its shareScope/shareRole body
// properties carry x-octo-flag scope/role so the CLI exposes clean --scope /
// --role flags while the wire body keys stay the byte-exact backend contract
// (shareScope / shareRole). The property name is never renamed — only the flag
// alias is added.
func TestBodyPropertyFlagAlias(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("docs.share.set")
	if !ok {
		t.Fatal("docs.share.set not found")
	}
	if op.RequestBody == nil || op.RequestBody.Properties == nil {
		t.Fatal("docs.share.set: expected a request body with properties")
	}
	cases := map[string]string{"shareScope": "scope", "shareRole": "role"}
	for prop, wantFlag := range cases {
		p, ok := op.RequestBody.Properties[prop]
		if !ok {
			t.Errorf("docs.share.set: missing body property %q", prop)
			continue
		}
		if p.FlagName != wantFlag {
			t.Errorf("%s flag alias = %q, want %q (from x-octo-flag)", prop, p.FlagName, wantFlag)
		}
	}
}

func TestDocsSheetCheckboxSchemas(t *testing.T) {
	r := MustNew()

	edit, ok := r.GetOperation("docs.sheet.edit")
	if !ok || edit.RequestBody == nil {
		t.Fatal("docs.sheet.edit request body not found")
	}
	if validation, ok := edit.RequestBody.Properties["dataValidations"]; !ok || validation.Type != "object" {
		t.Fatalf("docs.sheet.edit dataValidations = %+v, want object property", validation)
	}

	for _, operationID := range []string{"docs.sheet.get", "docs.versions.state"} {
		op, ok := r.GetOperation(operationID)
		if !ok || op.ResponseSchema == nil {
			t.Fatalf("%s response schema not found", operationID)
		}
		if validation, ok := op.ResponseSchema.Properties["sheetDataValidations"]; !ok || validation.Type != "object" {
			t.Errorf("%s sheetDataValidations = %+v, want object property", operationID, validation)
		}
	}
}

// TestBinaryBodyGatingDistinguishesInlineFromRedirect pins the -o footgun fix:
// both docs.scene.export and file.download are x-octo-binary-response, but only
// docs.scene.export delivers a body inline on a 2xx success, so only it should
// carry BinaryBody (the gate for the --output/-o flag). file.download is a
// 302-only redirect with no consumable body — offering -o there silently writes
// nothing.
func TestBinaryBodyGatingDistinguishesInlineFromRedirect(t *testing.T) {
	r := MustNew()

	export, ok := r.GetOperation("docs.scene.export")
	if !ok {
		t.Fatal("docs.scene.export not found")
	}
	if !export.BinaryResponse {
		t.Error("docs.scene.export: expected BinaryResponse=true")
	}
	if !export.BinaryBody {
		t.Error("docs.scene.export: expected BinaryBody=true (has a 2xx image body, -o must write it)")
	}

	dl, ok := r.GetOperation("file.download")
	if !ok {
		t.Fatal("file.download not found")
	}
	if !dl.BinaryResponse {
		t.Error("file.download: expected BinaryResponse=true (client still surfaces the 302 Location)")
	}
	if dl.BinaryBody {
		t.Error("file.download: expected BinaryBody=false (302-only redirect, -o would silently no-op)")
	}
}

// TestHasSuccessBodyResolvesResponseRef pins the item-3 fix: a 2xx response may
// be expressed inline OR via {"$ref":"#/components/responses/..."}. hasSuccessBody
// must resolve the ref before checking for a content body, otherwise a spec that
// factors its success response into components.responses would fail-closed and
// silently drop the --output/-o flag (BinaryBody=false) for a real binary body.
func TestHasSuccessBodyResolvesResponseRef(t *testing.T) {
	doc := map[string]any{
		"components": map[string]any{
			"responses": map[string]any{
				"BoardImage": map[string]any{
					"description": "shared image response",
					"content": map[string]any{
						"image/png": map[string]any{},
					},
				},
				"NoBody": map[string]any{
					"description": "bodyless shared response",
				},
			},
		},
	}

	cases := []struct {
		name  string
		resps map[string]any
		want  bool
	}{
		{
			name:  "inline 2xx content body",
			resps: map[string]any{"200": map[string]any{"content": map[string]any{"image/png": map[string]any{}}}},
			want:  true,
		},
		{
			name:  "2xx response via components.responses $ref with body",
			resps: map[string]any{"200": map[string]any{"$ref": "#/components/responses/BoardImage"}},
			want:  true,
		},
		{
			name:  "2xx response via $ref to a bodyless response",
			resps: map[string]any{"204": map[string]any{"$ref": "#/components/responses/NoBody"}},
			want:  false,
		},
		{
			name:  "unresolvable $ref is treated as no body",
			resps: map[string]any{"200": map[string]any{"$ref": "#/components/responses/Missing"}},
			want:  false,
		},
		{
			name:  "non-2xx content body is ignored",
			resps: map[string]any{"400": map[string]any{"content": map[string]any{"application/json": map[string]any{}}}},
			want:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSuccessBody(doc, tc.resps); got != tc.want {
				t.Errorf("hasSuccessBody = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolvesComponentRef(t *testing.T) {
	// matter.get's 200 response is a $ref to MatterDetail — the resolver
	// should inline the properties so the schema command can describe it.
	r := MustNew()
	op, ok := r.GetOperation("matter.get")
	if !ok {
		t.Fatal("matter.get not found")
	}
	if op.ResponseSchema == nil {
		t.Fatal("response schema: nil")
	}
	if _, ok := op.ResponseSchema.Properties["matter"]; !ok {
		t.Errorf("response schema: expected matter property after ref resolution; got %v", op.ResponseSchema.Properties)
	}
}

func TestWorkspaceListResolvesTypedCollection(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("workspace.list")
	if !ok {
		t.Fatal("workspace.list not found")
	}
	if op.ResponseSchema == nil {
		t.Fatal("response schema: nil")
	}
	data, ok := op.ResponseSchema.Properties["data"]
	if !ok || data.Items == nil {
		t.Fatalf("workspace.list data schema = %+v", data)
	}
	if data.Items.Ref != "" || data.Items.Properties["workspace_id"].Type != "string" {
		t.Fatalf("workspace item schema was not resolved: %+v", data.Items)
	}
	if _, ok := op.ResponseSchema.Properties["pagination"]; !ok {
		t.Fatalf("workspace.list response schema = %+v, want pagination", op.ResponseSchema.Properties)
	}
}

func operationIDs(ops []OperationInfo) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = o.ID
	}
	return out
}

// matter carries x-octo-disabled in its embedded spec — it must stay loaded
// (engine + schema introspection depend on it) yet drop out of the
// caller-facing enabled views.

func TestServiceDisabled(t *testing.T) {
	r := MustNew()
	if !r.ServiceDisabled("matter") {
		t.Error("matter should be disabled (x-octo-disabled in spec)")
	}
	if r.ServiceDisabled("message") {
		t.Error("message should not be disabled")
	}
	if r.ServiceDisabled("nosuch") {
		t.Error("unknown service should report not-disabled, not panic")
	}
}

func TestEnabledServicesExcludesDisabledButKeepsLoaded(t *testing.T) {
	r := MustNew()
	// Invariant that protects the engine fixture + introspection: the raw
	// listing still has matter even though the enabled view drops it.
	if !contains(r.ListServices(), "matter") {
		t.Fatal("ListServices must still include matter (raw view)")
	}
	if contains(r.EnabledServices(), "matter") {
		t.Error("EnabledServices must exclude matter")
	}
	if !contains(r.EnabledServices(), "message") {
		t.Error("EnabledServices must still include message")
	}
}

func TestEnabledOperationsExcludesDisabledButResolvable(t *testing.T) {
	r := MustNew()
	for _, op := range r.EnabledOperations() {
		if op.Service == "matter" {
			t.Errorf("EnabledOperations leaked a matter op: %s", op.ID)
		}
	}
	// Explicit lookup of a disabled service's op still resolves.
	if _, ok := r.GetOperation("matter.create"); !ok {
		t.Error("GetOperation(matter.create) must still resolve for introspection")
	}
}

func TestTruthy(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{"true", true},
		{false, false},
		{"false", false},
		{"", false},
		{nil, false},
		{1, false},
	}
	for _, c := range cases {
		if got := truthy(c.in); got != c.want {
			t.Errorf("truthy(%#v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
