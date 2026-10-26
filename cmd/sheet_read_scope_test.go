package cmd

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestSheetDirectoryCommand(test *testing.T) {
	for _, token := range []string{"app_test", "bf_test"} {
		test.Run(token, func(test *testing.T) {
			var requests atomic.Int32
			env := newDriveTestEnv(test, token, func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Method != http.MethodGet || request.URL.Path != "/v1/bot/docs/doc-1/sheets" || request.URL.RawQuery != "" {
					test.Errorf("unexpected directory request: %s %s", request.Method, request.URL)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"docId":"doc-1","items":[{"sheetId":"sales","name":"新剧榜单","order":1,"rowCount":200,"columnCount":20}]}`))
			}, nil)
			if err := env.run("docs", "sheet", "list", "doc-1"); err != nil {
				test.Fatal(err)
			}
			if requests.Load() != 1 || !strings.Contains(env.tf.Out.String(), "新剧榜单") {
				test.Fatalf("directory not returned: %s", env.tf.Out.String())
			}
		})
	}
}

func TestWorksheetScopedReadCommands(test *testing.T) {
	for _, token := range []string{"app_test", "bf_test"} {
		for _, selector := range []struct{ flag, key, value string }{
			{"--sheet-id", "sheetId", "sales"},
			{"--sheet-name", "sheetName", "新剧 & 榜单 + 10月"},
		} {
			test.Run(token+selector.flag, func(test *testing.T) {
				env := newDriveTestEnv(test, token, func(writer http.ResponseWriter, request *http.Request) {
					if request.Method != http.MethodGet || request.URL.Path != "/v1/bot/docs/doc-1/sheet" ||
						request.URL.Query().Get(selector.key) != selector.value || request.URL.Query().Get("limit") != "1" ||
						request.URL.Query().Get("cursor") != "opaque-cursor" {
						test.Errorf("selection or cursor lost: %s %s", request.Method, request.URL)
					}
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(`{"docId":"doc-1","sheetId":"sales","sheetCells":{"sales!1:0":{"v":"read"}},"baseVersion":"unchanged","hasMore":false,"nextCursor":null}`))
				}, nil)
				if err := env.run("docs", "sheet", "get", "doc-1", selector.flag, selector.value, "--limit", "1", "--cursor", "opaque-cursor"); err != nil {
					test.Fatal(err)
				}
				if env.data(test)["sheetId"] != "sales" {
					test.Fatal("resolved worksheet identity missing")
				}
			})
		}
	}
}

func TestWorksheetSelectionRejectedBeforeRequest(test *testing.T) {
	for _, flags := range [][]string{
		{"--sheet-id", "sales", "--sheet-name", "榜单"},
		{"--sheet-id", ""}, {"--sheet-name", " "},
	} {
		test.Run(strings.Join(flags, "/"), func(test *testing.T) {
			var requests atomic.Int32
			env := newDriveTestEnv(test, "bf_test", func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
			}, nil)
			args := append([]string{"docs", "sheet", "get", "doc-1"}, flags...)
			if err := env.run(args...); err == nil || requests.Load() != 0 {
				test.Fatalf("invalid selector reached network: err=%v requests=%d", err, requests.Load())
			}
		})
	}
}

func TestScopedReadRejectsOldOrMismatchedBackend(test *testing.T) {
	for _, scenario := range []struct{ name, body, code string }{
		{"old-backend", `{"docId":"doc-1","sheetCells":{"default!0:0":{"v":"foreign"}}}`, "SHEET_READ_SCOPE_UNAVAILABLE"},
		{"wrong-id", `{"sheetId":"default","sheetCells":{}}`, "SHEET_READ_SCOPE_MISMATCH"},
		{"foreign-cells", `{"sheetId":"sales","sheetCells":{"sales2!0:0":{"v":"foreign"}}}`, "SHEET_READ_SCOPE_MISMATCH"},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			var requests atomic.Int32
			env := newDriveTestEnv(test, "bf_test", func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(scenario.body))
			}, nil)
			err := env.run("docs", "sheet", "get", "doc-1", "--sheet-id", "sales")
			exit := output.AsExitError(err)
			if exit == nil || exit.Code != scenario.code || requests.Load() != 1 {
				test.Fatalf("expected fail-closed %s, got %v (%d requests)", scenario.code, err, requests.Load())
			}
			if strings.Contains(env.tf.Out.String(), `"ok": true`) {
				test.Fatal("foreign workbook data was emitted as a scoped success")
			}
		})
	}
}

func TestWorksheetDiscoveryErrorsDoNotFallBack(test *testing.T) {
	for _, scenario := range []struct {
		operation  string
		status     int
		body, code string
		flags      []string
	}{
		{"list", 404, `{"error":"not_found"}`, "", nil},
		{"get", 404, `{"error":"sheet_not_found"}`, "sheet_not_found", []string{"--sheet-name", "missing"}},
		{"get", 409, `{"error":"ambiguous_sheet_name","candidates":[{"sheetId":"one","name":"榜单"},{"sheetId":"two","name":"榜单"}]}`, "ambiguous_sheet_name", []string{"--sheet-name", "榜单"}},
		{"list", 403, `{"error":"forbidden"}`, "", nil},
	} {
		test.Run(scenario.operation+scenario.code+scenario.body, func(test *testing.T) {
			var requests atomic.Int32
			env := newDriveTestEnv(test, "bf_test", func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(scenario.status)
				_, _ = writer.Write([]byte(scenario.body))
			}, nil)
			err := env.run(append([]string{"docs", "sheet", scenario.operation, "doc-1"}, scenario.flags...)...)
			exit := output.AsExitError(err)
			if exit == nil || requests.Load() != 1 || (scenario.code != "" && exit.Code != scenario.code) {
				test.Fatalf("unexpected error/fallback: %v requests=%d", err, requests.Load())
			}
			if scenario.code == "ambiguous_sheet_name" {
				var detail struct {
					Candidates []any `json:"candidates"`
				}
				if json.Unmarshal(exit.Detail, &detail) != nil || len(detail.Candidates) != 2 {
					test.Fatal("candidate IDs were lost from error detail")
				}
			}
		})
	}
}
