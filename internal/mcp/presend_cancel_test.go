package mcp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/spf13/cobra"
)

func TestXIN2549MCPAfterValidationCancellation(t *testing.T) {
	var hits atomic.Int32
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) }))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, tool := range []string{"execute", "call_op"} {
			t.Run(transport+"/"+tool, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				// The builder runs after buildArgv's cancellation check and before CLI execution.
				build := func(f *cmdutil.Factory) *cobra.Command { root := testRoot(f); cancel(); return root }
				wire := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + xin2549Params(tool, false) + `}`
				var out bytes.Buffer
				before := hits.Load()
				if transport == "http" {
					h, err := testHTTPHandler(t, build, TrustedContext{}, cmdutil.GlobalOptions{NoRetry: true}, facadeBoth)
					if err != nil {
						t.Fatal(err)
					}
					r := httptest.NewRequest("POST", "/", strings.NewReader(wire)).WithContext(ctx)
					r.Header.Set("Authorization", "Bearer bf_fixture")
					w := httptest.NewRecorder()
					h.ServeHTTP(w, r)
					out.Write(w.Body.Bytes())
				} else {
					s, err := NewServer(build)
					if err != nil {
						t.Fatal(err)
					}
					s.WithFacade(facadeBoth).WithAdmission(testAdmission(t))
					s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
					if err := s.ServeStdio(ctx, strings.NewReader(wire+"\n"), &out); err != nil {
						t.Fatal(err)
					}
				}
				unknown := strings.Contains(out.String(), "RESULT_UNKNOWN")
				t.Logf("deliveries=%d result_unknown=%t", hits.Load()-before, unknown)
				if hits.Load() != before || unknown {
					t.Error("cancellation after argument validation but before send must remain proven-unsent")
				}
			})
		}
	}
}

func TestXIN2549MCPPreSendCancellation(t *testing.T) {
	var hits atomic.Int32
	backend := policyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.Write([]byte(`{}`)) }))
	defer backend.Close()
	t.Setenv("OCTO_API_BASE_URL", backend.URL)
	for _, transport := range []string{"http", "stdio"} {
		for _, tool := range []string{"execute", "call_op"} {
			for _, expired := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/expired=%t", transport, tool, expired), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					cancel()
					if expired {
						var stop context.CancelFunc
						ctx, stop = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
						defer stop()
					}
					wire := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + xin2549Params(tool, false) + `}`
					before := hits.Load()
					var out bytes.Buffer
					if transport == "http" {
						h, err := testHTTPHandler(t, testRoot, TrustedContext{}, cmdutil.GlobalOptions{NoRetry: true}, facadeBoth)
						if err != nil {
							t.Fatal(err)
						}
						r := httptest.NewRequest("POST", "/", strings.NewReader(wire)).WithContext(ctx)
						r.Header.Set("Authorization", "Bearer bf_fixture")
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						out.Write(w.Body.Bytes())
					} else {
						s := newTestServer(t).WithFacade(facadeBoth)
						s.factoryFn = fakeBackendFactory(backend.URL, "bf_fixture")
						if err := s.ServeStdio(ctx, strings.NewReader(wire+"\n"), &out); err != nil {
							t.Fatal(err)
						}
					}
					unknown := strings.Contains(out.String(), "RESULT_UNKNOWN")
					t.Logf("deliveries=%d result_unknown=%t", hits.Load()-before, unknown)
					if hits.Load() != before || unknown {
						t.Error("unsent cancelled MCP invocation must not report unknown mutation")
					}
				})
			}
		}
	}
}
