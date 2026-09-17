package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestBotDocumentDeleteForbidden(t *testing.T) {
	cases := []struct {
		name string
		args []string
		path string
	}{
		{"docs command", []string{"docs", "delete", "doc-1"}, "/v1/bot/docs/doc-1"},
		{"html command", []string{"html", "rm", "legacy-slug"}, "/docs-html/v1/docs/legacy-slug"},
		{"raw document ID", []string{"api", "DELETE", "/v1/bot/docs/doc-1"}, "/v1/bot/docs/doc-1"},
		{"raw HTML slug", []string{"api", "DELETE", "/v1/bot/docs/octo-doc/legacy-slug"}, "/v1/bot/docs/octo-doc/legacy-slug"},
	}
	for _, token := range []string{"app_test", "bf_test"} {
		for _, testCase := range cases {
			t.Run(token+"/"+testCase.name, func(t *testing.T) {
				var requests atomic.Int32
				env := newDriveTestEnv(t, token, func(writer http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					if request.Method != http.MethodDelete || request.URL.Path != testCase.path {
						t.Errorf("request = %s %s, want DELETE %s", request.Method, request.URL.Path, testCase.path)
					}
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(http.StatusForbidden)
					_, _ = writer.Write([]byte(`{"error":"bot_delete_forbidden"}`))
				}, nil)
				err := output.AsExitError(env.run(testCase.args...))
				if err == nil {
					t.Fatal("expected a structured deletion denial")
				}
				if err.Type != "permission" || err.Code != "bot_delete_forbidden" || err.HTTPStatus != http.StatusForbidden {
					t.Fatalf("error = %+v", err)
				}
				if err.ExitCode() != 1 || err.OutcomeUnknown() {
					t.Fatalf("denial must be a definite failure: %+v", err)
				}
				for _, instruction := range []string{"even as owner/admin", "do not retry", "human"} {
					if !strings.Contains(err.Hint, instruction) {
						t.Errorf("hint %q must explain %q", err.Hint, instruction)
					}
				}
				if requests.Load() != 1 {
					t.Errorf("requests = %d, want one request without retries or fallback routes", requests.Load())
				}
				var envelope struct {
					OK    bool `json:"ok"`
					Error struct {
						Type   string `json:"type"`
						Code   string `json:"code"`
						Hint   string `json:"hint"`
						Detail struct {
							Error string `json:"error"`
						} `json:"detail"`
					} `json:"error"`
				}
				if decodeErr := json.Unmarshal(env.tf.ErrOut.Bytes(), &envelope); decodeErr != nil {
					t.Fatalf("decode error envelope: %v; stderr = %s", decodeErr, env.tf.ErrOut.String())
				}
				if envelope.OK || envelope.Error.Type != err.Type || envelope.Error.Code != err.Code || envelope.Error.Hint != err.Hint {
					t.Errorf("emitted envelope = %+v, want the same structured denial", envelope)
				}
				if envelope.Error.Detail.Error != "bot_delete_forbidden" {
					t.Errorf("original denial not preserved: %+v", envelope.Error.Detail)
				}
			})
		}
	}
}
