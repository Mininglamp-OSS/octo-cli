package registry

import (
	"testing"
)

func TestNewLoadsAllServices(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got := r.ListServices()
	want := []string{"bot", "docs", "event", "file", "group", "matter", "message", "thread"}
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
	// Backend route counts per service. The user-facing CLI has more commands
	// than backend ops for matter (close/reopen/archive are aliases over
	// matter.transition) — the spec tracks actual routes, not CLI surface.
	r := MustNew()
	expected := map[string]int{
		"matter":  14,
		"message": 4,
		"group":   9,
		"thread":  8,
		"file":    4,
		"bot":     6,
		"event":   2,
		"docs":    22,
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

func TestGetOperationMessageSend_DMWorkimBase(t *testing.T) {
	r := MustNew()
	op, ok := r.GetOperation("message.send")
	if !ok {
		t.Fatal("message.send not found")
	}
	if op.BaseURLEnv != "OCTO_API_BASE_URL" {
		t.Errorf("base url env: got %q, want OCTO_API_BASE_URL", op.BaseURLEnv)
	}
	if op.SpaceHeader {
		t.Error("space header: want false for dmworkim domain")
	}
}

func TestGetOperationNotFound(t *testing.T) {
	r := MustNew()
	if _, ok := r.GetOperation("does.not.exist"); ok {
		t.Fatal("GetOperation: expected ok=false for unknown id")
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
