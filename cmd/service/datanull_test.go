package service

import (
	"net/http"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// Round-11: `--data null` decoded successfully into a nil map, and the promoted-flag
// merge then wrote into it — panic: assignment to entry in nil map. A panic is the one
// outcome a CLI must never have on caller input, and it is worse here than a bad
// message: it exits with a Go stack trace instead of the structured envelope every
// other malformed value produces, which is exactly what this PR's local contract
// enforcement is supposed to guarantee.
//
// The whole shape class, enumerated rather than patched at the one reported input:
// --data accepts five non-object JSON shapes, and `null` was the only one not already
// refused, because it is the only one that *decodes into a map successfully*. The
// others fail in Decode and were always clean validation errors. That is why the fix
// is a nil check after a successful decode and not a new special case.
func TestData_NonObjectShapesAreAllRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		// null with a promoted flag is the panic. Without one it silently behaved
		// like an absent body, which is the same root cause with a quieter symptom.
		{"null with a promoted body flag", []string{"docs", "create", "--data", "null", "--title", "t"}},
		{"null alone", []string{"docs", "create", "--data", "null"}},
		{"true", []string{"docs", "create", "--data", "true", "--title", "t"}},
		{"number", []string{"docs", "create", "--data", "123", "--title", "t"}},
		{"string", []string{"docs", "create", "--data", `"str"`, "--title", "t"}},
		{"array", []string{"docs", "create", "--data", "[]", "--title", "t"}},
		{"array of objects", []string{"docs", "create", "--data", `[{"title":"t"}]`, "--title", "t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			})
			root.SetArgs(tc.args)

			// A panic here is the defect, so it is caught and reported as a failure
			// rather than taking the test binary down with it.
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panicked on --data %s: %v — malformed input must be a structured "+
							"validation error, never a stack trace", tc.name, r)
					}
				}()
				err = root.Execute()
			}()

			if err == nil {
				t.Fatal("--data must be a JSON object, so this must be a local validation error")
			}
			if ee := output.AsExitError(err); ee == nil || ee.Type != "validation" {
				t.Errorf("taxonomy: got %v, want a validation error", err)
			}
			if called {
				t.Error("no request may be sent when --data fails validation")
			}
		})
	}
}

// TestData_AnObjectIsStillAccepted is the allow direction: the fix must reject
// non-objects without rejecting the shape --data exists for.
func TestData_AnObjectIsStillAccepted(t *testing.T) {
	root, _ := rootWithDriveToken(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {})
	root.SetArgs([]string{"docs", "create", "--data", `{"title":"from data"}`})
	if err := root.Execute(); err != nil {
		t.Errorf("a JSON object is the supported shape and must be accepted: %v", err)
	}
}
