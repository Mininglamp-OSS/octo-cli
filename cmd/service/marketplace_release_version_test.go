package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestMarketplaceReleaseVersionMetadata(t *testing.T) {
	r := registry.MustNew()
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"plugin.publish", true}, {"plugin.review_request.create", true},
		{"plugin.upsert", false}, {"plugin.import", false},
	} {
		op, ok := r.GetOperation(tc.id)
		if !ok || op.ValidateMarketplaceReleaseVersion != tc.want {
			t.Fatalf("%s: missing release-validation opt-in/out", tc.id)
		}
	}
	publish, _ := r.GetOperation("plugin.publish")
	for _, name := range publish.RequestBody.Required {
		if name == "version" {
			t.Fatal("version must not become globally required for Connectors")
		}
	}
}

func TestVersionedMarketplacePublicationRefusesMissingOrInvalidLabel(t *testing.T) {
	for _, kind := range []string{"skill", "expert", "expert_team"} {
		for _, tc := range []struct {
			name   string
			args   []string
			reason string
		}{
			{"missing", []string{"publish", "--plugin-id", "p1"}, "required"},
			{"blank flag", []string{"publish", "--plugin-id", "p1", "--version", "  "}, "required"},
			{"bad flag", []string{"publish", "--plugin-id", "p1", "--version", "v1.0"}, "invalid"},
			{"bad data", []string{"publish", "--data", `{"plugin_id":"p1","version":"1.2"}`}, "invalid"},
			{"blank data", []string{"publish", "--data", `{"plugin_id":"p1","version":""}`}, "required"},
			{"review bad", []string{"review-request", "create", "--data", `{"plugin_id":"p1","version":"1.2"}`}, "invalid"},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				reads, writes := 0, 0
				root, tf, _ := rootWithServiceSpaced(t, "space-a", func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodGet {
						writes++
						t.Error("publication must not be sent")
						return
					}
					reads++
					if r.URL.Path != "/market/api/v1/plugins/detail" || r.URL.Query().Get("plugin_id") != "p1" || r.URL.Query().Get("include_relations") != "false" || r.Header.Get("X-Space-Id") != "space-a" {
						t.Errorf("wrong preflight scope: %s %s", r.URL, r.Header.Get("X-Space-Id"))
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"data":{"plugin":{"plugin_type":%q,"current_version":"9.9.9"}}}`, kind)
				})
				root.SetArgs(append([]string{"marketplace", "plugin"}, tc.args...))
				err := output.AsExitError(root.Execute())
				if err == nil || err.Code != "VALIDATION_ERROR" || err.ExitCode() != 2 || !strings.Contains(err.Hint, "--version") || !strings.Contains(err.Hint, "Upgrade octo-cli") {
					t.Fatalf("error: %v / stderr: %s", err, tf.ErrOut.String())
				}
				if reads != 1 || writes != 0 || !strings.Contains(string(err.Detail), `"reason":"`+tc.reason+`"`) || tf.Out.Len() != 0 {
					t.Fatalf("reads=%d writes=%d detail=%s stdout=%s", reads, writes, err.Detail, tf.Out.String())
				}
			})
		}
	}
}

func TestMarketplaceReleaseExplicitLabelNeedsNoReadAndFlagsOverrideData(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag", []string{"publish", "--plugin-id", "p1", "--version", "1.3.0"}},
		{"data", []string{"publish", "--data", `{"plugin_id":"p1","version":"1.3.0"}`}},
		{"override", []string{"publish", "--data", `{"plugin_id":"p1","version":"bad"}`, "--version", "1.3.0"}},
		{"review", []string{"review-request", "create", "--plugin-id", "p1", "--version", "1.3.0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if r.Method != http.MethodPost {
					t.Fatalf("unexpected lookup: %s", r.URL)
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["version"] != "1.3.0" {
					t.Errorf("version changed/inferred: %#v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":{"plugin_id":"p1"}}`))
			})
			root.SetArgs(append([]string{"marketplace", "plugin"}, tc.args...))
			if err := root.Execute(); err != nil || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestMarketplaceConnectorPublicationRetainsExistingVersionPayload(t *testing.T) {
	for _, version := range []string{"", "legacy-label"} {
		t.Run(version, func(t *testing.T) {
			reads, writes := 0, 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodGet {
					reads++
					w.Write([]byte(`{"data":{"plugin":{"plugin_type":"connector"}}}`))
					return
				}
				writes++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if version == "" {
					if _, exists := body["version"]; exists {
						t.Error("connector version was injected")
					}
				} else if body["version"] != version {
					t.Errorf("connector version changed: %#v", body)
				}
				w.Write([]byte(`{"data":{"plugin_id":"p1"}}`))
			})
			args := []string{"marketplace", "plugin", "publish", "--plugin-id", "p1"}
			if version != "" {
				args = append(args, "--version", version)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil || reads != 1 || writes != 1 {
				t.Fatalf("reads=%d writes=%d err=%v", reads, writes, err)
			}
		})
	}
}

