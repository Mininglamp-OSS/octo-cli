package permission

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSelfPolicyAcquisitionCompleteNoCacheNoRedirect(t *testing.T) {
	p := fixture(t)
	var raw []byte
	var reads atomic.Int32
	var status atomic.Int32
	status.Store(200)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.URL.Path != "/v1/bot/permission-bundle/v1" || r.URL.Query().Get("space_id") != "space" || r.Header.Get("X-Space-ID") != "space" || r.Header.Get("Authorization") != "Bearer bf_synthetic" || r.Header.Get("If-None-Match") != "" {
			t.Error("self request mismatch")
		}
		if status.Load() != 200 {
			w.Header().Set("Location", "/should-not-follow")
			w.WriteHeader(int(status.Load()))
			return
		}
		w.Write(raw)
	}))
	defer srv.Close()
	p.Key.Origin = srv.URL
	raw = encode(t, p)
	conf := srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conf.InsecureSkipVerify = false
	r, err := NewSelfPolicyReader(p.Key.Origin, p.Key.BotUID, "bf_synthetic", conf)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for n := 0; n < 2; n++ {
		if _, err = r.ReadBundle(ctx, p.Key); err != nil {
			t.Fatal(err)
		}
	}
	if reads.Load() != 2 {
		t.Fatal("policy was cached")
	}
	for _, s := range []int32{302, 304, 400, 503} {
		status.Store(s)
		if _, err = r.ReadBundle(ctx, p.Key); err == nil {
			t.Fatal("accepted", s)
		}
	}
	if reads.Load() != 6 {
		t.Fatal("redirect/retry", reads.Load())
	}
	status.Store(200)
	raw = []byte(`{"error":{"code":"err.server.permission.bundle_unavailable"}}`)
	if _, err = r.ReadBundle(ctx, p.Key); err == nil {
		t.Fatal("semantic error accepted")
	}
	raw = []byte(strings.Repeat(" ", MaxBundleBytes+1))
	if _, err = r.ReadBundle(ctx, p.Key); err == nil {
		t.Fatal("unbounded policy")
	}
	key := p.Key
	key.BotUID = "other"
	before := reads.Load()
	if _, err = r.ReadBundle(ctx, key); err == nil || reads.Load() != before {
		t.Fatal("alternate Bot dispatched")
	}
}
