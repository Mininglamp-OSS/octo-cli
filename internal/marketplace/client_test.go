package marketplace

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"

	apiClient "github.com/Mininglamp-OSS/octo-cli/internal/client"
)

type downloadAPI struct {
	url string
}

func (a downloadAPI) Do(context.Context, *apiClient.Request) ([]byte, error) {
	return []byte(fmt.Sprintf(`{"data":{"download_url":%q,"file_sha256":"abc123"}}`, a.url)), nil
}

func newIPv4TLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
			t.Skipf("sandbox does not permit loopback listeners: %v", err)
		}
		t.Fatalf("listen on 127.0.0.1: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener = listener
	server.StartTLS()
	return server
}

func TestDownloadSkillRejectsHTTP(t *testing.T) {
	client := NewClient(downloadAPI{url: "http://example.com/skill.zip"}, nil)
	_, err := client.DownloadSkill(context.Background(), "skill-1")
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("error = %v, want https rejection", err)
	}
}

func TestDownloadSkillAllowsLocalHTTPWhenExplicit(t *testing.T) {
	want := []byte("local skill archive")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(want)
	}))
	defer server.Close()

	client := NewClient(downloadAPI{url: server.URL}, server.Client(), Options{AllowInsecureLocalhost: true})
	got, err := client.DownloadSkill(context.Background(), "skill-1")
	if err != nil {
		t.Fatalf("DownloadSkill: %v", err)
	}
	if string(got.Body) != string(want) || got.SHA256 != "abc123" {
		t.Fatalf("archive = %#v, want body %q and digest", got, want)
	}
}

func TestDownloadSkillRejectsPrivateIPHost(t *testing.T) {
	server := newIPv4TLSServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("private host must be rejected before the request")
	}))
	defer server.Close()

	client := NewClient(downloadAPI{url: server.URL}, server.Client())
	_, err := client.DownloadSkill(context.Background(), "skill-1")
	if err == nil || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("error = %v, want private-address rejection", err)
	}
}

func TestDownloadSkillRejectsMetadataIP(t *testing.T) {
	client := NewClient(downloadAPI{url: "https://169.254.169.254/latest/meta-data"}, nil)
	_, err := client.DownloadSkill(context.Background(), "skill-1")
	if err == nil || !strings.Contains(err.Error(), "private or local address") {
		t.Fatalf("error = %v, want link-local rejection", err)
	}
}

func TestDownloadSkillDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	server := newIPv4TLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, serverURL.Host)
	}
	httpClient := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	client := NewClient(downloadAPI{url: "https://93.184.216.34/skill.zip"}, httpClient)
	_, err = client.DownloadSkill(context.Background(), "skill-1")
	if err == nil {
		t.Fatal("expected redirect response to fail")
	}
	if redirected {
		t.Fatal("redirect target was requested")
	}
}
