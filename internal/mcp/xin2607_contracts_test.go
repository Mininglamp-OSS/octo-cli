package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestXIN2607RPCFoldTransportSafety(t *testing.T) {
	var hits atomic.Int32
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`{}`)) }))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, mode := range []struct {
			facade facadeMode
			tool   string
		}{{facadeTwo, "execute"}, {facadeThree, "call_op"}, {facadeBoth, "execute"}, {facadeBoth, "call_op"}} {
			s := newTestServer(t).WithFacade(mode.facade)
			s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
			h, err := testHTTPHandler(t, testRoot, TrustedContext{}, cmdutil.GlobalOptions{NoRetry: true}, mode.facade)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]string{"jsonrpc": `"2.0"`, "id": `1`, "method": `"tools/call"`, "params": xin2549Params(mode.tool, false)}
			for _, field := range []string{"jsonrpc", "id", "method", "params"} {
				aliases := []string{strings.ToUpper(field)}
				if field == "params" {
					aliases = append(aliases, "paramſ", `param\u017f`)
				}
				for _, alias := range aliases {
					for _, reverse := range []bool{false, true} {
						for _, equal := range []bool{false, true} {
							members := []string{}
							for _, k := range []string{"jsonrpc", "id", "method", "params"} {
								members = append(members, fmt.Sprintf(`%q:%s`, k, values[k]))
								if k == field {
									value := values[k]
									if !equal {
										value = `null`
										if k == "params" && mode.tool == "execute" {
											value = xin2549Params(mode.tool, true)
										}
									}
									item := `"` + alias + `":` + value
									if reverse {
										members = append(members[:len(members)-1], item, members[len(members)-1])
									} else {
										members = append(members, item)
									}
								}
							}
							wire := "{" + strings.Join(members, ",") + "}"
							var raw []byte
							if transport == "http" {
								r := httptest.NewRequest("POST", "/", strings.NewReader(wire))
								r.Header.Set("Authorization", "Bearer bf_fixture")
								w := httptest.NewRecorder()
								h.ServeHTTP(w, r)
								raw = w.Body.Bytes()
							} else {
								var out bytes.Buffer
								if err := s.ServeStdio(context.Background(), strings.NewReader(wire+"\n"), &out); err != nil {
									t.Fatal(err)
								}
								raw = out.Bytes()
							}
							var response rpcResponse
							if err := json.Unmarshal(raw, &response); err != nil {
								t.Fatal(err)
							}
							if hits.Load() != 0 || response.Error == nil || response.Error.Code != codeParseError {
								t.Fatalf("%s/%s/%s reverse=%v equal=%v: logical collision dispatched", transport, mode.facade, field, reverse, equal)
							}
						}
					}
				}
			}
		}
	}
}

func TestXIN2607RPCFoldCollisions(t *testing.T) {
	for _, field := range []string{"jsonrpc", "id", "method", "params"} {
		aliases := []string{strings.ToUpper(field)}
		if field == "params" {
			aliases = append(aliases, "paramſ", `param\u017f`)
		}
		for _, alias := range aliases {
			for _, reverse := range []bool{false, true} {
				for _, equal := range []bool{false, true} {
					a, b := field, alias
					if reverse {
						a, b = b, a
					}
					v := `null`
					if !equal {
						v = `{}`
					}
					wire := `{"` + a + `":null,"` + b + `":` + v + `}`
					var req rpcRequest
					if err := decodeRPCRequest([]byte(wire), &req); err == nil {
						t.Errorf("accepted logical duplicate: %s", wire)
					}
				}
			}
		}
	}
	for _, wire := range []string{`{"ID":1,"Method":"ping","paramſ":{}}`, `{"id":1,"method":"ping","extra":1,"Extra":2}`} {
		var req rpcRequest
		if err := decodeRPCRequest([]byte(wire), &req); err != nil {
			t.Fatal(err)
		}
	}
}

func TestXIN2607ExactEnvelopeNumbers(t *testing.T) {
	for _, okRun := range []bool{true, false} {
		for _, dry := range []bool{true, false} {
			wire := fmt.Sprintf(`{"ok":%t,"data":{"id":9007199254740993,"nested":[18446744073709551615,-9223372036854775808,9223372036854775807,1.234567890123456789]}}`, okRun)
			wire = string(spliceForcedArguments([]byte(wire), map[string]bool{"channel_id": true}))
			res := classifyExecuteResult([]byte(wire), okRun, "message.send", "fingerprint", dry, "POST", nil)
			for _, number := range []string{"9007199254740993", "18446744073709551615", "-9223372036854775808", "9223372036854775807", "1.234567890123456789"} {
				if !strings.Contains(res.Content[0].Text, number) {
					t.Errorf("lost exact business number %s", number)
				}
			}
		}
	}
	for _, invalid := range []string{`{} {}`, `null`, `[]`, `{"ok":true} trailing`} {
		if _, ok := decodeEnvelope([]byte(invalid))["raw"]; !ok {
			t.Errorf("accepted incomplete/non-object envelope %q", invalid)
		}
	}
}

