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
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestPptAdvisoryHashDoesNotHideSuccessfulResponse(t *testing.T) {
	for _, verb := range []string{"get", "edit"} {
		for _, hash := range []string{``, `,"contentHash":""`} {
			t.Run(verb+hash, func(t *testing.T) {
				payload := `{"docId":"d_1","deck":{},"baseRevision":0` + hash + `}`
				if verb == "edit" {
					payload = `{"revision":0,"changed":false` + hash + `}`
				}
				root, out, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":` + payload + `}`))
				})
				args := []string{"docs", "ppt", verb, "d_1"}
				if verb == "edit" {
					args = append(args, "--data", `{"baseRevision":0,"deck":{}}`)
				}
				root.SetArgs(args)
				if err := root.Execute(); err != nil {
					t.Fatalf("successful %s must not depend on advisory hash: %v", verb, err)
				}
				var env struct {
					OK   bool
					Data json.RawMessage
				}
				if err := json.Unmarshal(out.Out.Bytes(), &env); err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal(env.Data, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(payload), &want); err != nil {
					t.Fatal(err)
				}
				if !env.OK || !reflect.DeepEqual(got, want) {
					t.Fatalf("receipt changed: %s", out.Out.String())
				}
			})
		}
	}
}

func TestPptResponseStillRequiresUsableCoreFields(t *testing.T) {
	for _, tc := range []struct{ verb, payload string }{
		{"get", `{"docId":"","deck":{},"baseRevision":0}`},
		{"get", `{"docId":"d_1","baseRevision":0}`},
		{"get", `{"docId":"d_1","deck":{},"baseRevision":null}`},
		{"edit", `{"changed":false}`},
		{"edit", `{"revision":0,"changed":null}`},
	} {
		t.Run(tc.verb+tc.payload, func(t *testing.T) {
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":` + tc.payload + `}`))
			})
			args := []string{"docs", "ppt", tc.verb, "d_1"}
			if tc.verb == "edit" {
				args = append(args, "--data", `{"baseRevision":0,"deck":{}}`)
			}
			root.SetArgs(args)
			err := output.AsExitError(root.Execute())
			if err == nil || err.Code != "RESPONSE_UNWRAP" {
				t.Fatalf("invalid core payload accepted: %v", err)
			}
		})
	}
}

