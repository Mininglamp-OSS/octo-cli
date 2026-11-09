package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/permission"
)

func TestPrivatePackageReaderExactPinAndPerFileEligibility(t *testing.T) {
	files, pin, baseline := retainedPackageFixture(t)
	var requests atomic.Int32
	var failAfter atomic.Int32
	var compressed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		q := r.URL.Query()
		expectedFields := 4
		if strings.HasSuffix(r.URL.Path, "/file") {
			expectedFields++
		}
		if len(q) != expectedFields || q.Get("space_id") != "space" || q.Get("format") != permission.CatalogPackageFormat || q.Get("catalog_version") != pin.Version || q.Get("catalog_digest") != pin.Digest || r.Header.Get("Authorization") != "Bearer bf_catalog" {
			t.Error("private package request escaped frozen scope/pin")
			http.Error(w, "scope", http.StatusForbidden)
			return
		}
		for _, values := range q {
			if len(values) != 1 {
				t.Error("duplicate private selector")
			}
		}
		if at := failAfter.Load(); at > 0 && n > at {
			http.Error(w, "current eligibility removed", http.StatusForbidden)
			return
		}
		path := q.Get("path")
		if r.URL.Path == "/v1/bot/permission-bundle/v1/catalog" {
			path = "manifest.json"
		} else if r.URL.Path != "/v1/bot/permission-bundle/v1/catalog/file" {
			t.Error("wrong private catalog path")
			http.NotFound(w, r)
			return
		}
		raw, ok := files[path]
		if !ok {
			t.Error("unlisted private path")
			http.NotFound(w, r)
			return
		}
		media := "application/json"
		if strings.HasPrefix(path, "blobs/") {
			media = "application/octet-stream"
		}
		w.Header().Set("Content-Type", media)
		if compressed.Load() {
			w.Header().Set("Content-Encoding", "gzip")
		}
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	conf := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	conf.InsecureSkipVerify = false
	reader, err := permission.NewSelfPolicyReader(server.URL, "bot", "bf_catalog", conf, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	key := permission.Key{Origin: server.URL, BotUID: "bot", SpaceID: "space"}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	snapshot, err := reader.ReadCatalogPackage(ctx, key, pin, baseline, PermissionCatalogSlotValues)
	if err != nil || snapshot.Pin() != pin || requests.Load() != int32(len(files)) {
		t.Fatal("complete private package acquisition failed", err, requests.Load())
	}
	requests.Store(0)
	failAfter.Store(1)
	if partial, err := reader.ReadCatalogPackage(ctx, key, pin, baseline, PermissionCatalogSlotValues); err == nil || partial != nil || requests.Load() != 2 {
		t.Fatal("manifest read became an eligibility lease or partial catalog", err, requests.Load())
	}
	requests.Store(0)
	failAfter.Store(0)
	compressed.Store(true)
	if _, err := reader.ReadCatalogPackage(ctx, key, pin, baseline, PermissionCatalogSlotValues); err == nil || requests.Load() != 1 {
		t.Fatal("compressed private catalog accepted", err)
	}
	wrong := key
	wrong.BotUID = "other"
	before := requests.Load()
	if _, err := reader.ReadCatalogPackage(ctx, wrong, pin, baseline, PermissionCatalogSlotValues); err == nil || requests.Load() != before {
		t.Fatal("wrong Bot used package authority or ordinary fallback")
	}
}
