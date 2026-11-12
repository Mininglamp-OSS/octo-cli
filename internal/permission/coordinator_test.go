package permission

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testCap = "octo-cli/message/op/message.send"

type policyFunc func(context.Context, Key) ([]byte, error)

func (f policyFunc) ReadBundle(c context.Context, k Key) ([]byte, error) { return f(c, k) }

type factFunc func(context.Context, Binding, Needs) (Facts, error)

func (f factFunc) Recheck(c context.Context, b Binding, n Needs) (Facts, error) { //nolint:gocritic // test fixtures are intentionally detached immutable snapshots
	return f(c, b, n)
}
func allFacts(_ context.Context, b Binding, _ Needs) (Facts, error) { //nolint:gocritic // detached native fixture binding matches the private adapter interface
	return Facts{b.Subject, true, true, true, nil}, nil
}
func encode(t *testing.T, p Bundle) []byte { //nolint:gocritic // test fixtures are intentionally detached immutable snapshots
	t.Helper()
	_, b, e := Seal(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func testCoordinator(t *testing.T, p Bundle, read PolicyReader, f FactReader) (*Coordinator, *Invocation) { //nolint:gocritic // test fixtures are intentionally detached immutable snapshots
	t.Helper()
	c, e := NewCoordinator(Catalog{p.CatalogVersion, p.CatalogDigest, []string{testCap}}, read, f)
	if e != nil {
		t.Fatal(e)
	}
	i, e := c.Register(Binding{p.Key, p.Incarnation, p.ContextEpoch, "generation-1", Subject{Caller: "alice", Kind: "group", Source: "group"}})
	if e != nil {
		t.Fatal(e)
	}
	return c, i
}
func category(t *testing.T, err error, want string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Category != want {
		t.Fatalf("got %v (%#v), want %s", err, r, want)
	}
	if len(r.Correlation) != 32 {
		t.Fatal("correlation")
	}
}

func TestIndependentStagePairsAndNewFactReadAfterActivation(t *testing.T) {
	for _, inv := range []string{"U", "E"} {
		for _, cap := range []string{"U", "E"} {
			t.Run(inv+cap, func(t *testing.T) {
				p := fixture(t)
				p.Invocation.State = inv
				p.Capability.State = cap
				raw := encode(t, p)
				var trace []string
				c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { trace = append(trace, "B"); return raw, nil }), factFunc(func(ctx context.Context, b Binding, n Needs) (Facts, error) {
					trace = append(trace, "F")
					return allFacts(ctx, b, n)
				}))
				sends := 0
				err := c.atBoundary(context.Background(), i, testCap, func() error { sends++; return nil })
				if inv == "E" {
					category(t, err, "invocation_denied")
				} else if cap == "E" {
					category(t, err, "capability_denied")
				} else if err != nil || sends != 1 {
					t.Fatal(err, sends)
				}
				want := "FB"
				if inv == "E" || cap == "E" {
					want = "FBFB"
				}
				if strings.Join(trace, "") != want {
					t.Fatal(trace)
				}
				if (inv == "E" || cap == "E") && sends != 0 {
					t.Fatal("denied dispatch")
				}
			})
		}
	}
}

func TestDenyAdvancesSharedWatermarkAndOldReplyCannotDispatch(t *testing.T) {
	p := fixture(t)
	old := encode(t, p)
	p.Revision = "1"
	p.Capability = Stage{"E", []Rule{}}
	newer := encode(t, p)
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	read := policyFunc(func(context.Context, Key) ([]byte, error) {
		if reads.Add(1) == 1 {
			close(entered)
			<-release
			return old, nil
		}
		return newer, nil
	})
	c, i := testCoordinator(t, p, read, factFunc(allFacts))
	out := make(chan error, 1)
	go func() {
		out <- c.atBoundary(context.Background(), i, testCap, func() error { t.Error("stale dispatch"); return nil })
	}()
	<-entered
	err := c.atBoundary(context.Background(), i, testCap, func() error { t.Error("denied dispatch"); return nil })
	category(t, err, "capability_denied")
	close(release)
	category(t, <-out, "policy_unavailable")
}

func TestEpochRestartCloseAndUnknownFacts(t *testing.T) {
	p := fixture(t)
	raw := encode(t, p)
	read := policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil })
	c, i := testCoordinator(t, p, read, factFunc(allFacts))
	other, _ := testCoordinator(t, p, read, factFunc(allFacts))
	category(t, other.atBoundary(context.Background(), i, testCap, func() error { t.Fatal("restart reused handle"); return nil }), "context_untrusted")
	i.Close()
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "context_untrusted")
	c, i = testCoordinator(t, p, read, factFunc(func(context.Context, Binding, Needs) (Facts, error) { return Facts{}, nil }))
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "policy_unavailable")
	c, i = testCoordinator(t, p, read, factFunc(allFacts))
	p.Revision = "1"
	p.ContextEpoch = "1"
	raw = encode(t, p)
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "context_untrusted")
}