func TestCommentIdempotencyKeyExplicitBlankRejected(t *testing.T) {
	for _, key := range []string{"", "   ", "stable"} {
		t.Run(key, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Idempotency-Key") != key {
					t.Errorf("key changed: %q", r.Header.Get("Idempotency-Key"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":1}`))
			})
			root.SetArgs([]string{"docs", "comments", "add", "d_1", "--body", "Review", "--idempotency-key", key})
			err := root.Execute()
			if strings.TrimSpace(key) == "" {
				exit := output.AsExitError(err)
				if exit == nil || exit.Code != "VALIDATION_ERROR" || calls != 0 {
					t.Fatalf("blank key must fail before HTTP: err=%v calls=%d", err, calls)
				}
			} else if err != nil || calls != 1 {
				t.Fatalf("valid key rejected: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCommonCommentOmittedKeyRemainsOptional(t *testing.T) {
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Idempotency-Key"]; present {
			t.Error("omitted key must remain absent")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1}`))
	})
	root.SetArgs([]string{"docs", "comments", "add", "d_1", "--body", "Review"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestCommentRepliesPageAllPreservesLargeNumericCursor(t *testing.T) {
	var cursors []string
	root, out, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		cursors = append(cursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if len(cursors) == 1 {
			_, _ = w.Write([]byte(`{"items":[{"body":"first"}],"nextCursor":9007199254740993}`))
		} else {
			_, _ = w.Write([]byte(`{"items":[{"body":"second"}],"nextCursor":null}`))
		}
	})
	root.SetArgs([]string{"docs", "comments", "replies", "d_1", "1", "--page-all"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cursors, []string{"", "9007199254740993"}) {
		t.Fatalf("cursor rounded or failed to stop: %q", cursors)
	}
	var env struct{ Data []struct{ Body string } }
	if err := json.Unmarshal(out.Out.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 2 || env.Data[0].Body != "first" || env.Data[1].Body != "second" {
		t.Fatalf("pages not merged: %s", out.Out.String())
	}
}

func TestUnwrapIntegerPreservesUint64Range(t *testing.T) {
	for _, tc := range []struct {
		raw string
		bad bool
	}{
		{"0", false}, {"18446744073709551615", false}, {`"7"`, true}, {"1.5", true}, {"null", true}, {"true", true},
	} {
		if got := unusableUnwrapValue(json.RawMessage(tc.raw), "integer"); got != tc.bad {
			t.Errorf("integer %s unusable=%v want=%v", tc.raw, got, tc.bad)
		}
	}
}

// Exercise argument resolution without a listener or external HTTP service.
func TestPptExportSavesFile(t *testing.T) {
	const html = "<!doctype html><title>Deck</title><p>Offline slides</p>"
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/bot/docs/d_1/ppt/export" || r.URL.Query().Get("format") != "html" {
			t.Errorf("request = %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Disposition", `attachment; filename="slides.html"`)
		_, _ = w.Write([]byte(html))
	})
	if findCmd(findCmd(findCmd(root, "docs"), "ppt"), "export") == nil {
		t.Fatal("missing docs ppt export command")
	}
	path := filepath.Join(t.TempDir(), "slides.html")
	root.SetArgs([]string{"docs", "ppt", "export", "d_1", "--file-format", "html", "--output", path})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != html {
		t.Fatalf("exported bytes = %q", data)
	}
}

func TestPptReadEdit(t *testing.T) {
	for _, verb := range []string{"get", "edit"} {
		t.Run(verb, func(t *testing.T) {
			calls := 0
			root, out, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				method := "GET"
				if verb == "edit" {
					method = "PATCH"
				}
				if r.Method != method || r.URL.Path != "/v1/bot/docs/d_1/ppt" {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("X-Space-Id") != "" {
					t.Error("PPT must not send a client-selected Space")
				}
				if verb == "edit" {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if body["baseRevision"] != float64(7) || body["deck"] == nil {
						t.Errorf("edit body = %#v", body)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if verb == "get" {
					_, _ = w.Write([]byte(`{"data":{"docId":"d_1","baseRevision":7,"deck":{"title":"Hello"},"contentHash":"hash"}}`))
				} else {
					_, _ = w.Write([]byte(`{"data":{"revision":8,"changed":true,"contentHash":"hash"}}`))
				}
			})
			docs := findCmd(root, "docs")
			if findCmd(docs, "ppt") == nil {
				t.Fatal("missing docs ppt command")
			}
			args := []string{"docs", "ppt", verb, "d_1"}
			if verb == "edit" {
				args = append(args, "--data", `{"baseRevision":7,"deck":{"format":"bento/slides","version":1,"slides":[]}}`)
			}
			root.SetArgs(args)
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
			var env struct{ Data map[string]any }
			if err := json.Unmarshal(out.Out.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if env.Data["contentHash"] != "hash" || env.Data["data"] != nil {
				t.Fatalf("PPT response not unwrapped: %s", out.Out.String())
			}
		})
	}
}

func TestPptEditRequiresRevisionAndDeck(t *testing.T) {
	for _, body := range []string{`{}`, `{"deck":{}}`, `{"baseRevision":0}`, `{"baseRevision":null,"deck":{}}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
			if findCmd(findCmd(root, "docs"), "ppt") == nil {
				t.Fatal("missing docs ppt command")
			}
			root.SetArgs([]string{"docs", "ppt", "edit", "d_1", "--data", body})
			if err := root.Execute(); err == nil {
				t.Fatal("missing revision/deck accepted")
			}
			if calls != 0 {
				t.Fatal("invalid edit reached server")
			}
		})
	}
}

