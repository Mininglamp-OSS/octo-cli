package mcp_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// XIN-2463 against the real binary, with an isolated mock backend that reads
// every request body in full and counts each write it receives:
//   - B1: a caller-supplied Idempotency-Key is not proof that the backend
//     deduplicates a resend (octo-docs-backend honours it for html_ppt only),
//     so docs.create / docs.comments.add are delivered once on drop, 502, 503
//     and 504 and reported as an unknown result, from the CLI, execute and
//     call_op. capability.install, whose Fleet receipt deduplicates every
//     resend, keeps its retries and still ends as an unknown result.
//   - B2: net/http does not resend a keyed write on a reused keep-alive
//     connection: a board comment (scene GET, then POST on the same
//     connection) reaches the backend once, with and without --no-retry.
//   - Whole-history uncertainty: once an attempt may have applied the write,
//     later refused dials and a replay into 404 still end as an unknown
//     result; only all-refused dials are a definite failure.

const xin2463Scene = `{"elements":[{"id":"e1","type":"rectangle","x":0,"y":0,"width":10,"height":10}],"files":{},"appState":{},"baseVersion":"BV"}`

type xin2463Backend struct {
	srv    *httptest.Server
	mu     sync.Mutex
	writes int
	keys   []string
	mode   string
}

// newXIN2463Backend answers GET with a board scene and every other method as
// mode says: "drop" reads the request and closes the connection, a status code
// is answered as is, "503-close" answers the first write with 503 and then
// stops listening, and "drop-404" drops the first write and answers 404 after.
func newXIN2463Backend(t *testing.T) *xin2463Backend {
	t.Helper()
	b := &xin2463Backend{}
	b.srv = binaryPolicyFixtureServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(xin2463Scene))
			return
		}
		b.mu.Lock()
		b.writes++
		n, mode := b.writes, b.mode
		b.keys = append(b.keys, r.Header.Get("Idempotency-Key"))
		b.mu.Unlock()
		drop := func() {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		switch {
		case mode == "drop":
			drop()
		case mode == "503-close":
			_ = b.srv.Listener.Close()
			w.Header().Set("Connection", "close")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"UNAVAILABLE","message":"mock"}}`))
		case mode == "drop-404" && n == 1:
			drop()
		case mode == "drop-404":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"mock"}}`))
		case mode == "502" || mode == "503" || mode == "504":
			status := map[string]int{"502": http.StatusBadGateway, "503": http.StatusServiceUnavailable, "504": http.StatusGatewayTimeout}[mode]
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"mock","message":"mock"}}`))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(b.srv.Close)
	return b
}

func (b *xin2463Backend) reset(mode string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.writes, b.keys, b.mode = 0, nil, mode
}

func (b *xin2463Backend) count() (writes int, keys []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.writes, append([]string(nil), b.keys...)
}

// The 503-close mode stops the listener, so each such case gets its own
// backend.
func (b *xin2463Backend) url() string { return b.srv.URL }

func xin2463CLI(t *testing.T, bin, backendURL string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(isolatedEnv(t), "OCTO_API_BASE_URL="+backendURL)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("%v: want a failure: %s", args, out)
	}
	return string(out)
}

func TestBuiltBinary_XIN2463KeyedWritesAndAttemptHistory(t *testing.T) {
	bin := buildOctoCLI(t)
	backend := newXIN2463Backend(t)

	// B1 over the CLI: the key is forwarded but does not buy any resend.
	cliKeyed := map[string][]string{
		"comments add":      {"docs", "comments", "add", "d1", "--body", "hi", "--anchorText", "hi", "--idempotency-key", "k-123"},
		"create doc":        {"docs", "create", "--data", `{"title":"t","docType":"doc"}`, "--idempotency-key", "k-9"},
		"create html_ppt":   {"docs", "create", "--data", `{"title":"t","docType":"html_ppt","templateId":"blank"}`, "--idempotency-key", "k-10"},
		"board comment":     {"docs", "comments", "add", "d1", "--body", "hi", "--element-id", "e1", "--idempotency-key", "k-7"},
		"board no-retry":    {"--no-retry", "docs", "comments", "add", "d1", "--body", "hi", "--element-id", "e1", "--idempotency-key", "k-7"},
		"comments no-retry": {"--no-retry", "docs", "comments", "add", "d1", "--body", "hi", "--anchorText", "hi", "--idempotency-key", "k-123"},
	}
	for name, args := range cliKeyed {
		for _, mode := range []string{"drop", "502", "503", "504"} {
			t.Run("cli/B1/"+name+"/"+mode, func(t *testing.T) {
				backend.reset(mode)
				out := xin2463CLI(t, bin, backend.url(), args...)
				n, keys := backend.count()
				if n != 1 {
					t.Errorf("backend writes = %d, want exactly 1: %s", n, out)
				}
				if len(keys) > 0 && keys[0] == "" {
					t.Errorf("the Idempotency-Key header did not reach the backend")
				}
				if !strings.Contains(out, `"code": "RESULT_UNKNOWN"`) {
					t.Errorf("want RESULT_UNKNOWN: %s", out)
				}
			})
		}
	}

	// B1 over both MCP facades and both transports.
	for _, transport := range []string{"stdio", "http"} {
		for _, op := range []struct{ id, args string }{
			{"docs.comments.add", `{"docId":"d1","body":"hi","anchorText":"hi","Idempotency-Key":"k-1"}`},
			{"docs.create", `{"title":"t","docType":"doc","Idempotency-Key":"k-2"}`},
		} {
			for _, mode := range []string{"drop", "502", "503", "504"} {
				for _, c := range []struct{ facade, tool, want string }{
					{"two", "execute", `"status": "result_unknown"`},
					{"three", "call_op", `"code": "RESULT_UNKNOWN"`},
				} {
					t.Run(transport+"/B1/"+c.tool+"/"+op.id+"/"+mode, func(t *testing.T) {
						backend.reset(mode)
						line := xin2453Serve(t, bin, transport, c.facade, backend.url(), xin2453Request(c.tool, `{"operation_id":"`+op.id+`","arguments":`+op.args+`}`))
						text := xin2453Text(t, line)
						if !strings.Contains(text, c.want) {
							t.Errorf("missing %q: %.600s", c.want, text)
						}
						if strings.Contains(text, `"retryable": true`) {
							t.Errorf("an unknown write outcome must not be retryable: %.600s", text)
						}
						if n, _ := backend.count(); n != 1 {
							t.Errorf("backend writes = %d, want exactly 1", n)
						}
					})
				}
			}
		}
	}

	// capability.install has a verified deduplication contract: it keeps its
	// retries (one try plus three, same key every time) and an exhausted
	// ambiguous run is still RESULT_UNKNOWN.
	capability := []string{"loop", "capability", "install", "--workspace-id", "11111111-1111-4111-8111-111111111111", "--idempotency-key", "cap-1",
		"--data", `{"definition":{"schema_version":"1.0","name":"c","skills":[{"name":"s","content":"x"}]}}`}
	t.Run("cli/replay-safe exhaustion", func(t *testing.T) {
		backend.reset("503")
		out := xin2463CLI(t, bin, backend.url(), capability...)
		n, keys := backend.count()
		if n != 4 {
			t.Errorf("backend writes = %d, want 4: %s", n, out)
		}
		for _, k := range keys {
			if k != "cap-1" {
				t.Errorf("keys across attempts = %v, want cap-1 every time", keys)
				break
			}
		}
		if !strings.Contains(out, `"code": "RESULT_UNKNOWN"`) {
			t.Errorf("want RESULT_UNKNOWN: %s", out)
		}
	})

	// Whole history: the first write answered 503 and the backend then stopped
	// listening, so the later attempts are refused dials.
	t.Run("cli/history/503 then refused dials", func(t *testing.T) {
		closing := newXIN2463Backend(t)
		closing.reset("503-close")
		out := xin2463CLI(t, bin, closing.url(), capability...)
		if n, _ := closing.count(); n != 1 {
			t.Errorf("backend writes = %d, want 1: %s", n, out)
		}
		if !strings.Contains(out, `"code": "RESULT_UNKNOWN"`) {
			t.Errorf("want RESULT_UNKNOWN: %s", out)
		}
	})
	t.Run("cli/history/all dials refused", func(t *testing.T) {
		out := xin2463CLI(t, bin, "http://"+xin2463ClosedAddr(t), capability...)
		if !strings.Contains(out, `"code": "NETWORK_ERROR"`) || strings.Contains(out, "RESULT_UNKNOWN") {
			t.Errorf("want a definite NETWORK_ERROR: %s", out)
		}
	})
	t.Run("cli/history/DELETE replay into 404", func(t *testing.T) {
		backend.reset("drop-404")
		out := xin2463CLI(t, bin, backend.url(), "docs", "delete", "d1")
		if n, _ := backend.count(); n != 2 {
			t.Errorf("backend writes = %d, want 2: %s", n, out)
		}
		if !strings.Contains(out, `"code": "RESULT_UNKNOWN"`) {
			t.Errorf("want RESULT_UNKNOWN: %s", out)
		}
	})

	// The same history over MCP, with an idempotent DELETE that is resent.
	for _, transport := range []string{"stdio", "http"} {
		for _, c := range []struct{ facade, tool, unknown, definite string }{
			{"two", "execute", `"status": "result_unknown"`, `"status": "execution_error"`},
			{"three", "call_op", `"code": "RESULT_UNKNOWN"`, `"code": "NETWORK_ERROR"`},
		} {
			call := xin2453Request(c.tool, `{"operation_id":"docs.delete","arguments":{"docId":"d1"}}`)
			t.Run(transport+"/history/"+c.tool+"/503 then refused dials", func(t *testing.T) {
				closing := newXIN2463Backend(t)
				closing.reset("503-close")
				text := xin2453Text(t, xin2453Serve(t, bin, transport, c.facade, closing.url(), call))
				if !strings.Contains(text, c.unknown) {
					t.Errorf("missing %q: %.600s", c.unknown, text)
				}
				if n, _ := closing.count(); n != 1 {
					t.Errorf("backend writes = %d, want 1", n)
				}
			})
			t.Run(transport+"/history/"+c.tool+"/replay into 404", func(t *testing.T) {
				backend.reset("drop-404")
				text := xin2453Text(t, xin2453Serve(t, bin, transport, c.facade, backend.url(), call))
				if !strings.Contains(text, c.unknown) {
					t.Errorf("missing %q: %.600s", c.unknown, text)
				}
			})
			t.Run(transport+"/history/"+c.tool+"/all dials refused", func(t *testing.T) {
				text := xin2453Serve(t, bin, transport, c.facade, "http://"+xin2463ClosedAddr(t), call)
				if !strings.Contains(text, "BOT_VERIFICATION_UNAVAILABLE") || strings.Contains(text, "RESULT_UNKNOWN") || strings.Contains(text, "result_unknown") {
					t.Errorf("unreachable identity service must stop admission before business: %.600s", text)
				}
			})
		}
	}
}

// xin2463ClosedAddr returns a loopback address nothing listens on.
func xin2463ClosedAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}
