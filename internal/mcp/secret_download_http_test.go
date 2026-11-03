package mcp

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
)

func TestSecretDownloadHTTPUnavailable(t *testing.T) {
	for _, scenario := range []string{"missing Redis", "missing origin", "global dry run", "configured malformed request"} {
		t.Run(scenario, func(t *testing.T) {
			// An isolated TCP trap proves that neither constructor nor route
			// dispatch connects to Redis. No Redis process or backend is needed.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			var redisConnections atomic.Int32
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					redisConnections.Add(1)
					_ = conn.Close()
				}
			}()
			t.Cleanup(func() { _ = listener.Close(); <-done })
			t.Setenv("OCTO_MCP_SECRET_REDIS_URL", "redis://"+listener.Addr().String())
			t.Setenv("OCTO_MCP_SECRET_PUBLIC_ORIGIN", "https://downloads.example")
			t.Setenv("OCTO_MCP_SECRET_TTL", "")
			t.Setenv("OCTO_MCP_SECRET_REDIS_PREFIX", "")
			if scenario == "missing Redis" {
				t.Setenv("OCTO_MCP_SECRET_REDIS_URL", "")
			}
			if scenario == "missing origin" {
				t.Setenv("OCTO_MCP_SECRET_PUBLIC_ORIGIN", "")
			}
			handler, err := testHTTPHandler(t, testRoot, TrustedContext{}, cmdutil.GlobalOptions{DryRun: scenario == "global dry run"})
			if scenario == "global dry run" {
				if err == nil || err.Error() != "MCP_OFFLINE_POLICY_UNAVAILABLE" {
					t.Fatal("serve dry-run must fail locally")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(handler.Close)
			configured := scenario == "configured malformed request"
			if (handler.secretDownloads != nil) != configured {
				t.Fatal("unexpected optional service state")
			}
			var recoveryLog bytes.Buffer
			server := httptest.NewUnstartedServer(handler)
			server.Config.ErrorLog = log.New(&recoveryLog, "", 0)
			server.Start()
			t.Cleanup(server.Close)
			client := server.Client()
			client.Timeout = 3 * time.Second
			paths := []string{"short", "", "bad/token", strings.Repeat("a", 43) + "?query=refused"}
			if !configured {
				paths = append(paths, strings.Repeat("a", 43))
			}
			for _, path := range paths {
				resp, getErr := client.Get(server.URL + secretDownloadPath + path)
				if getErr != nil {
					t.Fatalf("GET %q: %v", path, getErr)
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil || resp.StatusCode != http.StatusNotFound || string(body) != "download unavailable\n" {
					t.Fatalf("GET %q: status=%d body=%q err=%v", path, resp.StatusCode, body, readErr)
				}
				if resp.Header.Get("Cache-Control") != "no-store" {
					t.Fatal("unavailable download must not be cached")
				}
			}
			server.Close()
			if recoveryLog.Len() != 0 || redisConnections.Load() != 0 {
				t.Fatalf("recovery log=%q Redis connections=%d", recoveryLog.String(), redisConnections.Load())
			}
			t.Logf("GETs=%d status=404 recovery_log_bytes=0 Redis_connections=0", len(paths))
		})
	}
}