func TestPptStrictInputRejectsUnknownFieldsBeforeHTTP(t *testing.T) {
	root, _, _ := rootWithServiceSpaced(t, "", func(w http.ResponseWriter, r *http.Request) { t.Error("invalid request reached HTTP") })
	root.SetArgs([]string{"docs", "ppt", "edit", "d_1", "--data", `{"baseRevision":0,"deck":{},"docId":"unexpected"}`})
	err := root.Execute()
	exit := output.AsExitError(err)
	if exit == nil || exit.Code != "VALIDATION_ERROR" {
		t.Fatalf("expected local validation error, got %#v", exit)
	}
}

func TestPptUsesCommonLifecycleCommands(t *testing.T) {
	root, _, _ := rootWithService(t, func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP") })
	ppt := findCmd(findCmd(root, "docs"), "ppt")
	for _, name := range []string{"create", "comments", "versions", "publish", "restore"} {
		if findCmd(ppt, name) != nil {
			t.Errorf("duplicate PPT lifecycle command still exposed: %s", name)
		}
	}
}

func TestPptCommonLifecycleRequests(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, response string
		args                         []string
		body                         map[string]any
	}{
		{"create", "POST", "/v1/bot/docs", `{"docId":"deck","docType":"html_ppt"}`,
			[]string{"create", "--docType", "html_ppt", "--title", "Deck", "--templateId", "blank", "--idempotency-key", "stable"}, map[string]any{"docType": "html_ppt", "title": "Deck", "templateId": "blank"}},
		{"comment", "POST", "/v1/bot/docs/deck/comments", `{"id":1}`,
			[]string{"comments", "add", "deck", "--idempotency-key", "stable", "--data", `{"body":"Review","anchor":{"kind":"document","versionSeq":null,"baseRevision":7}}`},
			map[string]any{"body": "Review", "anchor": map[string]any{"kind": "document", "versionSeq": nil, "baseRevision": float64(7)}}},
		{"reply", "POST", "/v1/bot/docs/deck/comments", `{"id":2}`,
			[]string{"comments", "add", "deck", "--body", "Done", "--parentId", "1", "--idempotency-key", "stable"}, map[string]any{"body": "Done", "parentId": float64(1)}},
		{"resolve", "PATCH", "/v1/bot/docs/deck/comments/1", `{"id":1}`,
			[]string{"comments", "edit", "deck", "1", "--revision", "2", "--resolved=true"}, map[string]any{"revision": float64(2), "resolved": true}},
		{"snapshot", "POST", "/v1/bot/docs/deck/versions", `{"docVersionSeq":3}`,
			[]string{"versions", "create", "deck", "--baseRevision", "7", "--label", "Review", "--idempotency-key", "stable"}, map[string]any{"baseRevision": float64(7), "label": "Review"}},
		{"restore", "POST", "/v1/bot/docs/deck/versions/3/restore", `{"restoredFrom":3,"newDocVersionSeq":4}`,
			[]string{"versions", "restore", "deck", "3", "--baseRevision", "8", "--idempotency-key", "stable"}, map[string]any{"baseRevision": float64(8)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			root, out, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request=%s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(body, tc.body) {
					t.Errorf("body=%#v want=%#v", body, tc.body)
				}
				if tc.method == "POST" && r.Header.Get("Idempotency-Key") != "stable" {
					t.Error("missing replay identity")
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.method == "POST" && tc.name != "restore" {
					w.WriteHeader(201)
				}
				_, _ = w.Write([]byte(tc.response))
			})
			root.SetArgs(append([]string{"docs"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("requests=%d", calls)
			}
			var result struct{ Data map[string]any }
			if err := json.Unmarshal(out.Out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if len(result.Data) == 0 || result.Data["data"] != nil {
				t.Fatalf("invalid common response: %s", out.Out.String())
			}
		})
	}
}

func TestCommonPptCommentReadsAndDelete(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		args         []string
	}{
		{"GET", "/v1/bot/docs/deck/comments/1", []string{"comments", "get", "deck", "1"}},
		{"GET", "/v1/bot/docs/deck/comments/1/replies?cursor=4", []string{"comments", "replies", "deck", "1", "--cursor", "4"}},
		{"GET", "/v1/bot/docs/deck/versions/3/state", []string{"versions", "state", "deck", "3"}},
		{"DELETE", "/v1/bot/docs/deck/comments/1?hard=1&revision=2", []string{"comments", "delete", "deck", "1", "--revision", "2", "--hard", "1"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method || r.URL.RequestURI() != tc.path {
					t.Errorf("request=%s %s", r.Method, r.URL.RequestURI())
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[],"nextCursor":null,"id":1,"deck":{},"docVersionSeq":3,"comment":{"id":1},"root":{"id":1}}`))
			})
			root.SetArgs(append([]string{"docs"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPptEditRejectsInvalidRevision(t *testing.T) {
	for _, revision := range []string{`"7"`, `-5`, `1.5`, `true`, `9007199254740992`} {
		t.Run(revision, func(t *testing.T) {
			calls := 0
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
			root.SetArgs([]string{"docs", "ppt", "edit", "d_1", "--data", `{"baseRevision":` + revision + `,"deck":{}}`})
			err := root.Execute()
			if err == nil || output.AsExitError(err) == nil || output.AsExitError(err).Code != "VALIDATION_ERROR" {
				t.Fatalf("invalid revision must fail validation before HTTP: %v", err)
			}
			if calls != 0 {
				t.Fatalf("invalid revision reached HTTP: %d", calls)
			}
		})
	}
}

func TestCommonCommentReadRejectsMissingPayload(t *testing.T) {
	for _, args := range [][]string{
		{"docs", "comments", "get", "d_1", "1"},
		{"docs", "comments", "replies", "d_1", "1"},
		{"docs", "comments", "replies", "d_1", "1", "--page-all"},
	} {
		t.Run(args[2]+args[len(args)-1], func(t *testing.T) {
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			})
			root.SetArgs(args)
			err := root.Execute()
			if err == nil || output.AsExitError(err) == nil || output.AsExitError(err).Code != "RESPONSE_SCHEMA" {
				t.Fatalf("malformed read must fail instead of claiming empty results: %v", err)
			}
		})
	}
}

func TestStrictNumericBoundsPreserveExactIntegers(t *testing.T) {
	minimum, maximum := float64(0), float64(10)
	for _, tc := range []struct {
		value any
		bad   bool
	}{
		{json.Number("0"), false}, {json.Number("10"), false}, {json.Number("11"), true},
		{json.Number("-1"), true}, {json.Number("1.5"), true}, {"1", true},
	} {
		schema := &registry.SchemaInfo{Type: "integer", Minimum: &minimum, Maximum: &maximum}
		if err := validateNumber(schema, tc.value, "revision"); (err != nil) != tc.bad {
			t.Errorf("value=%v err=%v", tc.value, err)
		}
	}
	if err := validateNumber(&registry.SchemaInfo{Type: "integer"}, json.Number("18446744073709551615"), "id"); err != nil {
		t.Fatal(err)
	}
}

func TestPptEditAcceptsMaximumSafeRevision(t *testing.T) {
	calls := 0
	root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		var body map[string]any
		if err := decoder.Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["baseRevision"] != json.Number("9007199254740991") {
			t.Fatalf("revision=%v", body["baseRevision"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"revision":9007199254740991,"changed":false,"contentHash":"hash"}}`))
	})
	root.SetArgs([]string{"docs", "ppt", "edit", "d_1", "--data", `{"baseRevision":9007199254740991,"deck":{}}`})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("requests=%d", calls)
	}
}
