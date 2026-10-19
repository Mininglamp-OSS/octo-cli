package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestSheetCleaningCommands(test *testing.T) {
	for _, token := range []string{"app_test", "bf_test"} {
		for _, operation := range []string{"split", "deduplicate"} {
			for _, apply := range []bool{false, true} {
				name := token + "/" + operation
				if apply {
					name += "/apply"
				} else {
					name += "/preview"
				}
				test.Run(name, func(test *testing.T) {
					var requests atomic.Int32
					env := newDriveTestEnv(test, token, func(writer http.ResponseWriter, request *http.Request) {
						requests.Add(1)
						if request.Method != http.MethodPost || request.URL.Path != "/v1/bot/docs/doc-1/sheet/"+operation {
							test.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
						}
						if request.Header.Get("If-Match") != "read-token" {
							test.Errorf("missing version guard: %q", request.Header.Get("If-Match"))
						}
						var body map[string]any
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
							test.Fatal(err)
						}
						if body["logicalId"] != "default" || body["range"] == nil {
							test.Errorf("missing selection: %+v", body)
						}
						if apply && body["preview"] != false {
							test.Errorf("apply must explicitly disable preview: %+v", body)
						}
						if !apply && body["preview"] != true {
							test.Errorf("default must explicitly request preview: %+v", body)
						}
						if operation == "split" && (body["delimiter"] != "," || apply && body["overwrite"] != true || !apply && body["overwrite"] == true) {
							test.Errorf("split options were not preserved: %+v", body)
						}
						if operation == "deduplicate" && (body["header"] != true || body["caseSensitive"] != true) {
							test.Errorf("deduplication options were not preserved: %+v", body)
						}
						writer.Header().Set("Content-Type", "application/json")
						_, _ = writer.Write([]byte(`{"docId":"doc-1","baseVersion":"next","removedRows":1}`))
					}, nil)
					args := []string{"docs", "sheet", operation, "doc-1", "--base-version", "read-token", "--data", `{"logicalId":"default","range":{"startRow":0,"endRow":9,"startColumn":0,"endColumn":0}}`}
					if operation == "split" {
						args = append(args, "--delimiter", ",")
						if apply {
							args = append(args, "--overwrite")
						}
					} else {
						args = append(args, "--header", "--case-sensitive")
					}
					if apply {
						args = append(args, "--preview=false")
					}
					if err := env.run(args...); err != nil {
						test.Fatalf("command failed: %v; stderr=%s", err, env.tf.ErrOut.String())
					}
					if requests.Load() != 1 {
						test.Fatalf("requests=%d, want one", requests.Load())
					}
				})
			}
		}
	}
}

