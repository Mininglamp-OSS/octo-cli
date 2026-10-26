package service

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// XIN-2424: five octo-docs-backend routes take the file bytes as the raw
// request body (not JSON, not multipart). x-octo-raw-body marks them; the
// engine streams --file with the single declared media type.

func TestXIN2424RawBodyUploads(t *testing.T) {
	reg := registry.MustNew()
	cases := []struct {
		op, method, path, contentType string
		maxBytes                      int64
		args                          []string
		wantHeaders                   map[string]string
	}{
		{"docs.ppt.media.upload", "POST", "/v1/bot/docs/{docId}/ppt/media", "application/octet-stream", 50 << 20,
			[]string{"docs", "ppt", "media", "upload", "d1", "--media-type", "image/png", "--file-name", "a.png"},
			map[string]string{"X-Media-Type": "image/png", "X-File-Name": "a.png"}},
		{"docs.attachments.svg-upload", "POST", "/v1/bot/docs/{docId}/attachments/svg", "image/svg+xml", 1 << 20,
			[]string{"docs", "attachments", "svg-upload", "d1"}, nil},
		{"docs.raw-import.docx", "POST", "/v1/bot/docs/{docId}/import/docx",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document", 200 << 20,
			[]string{"docs", "raw-import", "docx", "d1", "--apply", "true"}, map[string]string{"X-Octo-Import-Apply": "true"}},
		{"docs.raw-import.markdown", "POST", "/v1/bot/docs/{docId}/import/markdown", "text/markdown", 20 << 20,
			[]string{"docs", "raw-import", "markdown", "d1"}, nil},
		{"docs.raw-import.xlsx", "POST", "/v1/bot/docs/{docId}/import/xlsx",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", 200 << 20,
			[]string{"docs", "raw-import", "xlsx", "d1"}, nil},
	}
	file := filepath.Join(t.TempDir(), "payload.bin")
	if err := os.WriteFile(file, []byte("raw-bytes\x00\x01"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			d, ok := reg.GetOperation(tc.op)
			if !ok {
				t.Fatalf("%s not in registry", tc.op)
			}
			if d.Method != tc.method || d.Path != tc.path || !d.RawBody || d.Multipart {
				t.Fatalf("%s: %s %s raw=%v multipart=%v", tc.op, d.Method, d.Path, d.RawBody, d.Multipart)
			}
			if d.RawBodyContentType != tc.contentType || d.RawBodyMaxBytes != tc.maxBytes {
				t.Errorf("content type %q max %d, want %q %d", d.RawBodyContentType, d.RawBodyMaxBytes, tc.contentType, tc.maxBytes)
			}
			var gotCT, gotPath string
			var gotBody []byte
			gotHeaders := http.Header{}
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				gotCT, gotPath = r.Header.Get("Content-Type"), r.URL.Path
				gotHeaders = r.Header.Clone()
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{}}`))
			})
			root.SetArgs(append(append([]string{}, tc.args...), "--file", file))
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if gotPath != strings.Replace(tc.path, "{docId}", "d1", 1) || gotCT != tc.contentType || string(gotBody) != "raw-bytes\x00\x01" {
				t.Errorf("request %s ct=%q body=%q", gotPath, gotCT, gotBody)
			}
			for k, v := range tc.wantHeaders {
				if gotHeaders.Get(k) != v {
					t.Errorf("header %s = %q, want %q", k, gotHeaders.Get(k), v)
				}
			}
		})
	}
}

func TestXIN2424RawBodyRefusesEmptyAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.svg")
	big := filepath.Join(dir, "big.svg")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	ok := filepath.Join(dir, "ok.svg")
	if err := os.WriteFile(ok, make([]byte, 1<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file    string
		wantErr string
	}{
		{empty, "--file is empty"},
		{big, "exceeds the 1048576-byte limit"},
		{ok, ""},
	} {
		hits := 0
		root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
			hits++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"attachId":"a"}`))
		})
		root.SetArgs([]string{"docs", "attachments", "svg-upload", "d1", "--file", tc.file})
		err := root.Execute()
		if tc.wantErr == "" {
			if err != nil || hits != 1 {
				t.Errorf("%s: err=%v hits=%d, want one request", tc.file, err, hits)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) || hits != 0 {
			t.Errorf("%s: err=%v hits=%d, want local refusal containing %q", tc.file, err, hits, tc.wantErr)
		}
	}
}
