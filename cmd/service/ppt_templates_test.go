package service

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestPptTemplateChoicesReachCreateUnchanged(t *testing.T) {
	for _, template := range []string{"blank", "signal", "terra", "orbital", "picnic"} {
		for _, input := range []string{"flag", "data", "file"} {
			t.Run(template+"/"+input, func(t *testing.T) {
				want := map[string]any{"docType": "html_ppt", "title": "Gallery deck", "templateId": template}
				calls := 0
				root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					if r.Method != http.MethodPost || r.URL.Path != "/v1/bot/docs" || r.Header.Get("Idempotency-Key") != "gallery-create" {
						t.Errorf("wrong create route or replay key: %s %s", r.Method, r.URL.Path)
					}
					var got map[string]any
					if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Errorf("create body = %#v, want %#v", got, want)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(`{"docId":"deck","docType":"html_ppt"}`))
				})
				args := []string{"docs", "create", "--idempotency-key", "gallery-create"}
				if input == "flag" {
					args = append(args, "--docType", "html_ppt", "--title", "Gallery deck", "--templateId", template)
				} else {
					body, err := json.Marshal(want)
					if err != nil {
						t.Fatal(err)
					}
					value := string(body)
					if input == "file" {
						file := filepath.Join(t.TempDir(), "create.json")
						if err := os.WriteFile(file, body, 0o600); err != nil {
							t.Fatal(err)
						}
						value = "@" + file
					}
					args = append(args, "--data", value)
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("supported template %q via %s rejected: %v", template, input, err)
				}
				if calls != 1 {
					t.Fatalf("create requests = %d, want 1", calls)
				}
			})
		}
	}
}

func TestPptTemplateUnknownChoicesFailBeforeHTTP(t *testing.T) {
	var cases [][]string
	for _, invalid := range []string{"pitch", "report", "lesson", "unknown-template", "Signal"} {
		body := `{"templateId":"` + invalid + `"}`
		file := filepath.Join(t.TempDir(), "create.json")
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, []string{"--templateId", invalid}, []string{"--data", body}, []string{"--data", "@" + file})
	}
	// A flag is a string, so explicit JSON null has only inline/file forms.
	nullFile := filepath.Join(t.TempDir(), "null.json")
	if err := os.WriteFile(nullFile, []byte(`{"templateId":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases = append(cases, []string{"--data", `{"templateId":null}`}, []string{"--data", "@" + nullFile})
	for _, args := range cases {
		t.Run(args[len(args)-1], func(t *testing.T) {
			calls := 0
			root, tf, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) { calls++ })
			root.SetArgs(append([]string{"docs", "create", "--docType", "html_ppt", "--title", "Deck", "--idempotency-key", "gallery-create"}, args...))
			err := output.AsExitError(root.Execute())
			if err == nil || err.Code != "ENUM_NOT_ALLOWED" || calls != 0 {
				t.Fatalf("invalid template must fail before HTTP: err=%v calls=%d", err, calls)
			}
			if !strings.Contains(err.Hint, "docs list") || !strings.Contains(err.Hint, "docs search") || !strings.Contains(err.Hint, "new key") {
				t.Errorf("retired-template hint must explain ambiguous-create reconciliation: %s", err.Hint)
			}
			if err.ExitCode() != 2 || !strings.Contains(err.Hint, "matching backend catalogue") || !strings.Contains(err.Hint, "only blank") {
				t.Fatalf("template error missing actionable rollout hint: %#v", err)
			}
			var envelope struct {
				Error struct{ Hint string } `json:"error"`
			}
			if decodeErr := json.Unmarshal(tf.ErrOut.Bytes(), &envelope); decodeErr != nil || envelope.Error.Hint != err.Hint {
				t.Fatalf("rollout hint missing from emitted envelope: %s (%v)", tf.ErrOut.String(), decodeErr)
			}
		})
	}
}
