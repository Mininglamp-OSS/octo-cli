package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestRequestResponseBoundIsOptIn(t *testing.T) {
	body := `{"padding":"` + strings.Repeat("x", 16384) + `"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
	defer server.Close()
	cli := New(&config.Config{APIBaseURL: server.URL}, &credential.BotCredential{Token: "bf_fixture"}, Options{NoRetry: true, ErrOut: io.Discard})
	if _, err := cli.Do(context.Background(), &Request{Method: http.MethodPost, Path: "/verify", MaxResponseBytes: 16 << 10, DisableRetry: true}); err == nil || output.AsExitError(err).Code != "RESPONSE_TOO_LARGE" {
		t.Fatal("bounded verification response was not refused")
	}
	if got, err := cli.Do(context.Background(), &Request{Method: http.MethodGet, Path: "/business"}); err != nil || string(got) != body {
		t.Fatal("ordinary response behavior changed")
	}
}
