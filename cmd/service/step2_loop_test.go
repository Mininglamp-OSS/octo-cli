package service

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

const loopPriorityTestWorkspaceID = "99999999-9999-4999-8999-999999999999"

// TaskCreate and TaskUpdate must expose the Fleet priority vocabulary to the
// same local enum gate: supported values still reach the wire, while an
// unsupported value is rejected before HTTP (including when supplied by --data).
func TestLoopTaskPriorityVocabulary(t *testing.T) {
	operations := []struct {
		name string
		args []string
	}{
		{
			name: "create",
			args: []string{"loop", "task", "create", "--workspace-id", loopPriorityTestWorkspaceID},
		},
		{
			name: "update",
			args: []string{"loop", "task", "update", "11111111-1111-4111-8111-111111111111", "--workspace-id", loopPriorityTestWorkspaceID},
		},
	}
	for _, operation := range operations {
		for _, priority := range []string{"urgent", "high", "medium", "low", "none"} {
			t.Run("accept/"+operation.name+"/"+priority, func(t *testing.T) {
				var requests int
				var body map[string]any
				root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					requests++
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode task body: %v", err)
					}
					w.Header().Set("Content-Type", "application/json")
					if operation.name == "create" {
						w.WriteHeader(http.StatusCreated)
					}
					_, _ = io.WriteString(w, `{"data":{"task_id":"task-1"}}`)
				})
				args := append(append([]string{}, operation.args...), "--title", "Check priority", "--priority", priority)
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("supported priority %q was rejected: %v", priority, err)
				}
				if requests != 1 {
					t.Fatalf("backend requests = %d, want 1", requests)
				}
				if got, _ := body["priority"].(string); got != priority {
					t.Errorf("request priority = %q, want %q", got, priority)
				}
			})
		}

		for _, input := range []struct {
			name string
			args []string
		}{
			{
				name: "promoted flag",
				args: []string{"--title", "Check priority", "--priority", "emergency"},
			},
			{
				name: "merged --data body",
				args: []string{"--data", `{"title":"Check priority","priority":"emergency"}`},
			},
		} {
			t.Run("reject/"+operation.name+"/"+input.name, func(t *testing.T) {
				var requests int
				root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					requests++
					w.Header().Set("Content-Type", "application/json")
					if operation.name == "create" {
						w.WriteHeader(http.StatusCreated)
					}
					_, _ = io.WriteString(w, `{"data":{"task_id":"task-1"}}`)
				})
				args := append(append([]string{}, operation.args...), input.args...)
				root.SetArgs(args)
				err := root.Execute()
				if err == nil {
					t.Fatal("unsupported priority was accepted, want local validation failure")
				}
				if requests != 0 {
					t.Errorf("invalid priority reached backend %d time(s), want 0", requests)
				}
				if !strings.Contains(err.Error(), "--priority") {
					t.Errorf("error %q should identify --priority", err)
				}
				if !strings.Contains(tf.ErrOut.String(), enumNotAllowed) {
					t.Errorf("stderr envelope %q should carry %s", tf.ErrOut.String(), enumNotAllowed)
				}
			})
		}
	}
}

// This isolates the body-flag conversion from the Loop schema: a numeric
// position is a float in Fleet, so the promoted flag must preserve fractions.
func TestLoopNumberBodyFlagPreservesFractionalValue(t *testing.T) {
	var requests int
	var gotBody map[string]any
	_, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"data":{"ok":true}}`)
	})

	detail := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{
			ID:      "loop.position.probe",
			Service: "loop",
			Method:  http.MethodPost,
			Path:    "/fleet/api/v1/position-probe",
		},
		RequestBody: &registry.SchemaInfo{
			Type: "object",
			Properties: map[string]registry.SchemaInfo{
				"position": {Type: "number"},
			},
		},
	}
	root := &cobra.Command{Use: "octo-cli", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(buildOperationCmd(tf.Factory, detail, "probe"))
	root.SetArgs([]string{"probe", "--position", "4.5"})

	if err := root.Execute(); err != nil {
		t.Fatalf("execute synthetic --position 4.5: %v", err)
	}
	if requests != 1 {
		t.Fatalf("backend requests = %d, want 1", requests)
	}
	if got, ok := gotBody["position"].(float64); !ok || got != 4.5 {
		t.Errorf("request position = %#v, want float64(4.5)", gotBody["position"])
	}
}
