package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

const rcMessageArgs = `{"channel_id":"g","channel_type":2,"payload":{"type":1,"content":"fixture"}}`

func TestRCCallOpStrictAcrossTransports(t *testing.T) {
	for _, facade := range []facadeMode{facadeThree, facadeBoth} {
		for _, transport := range []string{"stdio", "http"} {
			t.Run(fmt.Sprintf("%v/%s", facade, transport), func(t *testing.T) {
				var hits atomic.Int32
				be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`{"message_id":1}`)) }))
				defer be.Close()
				s := newTestServer(t).WithFacade(facade)
				s.factoryFn = fakeBackendFactory(be.URL, "bf_test")
				s.httpMode = transport == "http"
				cases := []string{
					`"dry_run":true`, `"schema_fingerprint":"stale"`, `"bogus":1`, `"operation_id":"message.send"`,
				}
				for _, extra := range cases {
					params := fmt.Sprintf(`{"name":"call_op","arguments":{"operation_id":"message.send","arguments":%s,%s}}`, rcMessageArgs, extra)
					before := hits.Load()
					var resp rpcResponse
					if transport == "http" {
						h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facade)
						if err != nil {
							t.Fatal(err)
						}
						// Exercise HTTP framing and the real per-request factory, not a stub dispatcher.
						t.Setenv("OCTO_API_BASE_URL", be.URL)
						t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
						req := httptest.NewRequest("POST", "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+`}`))
						req.Header.Set("Authorization", "Bearer bf_test")
						rec := httptest.NewRecorder()
						h.ServeHTTP(rec, req)
						if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
							t.Fatal(err)
						}
					} else {
						var out bytes.Buffer
						if err := s.ServeStdio(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`+params+"}\n"), &out); err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
							t.Fatal(err)
						}
					}
					if resp.Error == nil || resp.Error.Code != codeInvalidParams || hits.Load() != before {
						t.Errorf("unsupported/duplicate call_op field %s: expected -32602 and zero backend requests; response=%+v hits=%d", extra, resp, hits.Load()-before)
					}
				}
				res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.send", "arguments": map[string]any{"channel_id": "g", "channel_type": 2, "payload": map[string]any{"type": 1, "content": "valid"}}})
				if res.IsError {
					t.Fatalf("legitimate live call must work: %s", res.Content[0].Text)
				}
			})
		}
	}
}

func TestRCMutationDefaultDoesNotRetry(t *testing.T) {
	for _, tool := range []string{"call_op", "execute"} {
		t.Run(tool, func(t *testing.T) {
			var commits atomic.Int32
			be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := commits.Add(1)
				if n == 1 {
					w.WriteHeader(502)
					_, _ = w.Write([]byte(`{"code":"UPSTREAM_UNAVAILABLE"}`))
					return
				}
				_, _ = w.Write([]byte(`{"message_id":2}`))
			}))
			defer be.Close()
			s := newTestServer(t).WithFacade(facadeBoth)
			t.Setenv("OCTO_API_BASE_URL", be.URL)
			t.Setenv("OCTO_BOT_TOKEN", "bf_test")
			t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
			var a map[string]any
			_ = json.Unmarshal([]byte(rcMessageArgs), &a)
			r := callTool(t, s, tool, map[string]any{"operation_id": "message.send", "arguments": a})
			if commits.Load() != 1 || !r.IsError {
				t.Fatalf("one intent must not commit twice after a lost response: commits=%d result=%s", commits.Load(), r.Content[0].Text)
			}
			if !strings.Contains(r.Content[0].Text, "RESULT_UNKNOWN") {
				t.Fatalf("ambiguous write needs RESULT_UNKNOWN: %s", r.Content[0].Text)
			}
			if tool == "execute" {
				x := toolText(t, r)
				if x["status"] != "result_unknown" || x["retryable"] != false {
					t.Fatalf("possibly committed mutation needs result_unknown and retryable=false: %v", x)
				}
			}
		})
	}
}

func TestRCRemoteFailureClassification(t *testing.T) {
	for _, op := range []string{"message.send", "docs.get", "docs.search"} {
		for _, mode := range []string{"mapped-validation", "mapped-auth", "html", "truncated"} {
			t.Run(op+"/"+mode, func(t *testing.T) {
				var hits atomic.Int32
				be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					hits.Add(1)
					switch mode {
					case "mapped-validation":
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":{"code":"VALIDATION_ERROR"}}`))
					case "mapped-auth":
						w.WriteHeader(503)
						_, _ = w.Write([]byte(`{"error":{"code":"UNAUTHORIZED"}}`))
					case "html":
						_, _ = w.Write([]byte(`<html>SPA</html>`))
					default:
						_, _ = w.Write([]byte(`{"data":`))
					}
				}))
				defer be.Close()
				s := newTestServer(t).WithFacade(facadeBoth)
				s.baseGlobals.NoRetry = true
				t.Setenv("OCTO_API_BASE_URL", be.URL)
				t.Setenv("OCTO_BOT_TOKEN", "bf_test")
				t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
				args := map[string]any{"docId": "d"}
				if op == "docs.search" {
					args = map[string]any{"q": "fixture"}
				}
				if op == "message.send" {
					args = map[string]any{}
					_ = json.Unmarshal([]byte(rcMessageArgs), &args)
				}
				r := callTool(t, s, "execute", map[string]any{"operation_id": op, "arguments": args})
				x := toolText(t, r)
				want := "execution_error"
				if op == "message.send" {
					want = "result_unknown"
				}
				if !r.IsError || x["status"] != want || hits.Load() != 1 {
					t.Fatalf("remote %s on %s must be %s after one request; got %v hits=%d", mode, op, want, x, hits.Load())
				}
				if want == "result_unknown" && x["retryable"] != false {
					t.Fatal("uncertain write must explicitly forbid automatic retry")
				}
				if op == "message.send" && (mode == "mapped-validation" || mode == "mapped-auth") {
					three := callTool(t, s, "call_op", map[string]any{"operation_id": op, "arguments": args})
					errorObject, _ := toolText(t, three)["error"].(map[string]any)
					if !three.IsError || errorObject["code"] != "RESULT_UNKNOWN" || !strings.Contains(strings.ToLower(errorObject["hint"].(string)), "do not retry automatically") {
						t.Fatalf("default facade must expose uncertain remote write: %v", errorObject)
					}
				}
			})
		}
	}
}