func TestSheetConditionalFormatEdit(test *testing.T) {
	for _, token := range []string{"app_test", "bf_test"} {
		test.Run(token, func(test *testing.T) {
			env := newDriveTestEnv(test, token, func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPatch || request.URL.Path != "/v1/bot/docs/doc-1/sheet" || request.Header.Get("If-Match") != "read-token" {
					test.Errorf("unexpected conditional-format request")
				}
				var body struct {
					ConditionalFormats map[string]json.RawMessage `json:"conditionalFormats"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					test.Fatal(err)
				}
				if len(body.ConditionalFormats) != 2 || string(body.ConditionalFormats["default!old"]) != "null" {
					test.Errorf("sparse rule operations lost: %+v", body)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"docId":"doc-1","baseVersion":"next"}`))
			}, nil)
			if err := env.run("docs", "sheet", "edit", "doc-1", "--base-version", "read-token", "--data", `{"conditionalFormats":{"default!one":{"priority":0,"rule":{"cfId":"one","stopIfTrue":false,"ranges":[{"startRow":0,"endRow":9,"startColumn":0,"endColumn":0}],"rule":{"type":"highlightCell","subType":"duplicateValues","style":{"bg":{"rgb":"#FFCCCC"}}}}},"default!old":null}}`); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func TestSheetCleaningDryRunDoesNotSend(test *testing.T) {
	env := newDriveTestEnv(test, "app_test", func(http.ResponseWriter, *http.Request) { test.Error("unexpected HTTP request") }, nil)
	env.tf.Globals.DryRun = true
	env.tf.SetClient(client.New(&config.Config{APIBaseURL: env.api.URL, BotToken: "app_test"}, &credential.BotCredential{Token: "app_test", Source: "test"}, client.Options{DryRun: true, ErrOut: io.Discard}))
	err := env.run("docs", "sheet", "split", "doc-1", "--delimiter", ",", "--data", `{"logicalId":"default","range":{"startRow":0,"endRow":1,"startColumn":0,"endColumn":0}}`, "--base-version", "read-token", "--dry-run")
	if err != nil {
		test.Fatal(err)
	}
	var envelope struct {
		Data struct {
			Body map[string]any `json:"body"`
		} `json:"data"`
	}
	if err := json.Unmarshal(env.tf.Out.Bytes(), &envelope); err != nil {
		test.Fatal(err)
	}
	if envelope.Data.Body["preview"] != true {
		test.Fatalf("dry-run must show explicit preview: %+v", envelope.Data.Body)
	}
}

func TestSheetCleaningPreviewPrecedence(test *testing.T) {
	for _, operation := range []string{"split", "deduplicate"} {
		for _, scenario := range []struct {
			name      string
			dataValue any
			hasData   bool
			flag      string
			want      bool
		}{
			{name: "omitted", want: true},
			{name: "explicit preview", flag: "--preview", want: true},
			{name: "explicit apply", flag: "--preview=false", want: false},
			{name: "data apply", dataValue: false, want: false},
			{name: "data preview", dataValue: true, want: true},
			{name: "flag overrides data apply", dataValue: false, flag: "--preview", want: true},
			{name: "flag overrides data preview", dataValue: true, flag: "--preview=false", want: false},
			{name: "flag overrides null", hasData: true, flag: "--preview", want: true},
			{name: "flag overrides wrong type", dataValue: "false", flag: "--preview=false", want: false},
		} {
			test.Run(operation+"/"+scenario.name, func(test *testing.T) {
				var requests atomic.Int32
				env := newDriveTestEnv(test, "app_test", func(writer http.ResponseWriter, request *http.Request) {
					requests.Add(1)
					var body map[string]any
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						test.Fatal(err)
					}
					if body["preview"] != scenario.want {
						test.Errorf("preview = %#v, want %v", body["preview"], scenario.want)
					}
					_, _ = writer.Write([]byte(`{"docId":"doc-1"}`))
				}, nil)
				body := map[string]any{"logicalId": "default", "range": map[string]int{"startRow": 0, "endRow": 2, "startColumn": 0, "endColumn": 0}}
				if operation == "split" {
					body["delimiter"] = ","
				}
				if scenario.hasData || scenario.dataValue != nil {
					body["preview"] = scenario.dataValue
				}
				encoded, err := json.Marshal(body)
				if err != nil {
					test.Fatal(err)
				}
				args := []string{"docs", "sheet", operation, "doc-1", "--base-version", "token", "--data", string(encoded)}
				if scenario.flag != "" {
					args = append(args, scenario.flag)
				}
				if err := env.run(args...); err != nil {
					test.Fatal(err)
				}
				if requests.Load() != 1 {
					test.Fatalf("requests = %d", requests.Load())
				}
			})
		}
	}
}

func TestSheetCleaningBoundsBeforeHTTP(test *testing.T) {
	for _, operation := range []string{"split", "deduplicate"} {
		for _, coordinate := range []string{"startRow", "endRow", "startColumn", "endColumn"} {
			for _, invalid := range []any{-1, 0.5, "0"} {
				test.Run(operation+"/"+coordinate+"/"+stringMustJSON(test, invalid), func(test *testing.T) {
					env := newDriveTestEnv(test, "app_test", func(http.ResponseWriter, *http.Request) { test.Error("invalid range reached HTTP") }, nil)
					selected := map[string]any{"startRow": 0, "endRow": 2, "startColumn": 0, "endColumn": 0}
					selected[coordinate] = invalid
					body := map[string]any{"logicalId": "default", "range": selected}
					if operation == "split" {
						body["delimiter"] = ","
					}
					err := env.run("docs", "sheet", operation, "doc-1", "--base-version", "token", "--data", stringMustJSON(test, body))
					if err == nil {
						test.Fatal("expected local range validation")
					}
				})
			}
		}
	}
	for _, delimiter := range []string{"", strings.Repeat("x", 17)} {
		test.Run("delimiter/"+delimiter, func(test *testing.T) {
			env := newDriveTestEnv(test, "app_test", func(http.ResponseWriter, *http.Request) { test.Error("invalid delimiter reached HTTP") }, nil)
			err := env.run("docs", "sheet", "split", "doc-1", "--base-version", "token", "--delimiter", delimiter, "--data", `{"logicalId":"default","range":{"startRow":0,"endRow":2,"startColumn":0,"endColumn":0}}`)
			if err == nil {
				test.Fatal("expected local delimiter validation")
			}
		})
	}
}

func TestSheetCleaningBooleanFieldsBeforeHTTP(test *testing.T) {
	for _, token := range []string{"app_test", "bf_test"} {
		for _, operation := range []string{"split", "deduplicate"} {
			fields := []string{"preview", "overwrite"}
			if operation == "deduplicate" {
				fields = []string{"preview", "header", "caseSensitive"}
			}
			for _, field := range fields {
				for _, invalid := range []any{nil, "false", "true", 0, 1, []any{}, map[string]any{}} {
					test.Run(token+"/"+operation+"/"+field+"/"+stringMustJSON(test, invalid), func(test *testing.T) {
						var requests atomic.Int32
						env := newDriveTestEnv(test, token, func(writer http.ResponseWriter, _ *http.Request) {
							requests.Add(1)
							_, _ = writer.Write([]byte(`{"docId":"doc-1"}`))
						}, nil)
						body := map[string]any{"logicalId": "default", "range": map[string]int{"startRow": 0, "endRow": 2, "startColumn": 0, "endColumn": 0}, field: invalid}
						if operation == "split" {
							body["delimiter"] = ","
						}
						err := env.run("docs", "sheet", operation, "doc-1", "--base-version", "token", "--data", stringMustJSON(test, body))
						exit := output.AsExitError(err)
						if exit == nil || exit.Code != "VALIDATION_ERROR" || !strings.Contains(exit.Message, field) {
							test.Errorf("expected local boolean validation for %s, got %v", field, err)
						}
						if requests.Load() != 0 {
							test.Fatalf("malformed %s reached HTTP %d times", field, requests.Load())
						}
					})
				}
			}
		}
	}
}

func TestSheetCleaningUnavailableRoute(test *testing.T) {
	for _, operation := range []string{"split", "deduplicate"} {
		for _, scenario := range []struct {
			name string
			body string
			code string
			hint string
		}{
			{name: "missing route", body: "<pre>Cannot POST /v1/bot/docs/doc-1/sheet/" + operation + "</pre>", code: "SHEET_CLEANING_UNAVAILABLE", hint: "backend deployment"},
			{name: "ambiguous proxy response", body: "404 page not found", code: "NOT_FOUND", hint: "backend deployment"},
			{name: "missing document", body: `{"error":"not_found"}`, code: "not_found", hint: ""},
			{name: "missing sheet", body: `{"error":"sheet_not_found"}`, code: "sheet_not_found", hint: ""},
			{name: "explicit backend code", body: `{"error":{"code":"NOT_FOUND","message":"document hidden"}}`, code: "NOT_FOUND", hint: "resource not found"},
		} {
			test.Run(operation+"/"+scenario.name, func(test *testing.T) {
				var requests atomic.Int32
				env := newDriveTestEnv(test, "app_test", func(writer http.ResponseWriter, _ *http.Request) {
					requests.Add(1)
					writer.WriteHeader(http.StatusNotFound)
					_, _ = writer.Write([]byte(scenario.body))
				}, nil)
				body := map[string]any{"logicalId": "default", "range": map[string]int{"startRow": 0, "endRow": 1, "startColumn": 0, "endColumn": 0}}
				if operation == "split" {
					body["delimiter"] = ","
				}
				err := env.run("docs", "sheet", operation, "doc-1", "--base-version", "token", "--data", stringMustJSON(test, body))
				exit := output.AsExitError(err)
				if exit == nil || exit.Code != scenario.code || !strings.Contains(exit.Hint, scenario.hint) {
					test.Fatalf("unexpected route error: %+v", exit)
				}
				if exit.HTTPStatus != 404 || requests.Load() != 1 {
					test.Fatalf("route refusal must retain its status without fallback requests: %+v, requests=%d", exit, requests.Load())
				}
			})
		}
	}
}

func stringMustJSON(test *testing.T, value any) string {
	test.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		test.Fatal(err)
	}
	return string(encoded)
}
