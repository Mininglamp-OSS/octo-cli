package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

type sceneTestCapture struct {
	f        *cmdutil.TestFactory
	requests int
}

func sceneTestFactory(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *sceneTestCapture) {
	t.Helper()
	capture := &sceneTestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.requests++
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	factory := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: server.URL, BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test"}
	factory.SetConfig(cfg)
	factory.SetCredential(cred)
	factory.SetClient(client.New(cfg, cred, client.Options{NoRetry: true, ErrOut: factory.ErrOut}))
	capture.f = factory
	return server, capture
}

func serveSceneTest(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		_, _ = io.WriteString(w, body)
	}
}

func TestGetSceneAllowsNilGlobals(t *testing.T) {
	_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
	capture.f.Globals = nil
	root := &cobra.Command{}
	root.SetContext(context.Background())
	if _, err := getScene(root, capture.f.Factory, "d1"); err != nil {
		t.Fatal(err)
	}
}