func TestMarketplacePublicationLookupFailureNeverWrites(t *testing.T) {
	for _, tc := range []struct {
		name           string
		status         int
		body, wantCode string
	}{
		{"denied", 403, `{"error":{"code":"FORBIDDEN","message":"denied","hint":"use the resource owner"}}`, "FORBIDDEN"},
		{"foreign", 404, `{"error":{"code":"NOT_FOUND","message":"not found"}}`, "NOT_FOUND"},
		{"unknown", 200, `{"data":{"plugin":{"plugin_type":"future"}}}`, "VALIDATION_ERROR"},
		{"missing", 200, `{"data":{}}`, "VALIDATION_ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet {
					t.Fatal("write after failed lookup")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			root.SetArgs([]string{"marketplace", "plugin", "publish", "--plugin-id", "foreign"})
			err := output.AsExitError(root.Execute())
			if err == nil || err.Code != tc.wantCode || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if tc.status == 403 && err.Hint != "bot lacks permission; check space membership" {
				t.Fatalf("non-version error hint changed: %s", err.Hint)
			}
		})
	}
}

func TestMarketplaceDraftVersionRemainsOptional(t *testing.T) {
	for _, kind := range []string{"skill", "expert", "expert_team"} {
		for _, update := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/update=%v", kind, update), func(t *testing.T) {
				plugin := map[string]any{"plugin_name": "Draft", "plugin_type": kind, "visibility": "private", "manifest_json": map[string]any{}, "plugin_json": map[string]any{}}
				if update {
					plugin["plugin_id"] = "p1"
				}
				data, _ := json.Marshal(map[string]any{"plugin": plugin})
				calls := 0
				root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					calls++
					var body struct {
						Plugin map[string]any `json:"plugin"`
					}
					json.NewDecoder(r.Body).Decode(&body)
					if _, ok := body.Plugin["version"]; ok {
						t.Error("draft version was injected")
					}
					w.Header().Set("Content-Type", "application/json")
					w.Write([]byte(`{"data":{"plugin":{"plugin_id":"p1"}}}`))
				})
				root.SetArgs([]string{"marketplace", "plugin", "upsert", "--data", string(data)})
				if err := root.Execute(); err != nil || calls != 1 {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
			})
		}
	}
}

func TestMarketplaceReleaseDryRunNeverLooksUpOrWrites(t *testing.T) {
	root, tf, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) { t.Error("dry-run made a request") })
	cfg := &config.Config{APIBaseURL: "https://market.example", BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	tf.Globals.DryRun = true
	tf.SetClient(client.New(cfg, cred, client.Options{DryRun: true, ErrOut: tf.ErrOut}))
	root.SetArgs([]string{"marketplace", "plugin", "publish", "--plugin-id", "p1"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tf.Out.String(), "publish") {
		t.Fatalf("missing preview: %s", tf.Out.String())
	}
}

func TestMarketplacePublicationPreservesServerUpgradeHint(t *testing.T) {
	const hint = "Update octo-cli and use Plugin APIs"
	root, tf, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatal("valid label should need no lookup")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"code":"VALIDATION_ERROR","message":"old API disabled","details":{"reason":"versioned_api_required"},"hint":%q}}`, hint)
	})
	root.SetArgs([]string{"marketplace", "plugin", "publish", "--plugin-id", "p1", "--version", "1.3.0"})
	err := output.AsExitError(root.Execute())
	if err == nil || err.Hint != hint || !strings.Contains(tf.ErrOut.String(), "versioned_api_required") {
		t.Fatalf("server upgrade hint/details lost: %v stderr=%s", err, tf.ErrOut.String())
	}
}

func TestMarketplaceSkillImportVersionOptional(t *testing.T) {
	for _, data := range []string{`{"parse_task_id":"t1","plugin_name":"Draft Skill"}`, `{"parse_task_id":"t1","plugin_id":"p1"}`} {
		calls := 0
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			if r.Method != http.MethodPost || r.URL.Path != "/market/api/v1/plugins/import" {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if _, found := body["version"]; found {
				t.Fatal("import injected a version")
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"plugin":{"plugin_id":"p1"}}}`))
		})
		root.SetArgs([]string{"marketplace", "plugin", "import", "--data", data})
		if err := root.Execute(); err != nil || calls != 1 {
			t.Fatalf("calls=%d err=%v", calls, err)
		}
	}
}

