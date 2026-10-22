package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

const loopQueryTestWorkspaceID = "99999999-9999-4999-8999-999999999999"

func newLoopQueryContractFactory(t *testing.T, handler http.HandlerFunc) *cmdutil.TestFactory {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	factory := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: server.URL, BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	factory.SetConfig(cfg)
	factory.SetCredential(cred)
	factory.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	return factory
}

func writeLoopQueryTestCollection(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
}

func TestLoopTaskSearchRejectsMissingQueryBeforeRequest(t *testing.T) {
	var requests int
	factory := newLoopQueryContractFactory(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeLoopQueryTestCollection(w)
	})

	_, _, err := execRoot(t, factory, "loop", "task", "search", "--workspace-id", loopQueryTestWorkspaceID)
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "q" not set`) {
		t.Fatalf("task search without --q error = %v, want required --q flag error", err)
	}
	if requests != 0 {
		t.Fatalf("missing --q sent %d requests, want 0", requests)
	}
}