func TestRCGroupAndThreadDerivation(t *testing.T) {
	explicit := TrustedContext{ChannelType: "5", ChannelID: "group____derived", ThreadID: "explicit"}
	if got := explicit.value(fieldThreadID); got != "explicit" {
		t.Fatalf("explicit thread binding must win: %q", got)
	}
	for _, c := range []struct{ kind, id, group, thread string }{
		{"1", "peer", "model-group", ""}, {"", "g____s", "model-group", ""}, {"2", "g", "g", ""}, {"5", "g____s", "g", "s"}, {"5", "malformed", "model-group", ""},
	} {
		t.Run(c.kind+"/"+c.id, func(t *testing.T) {
			tc := TrustedContext{ChannelType: c.kind, ChannelID: c.id}
			args := map[string]any{"group_no": "model-group"}
			tc.apply("group.get", args)
			if args["group_no"] != c.group {
				t.Errorf("DM/unknown type must not be used as a group path; got %v want %q", args["group_no"], c.group)
			}
			html := map[string]any{"thread_id": "model-thread"}
			tc.apply("html.publish", html)
			if c.thread != "" && html["thread_id"] != c.thread {
				t.Errorf("HTML must inherit the pinned thread: %v", html)
			}
			im := map[string]any{}
			tc.apply("drive.im-transfer.create", im)
			if im["im_group_no"] != c.id {
				t.Errorf("IM locator must keep the original channel id: %v", im)
			}
		})
	}
}

func TestRCMultipartShapesAndFalsePageAll(t *testing.T) {
	d := &registry.OperationDetail{OperationInfo: registry.OperationInfo{ID: "probe.upload", Service: "probe"}, Multipart: true, RequestBody: &registry.SchemaInfo{Properties: map[string]registry.SchemaInfo{"tags": {Type: "array"}, "name": {Type: "string"}}}}
	for _, a := range []map[string]any{{"tags": "a,b"}, {"name": []any{"a", "b"}}} {
		if _, err := buildArgv(d, a, execPolicy{}); err == nil {
			t.Errorf("multipart scalar/array mismatch must be refused: %v", a)
		}
	}
	if _, err := buildArgv(d, map[string]any{"tags": []any{"a,b", "c"}, "name": "valid", "page_all": false}, execPolicy{}); err != nil {
		t.Errorf("valid multipart array and explicit page_all=false must work: %v", err)
	}
}

func TestRCResourceCapabilityShape(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want bool
	}{{`null`, false}, {`[]`, false}, {`false`, false}, {`{}`, true}} {
		s := newTestServer(t)
		s.handleInitialize(json.RawMessage(`{"capabilities":{"resources":` + c.raw + `}}`))
		if s.clientResources != c.want {
			t.Errorf("resources %s: got %v want %v", c.raw, s.clientResources, c.want)
		}
	}
}

func TestRCMultipartSecretPathPlan(t *testing.T) {
	s := newTestServer(t)
	s.uploadRoot = t.TempDir()
	s.factoryFn = fakeBackendFactory("http://unused.invalid", "bf_test")
	d := &registry.OperationDetail{OperationInfo: registry.OperationInfo{ID: "file.upload", Method: "POST", Path: "/upload/{secret}"}, Multipart: true, Parameters: []registry.ParamInfo{{Name: "secret", In: "path", Required: true, Secret: true, Type: "string"}}}
	r := s.multipartDryRun(d, map[string]any{"secret": "secret-value-never-print", "file_path": "fixture.txt"}, "test")
	if r.IsError || strings.Contains(r.Content[0].Text, "secret-value-never-print") {
		t.Fatalf("path secret must be masked in the metadata plan: %s", r.Content[0].Text)
	}
}

func TestRCScopeSpellings(t *testing.T) {
	for _, c := range []struct {
		name  string
		scope bool
	}{{"targetChannelId", true}, {"source_group_no", true}, {"workspace-id", true}, {"principalSpaceId", true}, {"docId", false}, {"body", false}} {
		if isScopeField(c.name) != c.scope {
			t.Errorf("scope spelling %q: want %v", c.name, c.scope)
		}
	}
}

func TestRCPathParametersDocumented(t *testing.T) {
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		d, _ := reg.GetOperation(op.ID)
		for _, name := range extractPathParams(d.Path) {
			found := false
			for _, p := range d.Parameters {
				if p.Name == name && p.In == "path" && p.Required {
					found = true
				}
			}
			if !found {
				t.Errorf("operation %s missing required path parameter %s", op.ID, name)
			}
		}
	}
}

func TestRCFingerprintIncludesRiskPolicy(t *testing.T) {
	reg := registry.MustNew()
	d, _ := reg.GetOperation("docs.search")
	before := schemaFingerprint(d)
	d.Risk = "write"
	if schemaFingerprint(d) == before {
		t.Fatal("changing declared read/write policy must invalidate the execution fingerprint")
	}
}
