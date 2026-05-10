package registry

import (
	"sync"
	"testing"
)

// TestEmptyRegistryDoesNotPanic verifies that a Registry with zero specs
// doesn't panic on any public accessor. Helpful for tests that stub the
// registry via the Factory.
func TestEmptyRegistryDoesNotPanic(t *testing.T) {
	r := &Registry{specs: map[string]map[string]any{}}

	if got := r.ListServices(); len(got) != 0 {
		t.Errorf("ListServices: got %v, want empty", got)
	}
	if got := r.ListAllOperations(); len(got) != 0 {
		t.Errorf("ListAllOperations: got %v, want empty", got)
	}
	if got := r.ListOperations("nosuch"); len(got) != 0 {
		t.Errorf("ListOperations(nosuch): got %v, want empty", got)
	}
	if spec := r.GetSpec("nosuch"); spec != nil {
		t.Errorf("GetSpec(nosuch): got non-nil")
	}
	if op, ok := r.GetOperation("nosuch.verb"); ok || op != nil {
		t.Errorf("GetOperation(nosuch.verb): got (%v, %v), want (nil, false)", op, ok)
	}
}

// TestRegistryConcurrentReads documents that Registry read methods are
// goroutine-safe because specs is never mutated after construction.
// Writers to Registry do not exist; New/MustNew are the only populators.
// If a future change adds mutation, this test will surface the race under
// `go test -race`.
func TestRegistryConcurrentReads(t *testing.T) {
	r := MustNew()
	const workers = 16
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			_ = r.ListServices()
			_ = r.ListAllOperations()
			_ = r.ListOperations("matter")
			if _, ok := r.GetOperation("matter.create"); !ok {
				t.Errorf("matter.create not found")
			}
		}()
	}
	wg.Wait()
}
