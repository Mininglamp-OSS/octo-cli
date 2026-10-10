package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestRCHTTPMultipartConcurrencyBound(t *testing.T) {
	root := t.TempDir()
	markUploadRoot(t, root)
	if err := os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var hits atomic.Int32
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		<-release
		_, _ = w.Write([]byte(`{"url":"fixture"}`))
	}))
	defer be.Close()
	t.Setenv("OCTO_API_BASE_URL", be.URL)
	t.Setenv("OCTO_MCP_UPLOAD_ROOT", root)
	t.Setenv("OCTO_CONFIG_DIR", t.TempDir())
	h, err := NewHTTPHandler(testRoot, TrustedContext{}, cmdutil.GlobalOptions{}, facadeBoth)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tool := "call_op"
			if i%2 == 0 {
				tool = "execute"
			}
			req, _ := http.NewRequest("POST", srv.URL, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":{"operation_id":"file.upload","arguments":{"file_path":"fixture.txt"}}}}`, i, tool)))
			req.Header.Set("Authorization", "Bearer bf_fixture")
			r, e := http.DefaultClient.Do(req)
			if e != nil {
				t.Error(e)
				return
			}
			_ = r.Body.Close()
		}(i)
	}
	deadline := time.Now().Add(time.Second)
	for hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	before := hits.Load()
	close(release)
	wg.Wait()
	if before != 2 || hits.Load() != 3 {
		t.Fatalf("multipart concurrency must admit two and queue the third: before=%d final=%d", before, hits.Load())
	}
}

func TestRCDriveSpaceExclusionIsPerOperation(t *testing.T) {
	if isDriveResourceSpaceID(registry.OperationInfo{ID: "drive.future", Service: "drive"}, []string{"space_id"}) {
		t.Fatal("an unknown Drive operation must not inherit a service-wide space exclusion")
	}
	if !isDriveResourceSpaceID(registry.OperationInfo{ID: "drive.folder.create", Service: "drive"}, []string{"space_id"}) {
		t.Fatal("known Drive resource selector must remain caller-controlled")
	}
}