func TestSharedRevisionEquivocationAndEpochRegression(t *testing.T) {
	p := fixture(t)
	raw := encode(t, p)
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil }), factFunc(allFacts))
	if e := c.atBoundary(context.Background(), i, testCap, func() error { return nil }); e != nil {
		t.Fatal(e)
	}
	p.Capability = Stage{"E", []Rule{}}
	raw = encode(t, p)
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "policy_unavailable")
	p.Revision = "1"
	p.ContextEpoch = "1"
	raw = encode(t, p)
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "context_untrusted")
	// Epoch mismatch permanently retires the old handle. A fresh handle still
	// shares the accepted scope watermark and must reject epoch regression.
	i, err := c.Register(i.binding)
	if err != nil {
		t.Fatal(err)
	}
	p.Revision = "2"
	p.ContextEpoch = "0"
	raw = encode(t, p)
	category(t, c.atBoundary(context.Background(), i, testCap, nil), "policy_unavailable")
}

func TestNativeRevocationWithoutPolicyRevisionAndUnknownCapability(t *testing.T) {
	p := fixture(t)
	p.Invocation = Stage{"E", []Rule{rule("admit", "allow", "alice", "group", "", false)}}
	p.Capability = Stage{"E", []Rule{rule("send", "allow", "alice", "group", testCap, false)}}
	raw := encode(t, p)
	eligible := true
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil }), factFunc(func(ctx context.Context, b Binding, n Needs) (Facts, error) {
		f, e := allFacts(ctx, b, n)
		f.CapabilityEligible = eligible
		return f, e
	}))
	sent := 0
	if err := c.atBoundary(context.Background(), i, testCap, func() error { sent++; return nil }); err != nil {
		t.Fatal(err)
	}
	eligible = false
	category(t, c.atBoundary(context.Background(), i, testCap, func() error { sent++; return nil }), "capability_denied")
	eligible = true
	category(t, c.atBoundary(context.Background(), i, "unknown", func() error { sent++; return nil }), "capability_denied")
	if sent != 1 {
		t.Fatal("native revocation or unknown capability dispatched", sent)
	}
}

func TestOwnedTLSAttemptPositiveRefusalAndResponseLoss(t *testing.T) {
	requireOwnedPlatform(t)
	var received atomic.Int32
	var lose atomic.Bool
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		b, _ := io.ReadAll(r.Body)
		if string(b) != "sealed" {
			t.Error("request body drift")
		}
		if lose.Load() {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()
	p := fixture(t)
	p.Key.Origin = backend.URL
	p.Invocation = Stage{"E", []Rule{rule("admit", "allow", "alice", "group", "", false)}}
	p.Capability = Stage{"E", []Rule{rule("send", "allow", "alice", "group", testCap, false)}}
	raw := encode(t, p)
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { return raw, nil }), factFunc(allFacts))
	tlsConf := backend.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tlsConf.InsecureSkipVerify = false
	prepare := func() *PreparedHTTP {
		a, e := PrepareHTTP(context.Background(), p.Key, "POST", "/synthetic", nil, []byte("sealed"), tlsConf, time.Now().Add(3*time.Second))
		if e != nil {
			t.Fatal(e)
		}
		return a
	}
	a := prepare()
	result, err := c.Execute(context.Background(), i, a, testCap)
	if err != nil || result.Status != "sent" {
		t.Fatal(err, result)
	}
	result.Response.Body.Close()
	if received.Load() != 1 {
		t.Fatal(received.Load())
	}
	_, err = c.Execute(context.Background(), i, a, testCap)
	category(t, err, "context_untrusted")
	a = prepare()
	p.Revision = "1"
	p.Capability = Stage{"E", []Rule{}}
	raw = encode(t, p)
	_, err = c.Execute(context.Background(), i, a, testCap)
	category(t, err, "capability_denied")
	if received.Load() != 1 {
		t.Fatal("refused request reached receiver")
	}
	p.Revision = "2"
	p.Capability = Stage{"U", []Rule{}}
	raw = encode(t, p)
	lose.Store(true)
	a = prepare()
	result, err = c.Execute(context.Background(), i, a, testCap)
	if !errors.Is(err, ErrAttemptUnknown) || result.Status != "result_unknown" || received.Load() != 2 {
		t.Fatal(err, result, received.Load())
	}
	// A subsequent refused retry does not erase the first attempt's uncertainty.
	p.Revision = "3"
	p.Capability = Stage{"E", []Rule{}}
	raw = encode(t, p)
	a = prepare()
	_, err = c.Execute(context.Background(), i, a, testCap)
	category(t, err, "capability_denied")
	if received.Load() != 2 {
		t.Fatal("implicit retry")
	}
}

func TestConcurrentBoundaryAndRevocationSerialize(t *testing.T) {
	p := fixture(t)
	old := encode(t, p)
	p.Revision = "1"
	p.Invocation = Stage{"E", []Rule{}}
	newer := encode(t, p)
	var mu sync.Mutex
	raw := old
	c, i := testCoordinator(t, p, policyFunc(func(context.Context, Key) ([]byte, error) { mu.Lock(); defer mu.Unlock(); return raw, nil }), factFunc(allFacts))
	start, release := make(chan struct{}), make(chan struct{})
	out := make(chan error, 2)
	go func() {
		out <- c.atBoundary(context.Background(), i, testCap, func() error { close(start); <-release; return nil })
	}()
	<-start
	mu.Lock()
	raw = newer
	mu.Unlock()
	go func() {
		out <- c.atBoundary(context.Background(), i, testCap, func() error { t.Error("revoked send"); return nil })
	}()
	close(release)
	a, b := <-out, <-out
	if a != nil && b != nil {
		t.Fatal(a, b)
	}
	if a == nil {
		category(t, b, "invocation_denied")
	} else {
		category(t, a, "invocation_denied")
	}
}