func TestXIN2607UploadDryRunShared(t *testing.T) {
	s := newTestServer(t)
	root := t.TempDir()
	const contents = "PRIVATE_FIXTURE_UPLOAD_BYTES"
	if err := os.WriteFile(filepath.Join(root, "real"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	s.uploadRoot = root
	s.baseGlobals.DryRun = true
	opens := 0
	s.uploadOpener = func(string, execPolicy) (*os.File, error) {
		opens++
		return nil, fmt.Errorf("unexpected upload open")
	}
	s.factoryFn = fakeBackendFactory("http://127.0.0.1:9", "bf_test")
	if err := os.WriteFile(filepath.Join(root, "unreadable"), []byte(contents), 0o000); err != nil {
		t.Fatal(err)
	}
	for _, httpMode := range []bool{false, true} {
		s.httpMode = httpMode
		for _, global := range []bool{false, true} {
			s.baseGlobals.DryRun = global
			for _, tool := range []string{"call_op", "execute"} {
				if !global && tool == "call_op" {
					continue
				}
				for _, op := range []string{"file.upload", "docs.raw-import.markdown"} {
					for _, file := range []string{"real", "missing", "unreadable"} {
						t.Run(tool+"/"+op+"/"+file, func(t *testing.T) {
							args := map[string]any{"file_path": filepath.Join(root, file)}
							if op == "docs.raw-import.markdown" {
								args["docId"] = "fixture"
							}
							var res toolResult
							if tool == "execute" {
								res = s.execute(context.Background(), op, args, !global, "")
							} else {
								res = s.callOp(context.Background(), op, args)
							}
							if res.IsError || strings.Contains(res.Content[0].Text, contents) || !strings.Contains(res.Content[0].Text, `"file_path"`) {
								t.Fatalf("preview must describe without opening: %s", res.Content[0].Text)
							}
							if opens != 0 {
								t.Fatal("upload preview attempted to open a file")
							}
						})
					}
				}
			}
		}
	}
	s.baseGlobals.DryRun = false
	res := s.callOp(context.Background(), "file.upload", map[string]any{"file_path": filepath.Join(root, "real")})
	if !res.IsError || opens != 1 {
		t.Fatal("live control did not reach upload boundary")
	}

}

func TestXIN2607ArrayBridge(t *testing.T) {
	var mu sync.Mutex
	var received []string
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := "tag"
		if r.URL.Query().Has("creator") {
			key = "creator"
		}
		if r.URL.Query().Has("type") {
			key = "type"
		}
		received = append([]string(nil), r.URL.Query()[key]...)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer backend.Close()
	s := newTestServer(t)
	s.factoryFn = fakeBackendFactory(backend.URL, "bf_test")
	for _, tc := range []struct {
		op, key string
		args    map[string]any
	}{
		{"plugin.list", "tag", map[string]any{"scene_code": "default", "plugin_type": "expert"}},
		{"docs.recent.list", "creator", map[string]any{}},
		{"docs.list", "type", map[string]any{}},
		{"docs.recent.list", "type", map[string]any{}},
	} {
		for _, values := range [][]string{{"team,core"}, {`a"b`}, {""}, {"line\r\nbreak"}, {"doc", "sheet"}, {}} {
			for _, tool := range []string{"call_op", "execute"} {
				t.Run(tc.op+"/"+tc.key+"/"+tool+fmt.Sprint(values), func(t *testing.T) {
					args := map[string]any{}
					for k, v := range tc.args {
						args[k] = v
					}
					arr := make([]any, len(values))
					for i, v := range values {
						arr[i] = v
					}
					args[tc.key] = arr
					mu.Lock()
					received = nil
					mu.Unlock()
					var res toolResult
					if tool == "execute" {
						res = s.execute(context.Background(), tc.op, args, false, "")
					} else {
						res = s.callOp(context.Background(), tc.op, args)
					}
					invalidEnum := tc.key == "type" && len(values) > 0 && !reflect.DeepEqual(values, []string{"doc", "sheet"})
					mu.Lock()
					defer mu.Unlock()
					if invalidEnum {
						if !res.IsError || received != nil {
							t.Fatal("invalid single enum element was split or sent")
						}
						return
					}
					if res.IsError {
						t.Fatalf("typed slice refused: %s", res.Content[0].Text)
					}
					if len(values) != len(received) || (len(values) > 0 && !reflect.DeepEqual(values, received)) {
						t.Errorf("query slice changed: got %q want %q", received, values)
					}
				})
			}
		}
	}
}

func TestXIN2607RetryAdvice(t *testing.T) {
	s := newTestServer(t)
	for _, res := range []toolResult{s.describeOp("docs.create"), s.describeSkill("docs.create", "full")} {
		text := res.Content[0].Text
		if !strings.Contains(text, "html_ppt") || !strings.Contains(text, "inspect server state") || strings.Contains(text, "optional Idempotency-Key header dedupes retries") {
			t.Fatal("docs.create retry advice overstates the ordinary create contract")
		}
		if !json.Valid([]byte(text)) {
			t.Fatal("descriptor not JSON")
		}
	}
}

func TestXIN2607ProjectExposure(t *testing.T) {
	s := newTestServer(t)
	for _, op := range []string{"project.list", "project.search", "project.get", "project.create", "project.update", "project.delete", "project.resource.list", "project.resource.create", "project.resource.update", "project.resource.delete"} {
		if _, ok := s.reg.GetOperation(op); !ok {
			t.Fatalf("lost upstream API operation %s", op)
		}
		if mcpExposed(op) {
			t.Fatalf("unapproved MCP exposure for %s", op)
		}
		if !s.describeOp(op).IsError || !s.describeSkill(op, "full").IsError {
			t.Fatalf("withheld %s was advertised", op)
		}
		if !s.callOp(context.Background(), op, nil).IsError || !s.execute(context.Background(), op, nil, false, "").IsError {
			t.Fatalf("withheld %s was executable", op)
		}
	}
}
