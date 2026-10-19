package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestHTMLSourceCommand(t *testing.T) {
	const html = "<html>\n<p title=\"quoted\">中文 &amp; \\ path</p>\n</html>"
	for _, version := range []string{"", "3"} {
		t.Run("version="+version, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/docs-html/v1/docs/doc%2Fcanonical/source" || r.URL.Query().Get("version") != version {
					t.Errorf("request = %s %s", r.Method, r.URL.String())
				}
				if version == "" && r.URL.RawQuery != "" {
					t.Errorf("latest must omit version: %s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"slug": "doc/canonical", "version": 3, "html": html}})
			})
			cmd := findCmd(findCmd(root, "html"), "source")
			if cmd == nil || cmd.Use != "source <doc-ref>" {
				t.Fatalf("unexpected source command: %v", cmd)
			}
			args := []string{"html", "source", "doc/canonical"}
			if version != "" {
				args = append(args, "--version", version)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Slug    string
					Version int
					HTML    string
				}
			}
			if err := json.Unmarshal(tf.Out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if !env.OK || env.Data.Slug != "doc/canonical" || env.Data.Version != 3 || env.Data.HTML != html {
				t.Fatalf("source changed in transit: %s", tf.Out.String())
			}
		})
	}
}

func TestHTMLSourceRejectsIncompleteResponse(t *testing.T) {
	for _, body := range []string{`{"data":null}`, `{"data":{"slug":"doc","html":"<p>x</p>"}}`, `{"data":{"slug":"doc","version":3}}`, `{"data":{"slug":"doc","version":null,"html":"x"}}`, `{"data":{"slug":"doc","version":"3","html":"x"}}`, `{"data":{"slug":"doc","version":3,"html":{}}}`} {
		t.Run(body, func(t *testing.T) {
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			root.SetArgs([]string{"html", "source", "doc"})
			if err := root.Execute(); err == nil {
				t.Fatalf("incomplete source succeeded: %s", tf.Out.String())
			}
		})
	}
}

func TestHTMLSourcePublishConflictWorkflow(t *testing.T) {
	for _, tc := range []struct {
		status         int
		code, fineCode string
	}{{428, "VALIDATION_ERROR", "version_required"}, {409, "CONFLICT", "version_conflict"}} {
		t.Run(tc.code, func(t *testing.T) {
			writes := 0
			root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(`{"data":{"slug":"doc","version":7,"html":"<p>other edits</p>"}}`))
					return
				}
				writes++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body["version"] != float64(8) || body["slug"] != "doc" || body["html"] != "<p>other edits</p><p>my edit</p>" {
					t.Errorf("publish = %v", body)
				}
				if _, ok := body["idempotency_key"]; ok {
					t.Error("update added create idempotency key")
				}
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"reread the latest source and reapply the edit","details":{"code":%q,"latest_version":8,"next_version":9,"source_path":"/v1/docs/doc/source?version=8"},"hint":"Do not only increase version and resend the old HTML."}}`, tc.code, tc.fineCode)
			})
			root.SetArgs([]string{"html", "source", "doc"})
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			var source struct {
				Data struct {
					HTML    string
					Version int
				}
			}
			if err := json.Unmarshal(tf.Out.Bytes(), &source); err != nil {
				t.Fatal(err)
			}
			tf.Out.Reset()
			root.SetArgs([]string{"html", "publish", "--slug", "doc", "--version", strconv.Itoa(source.Data.Version + 1), "--html", source.Data.HTML + "<p>my edit</p>"})
			err := root.Execute()
			var ee *output.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 2 || ee.Code != tc.code {
				t.Fatalf("error = %v", err)
			}
			if writes != 1 {
				t.Fatalf("rejected content retried %d times", writes)
			}
			var raw struct {
				Error struct {
					Details map[string]any
					Hint    string
				}
			}
			if err := json.Unmarshal(ee.Detail, &raw); err != nil {
				t.Fatal(err)
			}
			if raw.Error.Details["code"] != tc.fineCode || raw.Error.Details["latest_version"] != float64(8) || raw.Error.Details["next_version"] != float64(9) || raw.Error.Details["source_path"] != "/v1/docs/doc/source?version=8" || !strings.Contains(raw.Error.Hint, "Do not only increase") {
				t.Fatalf("recovery details lost: %s", ee.Detail)
			}
		})
	}
}
