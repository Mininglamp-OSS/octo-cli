package permission

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeFixture struct {
	mu sync.Mutex
	p  Bundle
	c  *Coordinator
	i  *Invocation
}

func newRuntimeFixture(t *testing.T, capabilities ...string) *runtimeFixture {
	t.Helper()
	f := &runtimeFixture{p: fixture(t)}
	f.p.Invocation = Stage{"U", []Rule{}}
	f.p.Capability = Stage{"U", []Rule{}}
	reader := policyFunc(func(context.Context, Key) ([]byte, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		return encode(t, f.p), nil
	})
	var err error
	f.c, err = NewCoordinator(Catalog{f.p.CatalogVersion, f.p.CatalogDigest, capabilities}, reader, factFunc(allFacts))
	if err != nil {
		t.Fatal(err)
	}
	f.i, err = f.c.Register(Binding{f.p.Key, f.p.Incarnation, f.p.ContextEpoch, "generation", Subject{Caller: "alice", Kind: "group", Source: "group"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.i.Close)
	return f
}

func (f *runtimeFixture) deny() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.p.Revision = "1"
	f.p.Capability = Stage{"E", []Rule{}}
}

func TestRuntimeSourceTargetsPagesReadAndPublication(t *testing.T) {
	f := newRuntimeFixture(t, testCap)
	if err := f.c.ConfigurePageBounds(map[string]PageBound{testCap: {Items: 2, Pages: 2}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := f.c.BeginCall(ctx, f.i, testCap, []byte(`{}`), nil, RequestTarget{testCap, "POST", "/messages/{id}"})
	if err != nil {
		t.Fatal(err)
	}
	call := BoundCall(ctx)
	defer call.Close()
	if _, err := RequestContext(ctx, "message", "message.send", "POST", "/messages/42"); err != nil {
		t.Fatal(err)
	}
	if err := AdmitPage(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if err := AdmitPage(ctx, 1); err != nil {
		t.Fatal(err)
	}
	input := t.TempDir() + "/input"
	if err := os.WriteFile(input, []byte("synthetic input"), 0o600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	open := func() (*os.File, error) { opens++; return os.Open(input) }
	spool, cleanup, err := call.ReadLocal(ctx, open)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(spool)
	cleanup()
	if err != nil || string(b) != "synthetic input" {
		t.Fatal(err, string(b))
	}
	var out bytes.Buffer
	if err := call.Publish(ctx, &out, []byte("positive")); err != nil || out.String() != "positive" {
		t.Fatal(err, out.String())
	}
	f.deny()
	_, _, err = call.ReadLocal(ctx, open)
	category(t, err, "capability_denied")
	out.Reset()
	category(t, call.Publish(ctx, &out, []byte("private")), "capability_denied")
	if opens != 1 || out.Len() != 0 {
		t.Fatal("refused boundary leaked", opens, out.String())
	}
}

func TestRuntimePageOverflowChildAndLeaseDoNotBecomeUnlimited(t *testing.T) {
	f := newRuntimeFixture(t, testCap)
	if err := f.c.ConfigurePageBounds(map[string]PageBound{testCap: {2, 1}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := f.c.BeginCall(ctx, f.i, testCap, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer BoundCall(ctx).Close()
	if err := AdmitPage(ctx, 2); err != nil {
		t.Fatal(err)
	}
	category(t, AdmitPage(ctx, 1), "policy_unavailable")
	other := newRuntimeFixture(t, testCap)
	childCtx, err := other.c.BeginCall(ctx, other.i, testCap, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer BoundCall(childCtx).Close()
	_, err = Child(childCtx, "octo-cli/docs/op/docs.scene.get")
	category(t, err, "capability_denied")
	lease, hard := other.i.Deadlines()
	if err := other.c.Renew(ctx, other.i, "another-generation"); err == nil {
		t.Fatal("renew retargeted")
	}
	if err := other.c.Renew(ctx, other.i, "generation"); err != nil {
		t.Fatal(err)
	}
	newLease, newHard := other.i.Deadlines()
	if newLease.Before(lease) || newHard != hard {
		t.Fatal("lifetime changed")
	}
	other.i.mu.Lock()
	other.i.expires = time.Now().Add(-time.Second)
	other.i.mu.Unlock()
	category(t, other.c.Admit(ctx, other.i), "context_untrusted")
}

func TestOwnedTransferEveryHopAndRefusedNextHop(t *testing.T) {
	const capability = "octo-cli/docs/derived/portable-attachment-bytes"
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			f := newRuntimeFixture(t, capability)
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
				if n < 9 {
					if revoke {
						f.deny()
					}
					http.Redirect(w, r, fmt.Sprint("/", n+1), http.StatusFound)
					return
				}
				fmt.Fprint(w, "synthetic")
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx, err := f.c.BeginCall(ctx, f.i, capability, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer BoundCall(ctx).Close()
			base := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return nil, nil }, DialContext: (&net.Dialer{}).DialContext}, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) > 9 {
					return http.ErrUseLastResponse
				}
				return nil
			}}
			owned, err := OwnedTransferClient(ctx, base)
			if err != nil {
				t.Fatal(err)
			}
			req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/0", http.NoBody)
			response, err := owned.Do(req)
			if revoke {
				category(t, TransferFailure(err), "capability_denied")
				if hits.Load() != 1 {
					t.Fatal("unchecked redirect", hits.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if hits.Load() != 10 || string(data) != "synthetic" {
				t.Fatal(hits.Load(), string(data))
			}
		})
	}
}

func TestOwnedRedisFirstWritePositiveAndRevokedBeforeEval(t *testing.T) {
	const capability = "octo-cli/bot/secret/get_secret_download_url"
	f := newRuntimeFixture(t, capability)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var evals atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			func() {
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(3 * time.Second))
				reader := bufio.NewReader(conn)
				line, e := reader.ReadString('\n')
				if e != nil {
					return
				}
				count, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "*")))
				var parts []string
				for j := 0; j < count; j++ {
					length, e := reader.ReadString('\n')
					if e != nil {
						return
					}
					n, _ := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(length, "$")))
					b := make([]byte, n+2)
					if _, e = io.ReadFull(reader, b); e != nil {
						return
					}
					parts = append(parts, string(b[:n]))
				}
				if len(parts) > 0 && parts[0] == "EVAL" {
					evals.Add(1)
					fmt.Fprint(conn, ":1\r\n")
				}
			}()
		}
	}()
	defer func() { listener.Close(); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err = f.c.BeginCall(ctx, f.i, capability, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer BoundCall(ctx).Close()
	call := BoundCall(ctx)
	endpoint := RedisEndpoint{Address: listener.Addr().String()}
	n, err := call.IssueSecret(ctx, endpoint, "return 1", "test:ticket", "synthetic", 1)
	if err != nil || n != 1 || evals.Load() != 1 {
		t.Fatal(n, err, evals.Load())
	}
	f.deny()
	_, err = call.IssueSecret(ctx, endpoint, "return 1", "test:ticket", "synthetic", 1)
	category(t, err, "capability_denied")
	if evals.Load() != 1 {
		t.Fatal("refused Redis EVAL reached receiver")
	}
}

func TestOwnedHTTPProxyRetainsProxyAuthWithoutBotCredentials(t *testing.T) {
	const capability = "octo-cli/docs/derived/portable-attachment-bytes"
	f := newRuntimeFixture(t, capability)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:password")) || r.Header.Get("Authorization") != "" || !strings.HasPrefix(r.RequestURI, "http://download.example/") {
			t.Error("proxy credential/framing contract changed")
		}
		fmt.Fprint(w, "synthetic")
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	u.User = url.UserPassword("fixture", "password")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx, err := f.c.BeginCall(ctx, f.i, capability, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer BoundCall(ctx).Close()
	base := &http.Client{Transport: &http.Transport{Proxy: func(*http.Request) (*url.URL, error) { return u, nil }, DialContext: (&net.Dialer{}).DialContext}}
	owned, err := OwnedTransferClient(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://download.example/asset", http.NoBody)
	response, err := owned.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(response.Body)
	if string(data) != "synthetic" {
		t.Fatal(string(data))
	}
}
