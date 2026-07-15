package cmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apiClient "github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func marketplaceArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	body := []byte("---\nname: demo\n---\n")
	w, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func TestMarketplaceSkillInstallAndAlias(t *testing.T) {
	archive, digest := marketplaceArchive(t)
	var gotAuth string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gateway/api/v1/skill/skill-1":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"code":0,"data":{"id":"skill-1","name":"demo","file_sha256":"` + digest + `"}}`))
		case "/gateway/api/v1/skill/skill-1/download":
			gotAuth = r.Header.Get("Authorization")
			http.Redirect(w, r, srv.URL+"/artifact/demo.zip", http.StatusFound)
		case "/artifact/demo.zip":
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	f := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: srv.URL + "/normal-api", MarketplaceAPIPrefix: "/gateway/api/v1", BotToken: "bf_test", Format: "json"}
	cred := &credential.BotCredential{Token: "bf_test", Source: "test"}
	f.SetConfig(cfg)
	f.SetCredential(cred)
	f.SetClient(apiClient.New(cfg, cred, apiClient.Options{NoRetry: true, ErrOut: f.ErrOut}))
	root := t.TempDir()
	out, errOut, err := execRoot(t, f, "market", "skills", "skill-1", "--install", root)
	if err != nil {
		t.Fatalf("install: %v\n%s", err, errOut)
	}
	if gotAuth != "Bearer bf_test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(out, `"source": "marketplace"`) {
		t.Fatalf("unexpected output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(root, "demo", "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not installed: %v", err)
	}
}

func TestMarketplaceSkillRequiresInstall(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{BotToken: "bf_test", Format: "json"})
	f.SetCredential(&credential.BotCredential{Token: "bf_test"})
	_, errOut, err := execRoot(t, f, "marketplace", "skills", "demo")
	if err == nil || !strings.Contains(errOut, "--install is required") {
		t.Fatalf("error = %v, stderr = %s", err, errOut)
	}
}

func TestMarketplaceSkillRejectsAppBot(t *testing.T) {
	f := newTestFactoryWithReg()
	f.SetConfig(&config.Config{BotToken: "app_test", Format: "json"})
	f.SetCredential(&credential.BotCredential{Token: "app_test"})
	_, errOut, err := execRoot(t, f, "marketplace", "skills", "demo", "--install", t.TempDir())
	if err == nil || !strings.Contains(errOut, "bf_*") {
		t.Fatalf("error = %v, stderr = %s", err, errOut)
	}
}

func TestMarketplaceCommandAlias(t *testing.T) {
	f := newTestFactoryWithReg()
	root := NewRootCmd(f.Factory)
	marketplaceCmd, _, err := root.Find([]string{"market", "skills"})
	if err != nil {
		t.Fatalf("find alias: %v", err)
	}
	if marketplaceCmd.Name() != "skills" {
		t.Fatalf("alias resolved to %q, want skills", marketplaceCmd.Name())
	}
}

func TestMarketplaceRejectsSingularSkillCommand(t *testing.T) {
	f := newTestFactoryWithReg()
	root := NewRootCmd(f.Factory)
	marketplaceCmd, _, err := root.Find([]string{"marketplace"})
	if err != nil {
		t.Fatalf("find marketplace: %v", err)
	}
	for _, command := range marketplaceCmd.Commands() {
		if command.Name() == "skill" || command.HasAlias("skill") {
			t.Fatal("singular skill command must not be registered")
		}
	}
}