func TestMarketplaceReviewMissingVersionStillRejectedLocally(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) {
		t.Error("missing required review version made an HTTP request")
	})
	root.SetArgs([]string{"marketplace", "plugin", "review-request", "create", "--plugin-id", "p1"})
	err := output.AsExitError(root.Execute())
	if err == nil || err.Code != "VALIDATION_ERROR" || !strings.Contains(err.Message, "version") {
		t.Fatalf("missing version: %v", err)
	}
}

func TestMarketplaceVersionErrorHintAnnotationIsScoped(t *testing.T) {
	for _, tc := range []struct{ service, detail, want string }{
		{"marketplace", `{"error":{"details":{"field":"version","reason":"required"},"hint":"Supply version; update CLI if needed"}}`, "Supply version; update CLI if needed"},
		{"marketplace", `{"error":{"details":{"reason":"versioned_api_required"},"hint":"Upgrade CLI and use Plugin APIs"}}`, "Upgrade CLI and use Plugin APIs"},
		{"marketplace", `{"error":{"details":{"field":"visibility"},"hint":"other advice"}}`, "original hint"},
		{"loop", `{"error":{"details":{"field":"version"},"hint":"other advice"}}`, "original hint"},
	} {
		original := &output.ExitError{Type: "validation", Code: "VALIDATION_ERROR", Message: "refused", Hint: "original hint", Detail: json.RawMessage(tc.detail), HTTPStatus: 400}
		got := output.AsExitError(annotateMarketplaceVersionError(tc.service, original))
		if got == nil || got.Hint != tc.want || got.Code != original.Code || got.Type != original.Type || got.HTTPStatus != original.HTTPStatus || string(got.Detail) != tc.detail {
			t.Fatalf("error altered: %+v", got)
		}
		if original.Hint != "original hint" {
			t.Fatal("annotation mutated the original error")
		}
	}
}

func TestMarketplaceDraftRevisionRoundTrip(t *testing.T) {
	before, saved := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, method, path, inputRevision, outputRevision string
		args                                              []string
	}{
		{"read", "GET", "/market/api/v1/plugins/p1/draft", "", before, []string{"draft", "get", "--plugin-id", "p1"}},
		{"save", "POST", "/market/api/v1/plugins/upsert", before, saved, []string{"upsert", "--data", fmt.Sprintf(`{"plugin":{"plugin_id":"p1","plugin_type":"expert","plugin_name":"name","visibility":"private","manifest_json":{},"plugin_json":{}},"expected_draft_revision":"%s","relations":[]}`, before)}},
		{"publish", "POST", "/market/api/v1/plugins/publish", saved, "", []string{"publish", "--plugin-id", "p1", "--version", "1.2.0", "--expected-draft-revision", saved}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root, tf, _ := rootWithServiceSpaced(t, "qa", func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("X-Space-Id") != "qa" {
					t.Errorf("unexpected %s %s or Space", r.Method, r.URL.Path)
				}
				if tc.inputRevision != "" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["expected_draft_revision"] != tc.inputRevision {
						t.Errorf("conditional body = %v, err = %v", body, err)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":{"draft_revision":%q}}`, tc.outputRevision)
			})
			root.SetArgs(append([]string{"marketplace", "plugin"}, tc.args...))
			if err := root.Execute(); err != nil || calls != 1 {
				t.Fatalf("calls = %d, err = %v", calls, err)
			}
			if tc.outputRevision != "" && !strings.Contains(tf.Out.String(), tc.outputRevision) {
				t.Error("response did not preserve the revision for the next command")
			}
		})
	}
}
