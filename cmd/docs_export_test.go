package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

func TestDocsExport_AllFormatsUseExpectedEndpointAndWriteBytes(t *testing.T) {
	formats := []string{"md", "docx", "pdf", "xlsx", "png", "svg"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			const payload = "exported bytes"
			tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
				wantPath := "/v1/bot/docs/d%2F1/export"
				if format != "png" && format != "svg" {
					wantPath += "/file"
				}
				if r.Method != http.MethodGet || r.URL.EscapedPath() != wantPath {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.EscapedPath(), wantPath)
				}
				if got := r.URL.Query().Get("format"); got != format {
					t.Errorf("format query = %q, want %q", got, format)
				}
				_, _ = io.WriteString(w, payload)
			})
			dst := filepath.Join(t.TempDir(), "result."+format)
			out, _, err := execRoot(t, tf, "docs", "export", "d/1", "--export-format", format, "-o", dst)
			if err != nil {
				t.Fatalf("export: %v", err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || string(got) != payload {
				t.Fatalf("output file = %q, %v", got, err)
			}
			var env struct {
				OK   bool `json:"ok"`
				Data struct {
					Output string `json:"output"`
					Size   int    `json:"size"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(out), &env); err != nil {
				t.Fatal(err)
			}
			if !env.OK || env.Data.Output != dst || env.Data.Size != len(payload) {
				t.Errorf("envelope = %+v", env)
			}
		})
	}
}

func TestDocsExport_ValidatesRequiredFlagsAndExtensionBeforeRequest(t *testing.T) {
	called := false
	tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) { called = true })

	cases := [][]string{
		{"docs", "export", "d1", "--export-format", "pdf", "-o", "report.docx"},
		{"docs", "export", "d1", "--export-format", "zip", "-o", "report.zip"},
		{"docs", "export", "d1", "--export-format", "pdf"},
		{"docs", "export", "d1", "-o", "report.pdf"},
	}
	for _, args := range cases {
		tf.Out.Reset()
		tf.ErrOut.Reset()
		if _, _, err := execRoot(t, tf, args...); err == nil {
			t.Errorf("args %v: expected validation error", args)
		}
	}
	if called {
		t.Error("server must not be called for invalid input")
	}
}

func TestDocsExport_DryRunDoesNotWriteOrRequest(t *testing.T) {
	called := false
	tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	dst := filepath.Join(t.TempDir(), "report.pdf")
	out, _, err := execRoot(t, tf, "--dry-run", "docs", "export", "d1", "--export-format", "pdf", "-o", dst)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if called {
		t.Error("server must not be called")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dry run wrote destination: %v", err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("invalid JSON envelope: %s", out)
	}
}

func TestDocsExport_ErrorResponsePreservesExistingTarget(t *testing.T) {
	tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":"export_failed","message":"nope"}`)
	})
	dst := filepath.Join(t.TempDir(), "report.pdf")
	const old = "keep me"
	if err := os.WriteFile(dst, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execRoot(t, tf, "docs", "export", "d1", "--export-format", "pdf", "-o", dst); err == nil {
		t.Fatal("expected backend error")
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != old {
		t.Fatalf("existing target changed: %q, %v", got, err)
	}
}

func TestDocsExport_DoesNotShadowGlobalFormat(t *testing.T) {
	tf := newTestFactoryWithReg()
	root := NewRootCmd(tf.Factory)
	docs, _, err := root.Find([]string{"docs"})
	if err != nil {
		t.Fatal(err)
	}
	export, _, err := docs.Find([]string{"export"})
	if err != nil {
		t.Fatal(err)
	}
	if export.LocalFlags().Lookup("export-format") == nil {
		t.Fatal("missing local --export-format")
	}
	if export.LocalFlags().Lookup("format") != nil {
		t.Fatal("local --format shadows global envelope format")
	}
}

func TestDocsExport_RejectsOpaqueDocumentIDSentinelsBeforeRequest(t *testing.T) {
	for _, docID := range []string{"", ".", ".."} {
		t.Run(docID, func(t *testing.T) {
			calls := 0
			tf, _ := importTestRoot(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				_, _ = io.WriteString(w, "exported")
			})
			dst := filepath.Join(t.TempDir(), "report.pdf")
			if _, _, err := execRoot(t, tf, "docs", "export", docID, "--export-format", "pdf", "-o", dst); err == nil {
				t.Fatalf("docId %q was accepted", docID)
			}
			if calls != 0 {
				t.Fatalf("docId %q made %d requests", docID, calls)
			}
		})
	}
}

func TestDocsExport_EscapesOpaqueDocumentID(t *testing.T) {
	var gotPath string
	tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = io.WriteString(w, "exported")
	})
	dst := filepath.Join(t.TempDir(), "report.pdf")
	if _, _, err := execRoot(t, tf, "docs", "export", "d/文档", "--export-format", "pdf", "-o", dst); err != nil {
		t.Fatalf("export: %v", err)
	}
	const wantPath = "/v1/bot/docs/d%2F%E6%96%87%E6%A1%A3/export/file"
	if gotPath != wantPath {
		t.Fatalf("request path = %q, want %q", gotPath, wantPath)
	}
}

func TestDocsExport_MissingFormatHasDistinctValidationError(t *testing.T) {
	called := false
	tf, _ := importTestRoot(t, func(http.ResponseWriter, *http.Request) { called = true })
	_, _, missingErr := execRoot(t, tf, "docs", "export", "d1", "-o", "report.pdf")
	_, _, unsupportedErr := execRoot(t, tf, "docs", "export", "d1", "--export-format", "zip", "-o", "report.zip")
	missing := output.AsExitError(missingErr)
	if missing == nil || missing.Type != "validation" || missing.Code != "VALIDATION_ERROR" {
		t.Fatalf("missing --export-format error = %v, want structured validation", missingErr)
	}
	unsupported := output.AsExitError(unsupportedErr)
	if unsupported == nil || unsupported.Type != "validation" || unsupported.Code != "VALIDATION_ERROR" {
		t.Fatalf("unsupported --export-format error = %v, want structured validation", unsupportedErr)
	}
	if !strings.Contains(missing.Message, "--export-format") || strings.Contains(strings.ToLower(missing.Message), "unsupported") {
		t.Fatalf("missing-format diagnosis = %q", missing.Message)
	}
	if !strings.Contains(strings.ToLower(unsupported.Message), "unsupported") {
		t.Fatalf("unsupported-format diagnosis = %q", unsupported.Message)
	}
	if missing.Message == unsupported.Message {
		t.Fatalf("missing and unsupported format diagnoses are indistinguishable: %q", missing.Message)
	}
	if called {
		t.Fatal("server must not be called for invalid format input")
	}
}
