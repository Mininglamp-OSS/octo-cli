package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func logFixtureRecord(t *testing.T) RequestRecord {
	t.Helper()
	id, e := randomUUID()
	if e != nil {
		t.Fatal(e)
	}
	return RequestRecord{ReceivedAt: time.Now().UTC().Format(time.RFC3339Nano), RequestID: id, BotUID: "bot_fixture", RPCID: RecordedRPCID{Type: "number", Lexeme: "9007199254740993"}, Tool: "search_ops", RequestedArguments: map[string]json.RawMessage{}}
}

func TestRequestLogLockRotateAndRestart(t *testing.T) {
	dir := secureTestDir(t)
	l, e := OpenRequestLog(dir)
	if e != nil {
		t.Fatal(e)
	}
	if other, e := OpenRequestLog(dir); e == nil {
		_ = other.Close()
		t.Fatal("shared directory lock accepted")
	}
	l.segmentLimit = 400
	for i := 0; i < 4; i++ {
		if e = l.AppendAndSync(context.Background(), logFixtureRecord(t)); e != nil {
			t.Fatal(e)
		}
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	segments := 0
	for _, f := range entries {
		if strings.HasSuffix(f.Name(), ".open") {
			t.Fatal("active segment remained after close")
		}
		if strings.HasSuffix(f.Name(), ".jsonl") {
			segments++
			st, e := f.Info()
			if e != nil || st.Mode().Perm() != 0o600 {
				t.Fatal("unsafe segment permissions")
			}
		}
	}
	if segments < 2 {
		t.Fatal("segment limit did not rotate")
	}
	l, e = OpenRequestLog(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.AppendAndSync(context.Background(), logFixtureRecord(t)); e != nil {
		t.Fatal(e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestRequestLogTailQuarantinedBeforeRecovery(t *testing.T) {
	dir := secureTestDir(t)
	l, e := OpenRequestLog(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.AppendAndSync(context.Background(), logFixtureRecord(t)); e != nil {
		t.Fatal(e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(dir)
	var name string
	for _, f := range entries {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			name = f.Name()
		}
	}
	active := strings.TrimSuffix(name, ".jsonl") + ".open"
	if e = os.Rename(filepath.Join(dir, name), filepath.Join(dir, active)); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(filepath.Join(dir, active), os.O_APPEND|os.O_WRONLY, 0o600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.WriteString(`{"incomplete":`)
	_ = f.Close()
	if e != nil {
		t.Fatal(e)
	}
	l, e = OpenRequestLog(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	entries, _ = os.ReadDir(dir)
	found := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "quarantine-") {
			found = true
			st, _ := entry.Info()
			if st.Mode().Perm() != 0o600 {
				t.Fatal("unsafe backup")
			}
		}
	}
	if !found {
		t.Fatal("tail was truncated without quarantine")
	}
}

func TestRequestLogCorruptionRetentionAndQuota(t *testing.T) {
	t.Run("closed corruption", func(t *testing.T) {
		dir := secureTestDir(t)
		id, _ := randomUUID()
		name := fmtLogName(time.Now(), id)
		if e := os.WriteFile(filepath.Join(dir, name), []byte("{\"status\":\"success\"}\n"), 0o600); e != nil {
			t.Fatal(e)
		}
		if l, e := OpenRequestLog(dir); e == nil {
			_ = l.Close()
			t.Fatal("unknown schema accepted")
		}
	})
	t.Run("retention", func(t *testing.T) {
		dir := secureTestDir(t)
		r := logFixtureRecord(t)
		b, _ := json.Marshal(r)
		b = append(b, '\n')
		id, _ := randomUUID()
		old := fmtLogName(time.Now().Add(-8*24*time.Hour), id)
		if e := os.WriteFile(filepath.Join(dir, old), b, 0o600); e != nil {
			t.Fatal(e)
		}
		l, e := OpenRequestLog(dir)
		if e != nil {
			t.Fatal(e)
		}
		if e = l.Close(); e != nil {
			t.Fatal(e)
		}
		if _, e = os.Stat(filepath.Join(dir, old)); !os.IsNotExist(e) {
			t.Fatal("expired closed segment retained")
		}
	})
	t.Run("quota", func(t *testing.T) {
		l, e := OpenRequestLog(secureTestDir(t))
		if e != nil {
			t.Fatal(e)
		}
		l.quota = 1
		if e = l.AppendAndSync(context.Background(), logFixtureRecord(t)); e == nil {
			t.Fatal("quota exceeded")
		}
		_ = l.Close()
	})
}
func fmtLogName(t time.Time, id string) string {
	return "request-" + fmt.Sprintf("%019d", t.UnixNano()) + "-" + id + ".jsonl"
}

func TestRequestLogBackpressureAndCloseCancelPending(t *testing.T) {
	l, e := OpenRequestLog(secureTestDir(t))
	if e != nil {
		t.Fatal(e)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	l.syncFile = func(f *os.File) error { once.Do(func() { close(started) }); <-release; return f.Sync() }
	first := make(chan error, 1)
	go func() { first <- l.AppendAndSync(context.Background(), logFixtureRecord(t)) }()
	<-started
	// Only the blocked writer owns an item. Fill the bounded queue with
	// encoded fixture records without spawning unbounded waiting callers.
	raw, _ := json.Marshal(logFixtureRecord(t))
	raw = append(raw, '\n')
	l.mu.Lock()
	for i := 0; i < cap(l.queue); i++ {
		l.queue <- logItem{payload: raw, ctx: context.Background(), ack: make(chan error, 1)}
		l.queuedBytes += len(raw)
	}
	l.mu.Unlock()
	start := time.Now()
	if e = l.AppendAndSync(context.Background(), logFixtureRecord(t)); e == nil || time.Since(start) > 100*time.Millisecond {
		t.Fatal("full queue did not refuse immediately")
	}
	closed := make(chan error, 1)
	go func() { closed <- l.Close() }()
	if e = <-first; e == nil {
		t.Fatal("shutdown released pending business")
	}
	close(release)
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
}

func TestAdmissionCapacityLimits(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < cap(s.admission.tools); i++ {
		s.admission.tools <- struct{}{}
	}
	expectAdmissionRPC(t, s, "search_ops", map[string]any{}, -32006)
	for len(s.admission.tools) > 0 {
		<-s.admission.tools
	}
	s.admission.mu.Lock()
	s.admission.tokens = 0
	s.admission.updated = time.Now()
	s.admission.mu.Unlock()
	expectAdmissionRPC(t, s, "search_ops", map[string]any{}, -32006)
	if len(allLogRecords(t, s.admission.log)) != 0 {
		t.Fatal("busy request recorded")
	}
}

func TestAdmissionFrozenCredentialNoRegister(t *testing.T) {
	var verifyCount, businessCount int
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/verify-bot" {
			verifyCount++
			io.WriteString(w, `{"bot_uid":"bot_fixture","mcp_policy":`+compatiblePolicy+`}`)
			return
		}
		if r.URL.Path == "/v1/bot/register" {
			t.Error("registration is forbidden during admission")
		}
		businessCount++
		if r.Header.Get("Authorization") != "Bearer bf_original" {
			t.Error("credential was re-resolved")
		}
		io.WriteString(w, `{}`)
	}))
	defer be.Close()
	s := newTestServer(t)
	base := fakeBackendFactory(be.URL, "bf_original")
	reads := 0
	s.factoryFn = func(tc TrustedContext) (*cmdutil.Factory, *bytes.Buffer, *bytes.Buffer) {
		f, o, e := base(tc)
		provider := f.CredentialFunc
		f.CredentialFunc = func() (*credential.BotCredential, error) {
			c, e := provider()
			reads++
			if reads > 1 {
				c.Token = "bf_changed"
			}
			return c, e
		}
		return f, o, e
	}
	res := callTool(t, s, "call_op", map[string]any{"operation_id": "message.send", "arguments": map[string]any{"channel_id": "target", "channel_type": 2, "payload": map[string]any{"content": "not logged"}}})
	if res.IsError || verifyCount != 1 || businessCount != 1 || reads != 1 {
		t.Fatalf("verification=%d business=%d provider_reads=%d", verifyCount, businessCount, reads)
	}
}
