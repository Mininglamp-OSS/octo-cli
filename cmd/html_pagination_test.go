package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

type htmlPaginationDocument struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Latest  int    `json:"latest"`
	Updated string `json:"updated"`
}

type htmlPaginationMetadata struct {
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

type htmlPaginationBackendPage struct {
	Data       []htmlPaginationDocument `json:"data"`
	Pagination htmlPaginationMetadata   `json:"pagination"`
}

type htmlPaginationEnvelope struct {
	Data       []htmlPaginationDocument `json:"data"`
	Pagination htmlPaginationMetadata   `json:"_pagination"`
}

func htmlPaginationSlugs(docs []htmlPaginationDocument) []string {
	slugs := make([]string, len(docs))
	for i, doc := range docs {
		slugs[i] = doc.Slug
	}
	return slugs
}

func newHTMLPaginationTestFactory(t *testing.T, apiBaseURL string) *cmdutil.TestFactory {
	t.Helper()

	factory := newTestFactoryWithReg()
	cfg := &config.Config{APIBaseURL: apiBaseURL, BotToken: "app_test", Format: "json"}
	cred := &credential.BotCredential{Token: "app_test", Source: "test"}
	factory.SetConfig(cfg)
	factory.SetCredential(cred)
	factory.SetClient(client.New(cfg, cred, client.Options{NoRetry: true}))
	return factory
}

func TestHTMLListCanReadSecondPageAndPreservesPagination(t *testing.T) {
	documents := []htmlPaginationDocument{
		{Slug: "doc-1", Title: "First", Latest: 1, Updated: "2026-10-01T00:00:00Z"},
		{Slug: "doc-2", Title: "Second", Latest: 1, Updated: "2026-10-02T00:00:00Z"},
		{Slug: "doc-3", Title: "Third", Latest: 1, Updated: "2026-10-03T00:00:00Z"},
	}
	var backendPages []htmlPaginationBackendPage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/docs-html/v1/docs" {
			t.Errorf("request = %s %s, want GET /docs-html/v1/docs", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}

		// Model the backend's offset paging behavior: omitted values use its
		// defaults, and returned data is sliced rather than echoing the query.
		page, pageSize := 1, 20
		if raw := r.URL.Query().Get("page"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "invalid page", http.StatusBadRequest)
				return
			}
			page = parsed
		}
		if raw := r.URL.Query().Get("page_size"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				http.Error(w, "invalid page_size", http.StatusBadRequest)
				return
			}
			pageSize = parsed
		}

		start := (page - 1) * pageSize
		if start > len(documents) {
			start = len(documents)
		}
		end := start + pageSize
		if end > len(documents) {
			end = len(documents)
		}
		response := htmlPaginationBackendPage{
			Data: append([]htmlPaginationDocument(nil), documents[start:end]...),
			Pagination: htmlPaginationMetadata{
				Total:    len(documents),
				Page:     page,
				PageSize: pageSize,
			},
		}
		backendPages = append(backendPages, response)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Errorf("encode backend page: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	firstFactory := newHTMLPaginationTestFactory(t, server.URL)
	firstOut, firstErr, err := execRoot(t, firstFactory, "html", "list")
	if err != nil {
		t.Fatalf("first-page list: %v\n%s", err, firstErr)
	}
	var first htmlPaginationEnvelope
	if err := json.Unmarshal([]byte(firstOut), &first); err != nil {
		t.Fatalf("decode first-page envelope: %v -- %s", err, firstOut)
	}
	if got, want := htmlPaginationSlugs(first.Data), []string{"doc-1", "doc-2", "doc-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first-page documents = %v, want %v", got, want)
	}
	if len(backendPages) != 1 || !reflect.DeepEqual(first.Pagination, backendPages[0].Pagination) {
		t.Fatalf("first-page _pagination = %+v, backend metadata = %+v", first.Pagination, backendPages)
	}

	secondFactory := newHTMLPaginationTestFactory(t, server.URL)
	secondOut, secondErr, err := execRoot(t, secondFactory, "html", "list", "--page", "2", "--page-size", "2")
	if err != nil {
		t.Fatalf("second-page list: %v\n%s", err, secondErr)
	}
	var second htmlPaginationEnvelope
	if err := json.Unmarshal([]byte(secondOut), &second); err != nil {
		t.Fatalf("decode second-page envelope: %v -- %s", err, secondOut)
	}
	if got, want := htmlPaginationSlugs(second.Data), []string{"doc-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second-page documents = %v, want %v", got, want)
	}
	if len(backendPages) != 2 || !reflect.DeepEqual(second.Pagination, backendPages[1].Pagination) {
		t.Fatalf("second-page _pagination = %+v, backend metadata = %+v", second.Pagination, backendPages)
	}
}
