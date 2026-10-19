package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearElementsForExport(t *testing.T) {
	elements := []map[string]any{
		{"id": "a", "type": "rectangle", "isDeleted": false},
		{"id": "b", "type": "rectangle", "isDeleted": true}, // dropped
		{"id": "c", "type": "arrow", "lastCommittedPoint": []any{float64(1), float64(2)}},
		{"id": "d", "type": "line", "lastCommittedPoint": []any{float64(3), float64(4)}},
	}
	out := clearElementsForExport(elements)
	if len(out) != 3 {
		t.Fatalf("expected 3 surviving elements, got %d", len(out))
	}
	if out[0]["id"] != "a" || out[1]["id"] != "c" || out[2]["id"] != "d" {
		t.Fatalf("order/content wrong: %v", out)
	}
	if out[1]["lastCommittedPoint"] != nil || out[2]["lastCommittedPoint"] != nil {
		t.Fatalf("linear lastCommittedPoint must be nulled: %v %v", out[1], out[2])
	}
	// The original element must not be mutated (clone-on-write for linear).
	if elements[2]["lastCommittedPoint"] == nil {
		t.Fatal("clearElementsForExport mutated the source element")
	}
}

func TestCleanAppStateForExport(t *testing.T) {
	in := map[string]any{
		"viewBackgroundColor": "#ffffff",
		"gridSize":            float64(20),
		"gridStep":            float64(5),
		"gridModeEnabled":     true,
		"theme":               "dark",     // stripped
		"scrollX":             float64(9), // stripped
		"name":                "board",    // stripped
	}
	out := cleanAppStateForExport(in)
	if len(out) != 4 {
		t.Fatalf("expected only the 4 allowlisted keys, got %v", out)
	}
	for _, k := range []string{"viewBackgroundColor", "gridSize", "gridStep", "gridModeEnabled"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing allowlisted key %q", k)
		}
	}
	for _, k := range []string{"theme", "scrollX", "name"} {
		if _, ok := out[k]; ok {
			t.Errorf("non-allowlisted key %q leaked into export", k)
		}
	}
}

func TestReferencedFileIDs(t *testing.T) {
	elements := []map[string]any{
		{"id": "img1", "type": "image", "fileId": "f1"},
		{"id": "img2", "type": "image", "fileId": "f2"},
		{"id": "rect", "type": "rectangle"},
	}
	ids := referencedFileIDs(elements)
	if len(ids) != 2 || !ids["f1"] || !ids["f2"] {
		t.Fatalf("referenced fileIds = %v", ids)
	}
}

func TestMarshalExcalidrawEnvelopeOrderAndEscaping(t *testing.T) {
	elements := []map[string]any{{"id": "a", "type": "text", "text": "1 < 2 && 3 > 2"}}
	data, err := marshalExcalidrawEnvelope(elements, map[string]any{"viewBackgroundColor": "#fff"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// Top-level key order must match serializeAsJSON: type,version,source,elements,appState,files.
	order := []string{`"type"`, `"version"`, `"source"`, `"elements"`, `"appState"`, `"files"`}
	last := -1
	for _, key := range order {
		i := strings.Index(s, key)
		if i < 0 {
			t.Fatalf("missing key %s in %s", key, s)
		}
		if i < last {
			t.Fatalf("key %s out of order in %s", key, s)
		}
		last = i
	}
	// HTML escaping disabled: < > & are written literally (as JSON.stringify does),
	// not as the < / > / & forms Go's default encoder emits.
	if !strings.Contains(s, "1 < 2 && 3 > 2") {
		t.Fatalf("expected unescaped text, got %s", s)
	}
	for _, esc := range []string{"\\u003c", "\\u003e", "\\u0026"} {
		if strings.Contains(s, esc) {
			t.Fatalf("HTML escaping must be disabled (found %s): %s", esc, s)
		}
	}
	// A valid, re-parseable version-2 envelope.
	var env map[string]any
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if env["type"] != "excalidraw" || env["version"] != float64(2) || env["source"] != "octo-cli" {
		t.Fatalf("envelope header = %v", env)
	}
}

func TestDocsSceneExportKeepsPNGAndRemovesTopLevelAlias(t *testing.T) {
	var path, query string
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	root := NewRootCmd(capture.f.Factory)
	if commandAt(root, "docs", "save-excalidraw") != nil {
		t.Fatal("top-level docs save-excalidraw alias must not be registered")
	}
	if _, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "png"); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/bot/docs/d1/export" || query != "format=png" {
		t.Fatalf("PNG request = %s?%s", path, query)
	}
}

func TestDocsExcalidrawExportRejectsBadExtension(t *testing.T) {
	_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
	_, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", "board.json")
	if err == nil {
		t.Fatal("expected rejection for non-.excalidraw extension")
	}
}

func TestDocsExcalidrawExportRequiresOutput(t *testing.T) {
	_, capture := sceneTestFactory(t, serveSceneTest(`{"elements":[],"files":{},"baseVersion":"BV"}`))
	_, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw")
	if err == nil || !strings.Contains(err.Error(), `--output is required`) {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifiedPortableMIMERejectsEmptyAndMismatch(t *testing.T) {
	if _, err := verifiedPortableMIME(nil, "image/png", ""); err == nil || !strings.Contains(err.Error(), "empty body") {
		t.Fatalf("empty error=%v", err)
	}
	if _, err := verifiedPortableMIME([]byte("plain text"), "image/png", "image/png"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch error=%v", err)
	}
}

func TestDetectPortableMIMEAcceptsValidSVGPrologs(t *testing.T) {
	for _, body := range [][]byte{
		[]byte("\xef\xbb\xbf<?xml version=\"1.0\"?><!-- lead --><svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"),
		[]byte("<!-- lead --><!DOCTYPE svg><svg></svg>"),
		[]byte("<?xml version=\"1.0\"?><x:svg xmlns:x=\"urn:test\"></x:svg>"),
		[]byte(`<!DOCTYPE svg [<!ENTITY ns_extend "http://ns.adobe.com/Extensibility/1.0/">]><svg xmlns:x="&ns_extend;"></svg>`),
	} {
		if got := detectPortableMIME(body); got != "image/svg+xml" {
			t.Errorf("MIME=%q for %q", got, body)
		}
	}
}

func TestDetectPortableMIMERejectsSVGTextWithoutSVGRoot(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`<?xml version="1.0"?><html><!-- <svg></svg> --></html>`),
		[]byte(`<!DOCTYPE svg><html></html>`),
		[]byte(`<?xml version="1.0"?><not-svg/>`),
	} {
		if got := detectPortableMIME(body); got == "image/svg+xml" {
			t.Errorf("false SVG detection for %q", body)
		}
	}
}

func TestNormalizePortableMIMEAliases(t *testing.T) {
	for input, want := range map[string]string{"image/jpg": "image/jpeg", "IMAGE/PJPEG; x=y": "image/jpeg", "image/x-png": "image/png"} {
		if got := normalizeMIME(input); got != want {
			t.Errorf("normalizeMIME(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestAttachmentLocationReadsNestedMIME(t *testing.T) {
	url, mime := attachmentLocation(map[string]any{"data": map[string]any{"downloadUrl": "https://example.com/x", "mimeType": "image/png"}})
	if url != "https://example.com/x" || mime != "image/png" {
		t.Fatalf("location=%q MIME=%q", url, mime)
	}
}

func TestReadPortableAttachmentBodyRejectsTruncatedTransfer(t *testing.T) {
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 100,
		Body:          io.NopCloser(bytes.NewReader(portablePNGTestBytes())),
	}
	if _, err := readPortableAttachmentBody(resp, maxDocsImportBytes, "image/png"); err == nil || !strings.Contains(err.Error(), "sent") {
		t.Fatalf("truncated transfer error=%v", err)
	}
}

func TestReadPortableAttachmentBodyRejectsPartialResponse(t *testing.T) {
	body := portablePNGTestBytes()
	resp := &http.Response{
		StatusCode:    http.StatusPartialContent,
		ContentLength: int64(len(body)),
		Header:        http.Header{"Content-Type": []string{"image/png"}},
		Body:          io.NopCloser(bytes.NewReader(body)),
	}
	if _, err := readPortableAttachmentBody(resp, maxDocsImportBytes, "image/png"); err == nil || !strings.Contains(err.Error(), "HTTP 206") {
		t.Fatalf("partial response error=%v", err)
	}
}

func TestReadPortableAttachmentBodyRejectsPredictedBase64OverflowBeforeRead(t *testing.T) {
	body := &trackingReadCloser{Reader: bytes.NewReader(portablePNGTestBytes())}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 100,
		Header:        http.Header{"Content-Type": []string{"image/png"}},
		Body:          body,
	}
	if _, err := readPortableAttachmentBody(resp, 100, "image/png"); err == nil || !strings.Contains(err.Error(), "base64-expanded") {
		t.Fatalf("overflow error=%v", err)
	}
	if body.reads != 0 || !body.closed {
		t.Fatalf("body reads=%d closed=%v, want zero reads and closed", body.reads, body.closed)
	}
}

type trackingReadCloser struct {
	*bytes.Reader
	reads    int
	closed   bool
	closeErr error
}

func (r *trackingReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.Reader.Read(p)
}

func (r *trackingReadCloser) Close() error {
	r.closed = true
	return r.closeErr
}

func TestReadPortableAttachmentBodyKeepsPrimaryErrorOverCloseError(t *testing.T) {
	closeErr := errors.New("close failed")
	for _, tc := range []struct {
		name      string
		resp      *http.Response
		remaining int64
		want      string
	}{
		{"http", &http.Response{StatusCode: http.StatusBadGateway, Body: &trackingReadCloser{Reader: bytes.NewReader(nil), closeErr: closeErr}}, maxDocsImportBytes, "HTTP 502"},
		{"size", &http.Response{StatusCode: http.StatusOK, ContentLength: 100, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: &trackingReadCloser{Reader: bytes.NewReader(nil), closeErr: closeErr}}, 10, "base64-expanded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readPortableAttachmentBody(tc.resp, tc.remaining, "image/png")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestDocsExcalidrawExportDryRunNoWriteNoDownload(t *testing.T) {
	body := `{"elements":[{"id":"a","type":"rectangle","isDeleted":false},{"id":"b","type":"rectangle","isDeleted":true},{"id":"img","type":"image","fileId":"f1"}],"files":{"f1":{"attachId":"att1"}},"appState":{"viewBackgroundColor":"#eee","theme":"dark"},"baseVersion":"BV"}`
	_, capture := sceneTestFactory(t, serveSceneTest(body))
	dir := t.TempDir()
	out := filepath.Join(dir, "board.excalidraw")
	stdout, _, err := execRoot(t, capture.f, "--dry-run", "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("dry-run must not write the file")
	}
	if capture.requests != 1 {
		// Only the scene GET; no attachment resolve/download during dry-run.
		t.Fatalf("dry-run made %d requests; want 1 (scene GET only)", capture.requests)
	}
	var env map[string]any
	_ = json.Unmarshal([]byte(stdout), &env)
	data, _ := env["data"].(map[string]any)
	if data["elements"] != float64(2) { // deleted 'b' excluded
		t.Fatalf("dry-run elements=%v, want 2", data["elements"])
	}
	if data["files"] != float64(1) { // 'f1' still referenced by live image
		t.Fatalf("dry-run files=%v, want 1", data["files"])
	}
	keys, _ := data["appState"].([]any)
	if len(keys) != 1 || keys[0] != "viewBackgroundColor" {
		t.Fatalf("dry-run appState keys=%v, want [viewBackgroundColor]", data["appState"])
	}
}

func TestValidatePortableAttachmentRefsFailsLoud(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]any
		want  string
	}{
		{name: "missing", files: map[string]any{}, want: `attachment "f1" is referenced`},
		{name: "invalid metadata", files: map[string]any{"f1": "bad"}, want: `attachment "f1" has invalid`},
		{name: "missing attachId", files: map[string]any{"f1": map[string]any{}}, want: `attachment "f1" is missing attachId`},
		{name: "dot attachId", files: map[string]any{"f1": map[string]any{"attachId": ".."}}, want: `attachId must not be a dot path segment`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validatePortableAttachmentRefs(tc.files, map[string]bool{"f1": true})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestValidatePortableAttachmentRefsEnforcesCountLimit(t *testing.T) {
	files := map[string]any{}
	referenced := map[string]bool{}
	for i := 0; i <= maxPortableExportAttachments; i++ {
		id := fmt.Sprintf("f%d", i)
		files[id] = map[string]any{"attachId": "att_" + id}
		referenced[id] = true
	}
	_, err := validatePortableAttachmentRefs(files, referenced)
	if err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("err=%v", err)
	}
}

func TestDocsExcalidrawExportFailsBeforeWriteOnMissingFile(t *testing.T) {
	body := `{"elements":[{"id":"img","type":"image","fileId":"f_missing"}],"files":{},"appState":{},"baseVersion":"BV"}`
	_, capture := sceneTestFactory(t, serveSceneTest(body))
	out := filepath.Join(t.TempDir(), "board.excalidraw")
	_, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out)
	if err == nil || !strings.Contains(err.Error(), "f_missing") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("output unexpectedly exists: %v", statErr)
	}
}

func TestDocsExcalidrawExportWritesAtomicFile(t *testing.T) {
	body := `{"elements":[{"id":"a","type":"rectangle","isDeleted":false},{"id":"b","type":"rectangle","isDeleted":true}],"files":{},"appState":{"viewBackgroundColor":"#123456","zoom":2},"baseVersion":"BV"}`
	_, capture := sceneTestFactory(t, serveSceneTest(body))
	dir := t.TempDir()
	out := filepath.Join(dir, "board.excalidraw")
	_, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("output file: %v", err)
	}
	var env struct {
		Type     string           `json:"type"`
		Version  int              `json:"version"`
		Elements []map[string]any `json:"elements"`
		AppState map[string]any   `json:"appState"`
		Files    map[string]any   `json:"files"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("written file is not valid JSON: %v\n%s", err, raw)
	}
	if env.Type != "excalidraw" || env.Version != 2 {
		t.Fatalf("header = %s/%d", env.Type, env.Version)
	}
	if len(env.Elements) != 1 || env.Elements[0]["id"] != "a" {
		t.Fatalf("deleted element not stripped: %v", env.Elements)
	}
	if len(env.AppState) != 1 || env.AppState["viewBackgroundColor"] != "#123456" {
		t.Fatalf("appState not filtered to allowlist: %v", env.AppState)
	}
	// No temp files should remain in the destination directory.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("expected exactly the output file, found %d entries", len(entries))
	}
}

func portablePNGTestBytes() []byte {
	return append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xa5}, 32)...)
}

func TestDocsExcalidrawExportEmbedsResolvedImage(t *testing.T) {
	image := portablePNGTestBytes()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/docs/d1/scene":
			_, _ = io.WriteString(w, `{"elements":[{"id":"img","type":"image","fileId":"f1"}],"files":{"f1":{"attachId":"att1"}},"appState":{},"baseVersion":"BV"}`)
		case "/v1/bot/docs/d1/attachments/att1":
			_ = json.NewEncoder(w).Encode(map[string]string{"url": server.URL + "/blob", "mimeType": "image/png"})
		case "/blob":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) {
		server.Config.Handler.ServeHTTP(w, r)
	})
	f := capture.f
	out := filepath.Join(t.TempDir(), "board.excalidraw")
	if _, _, err := execRoot(t, f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Files map[string]struct {
			DataURL string `json:"dataURL"`
		} `json:"files"`
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)
	if envelope.Files["f1"].DataURL != want {
		t.Fatalf("embedded data URL mismatch")
	}
}

func TestDocsExcalidrawExportRejectsUnsafeAttachmentURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/docs/d1/scene":
			_, _ = io.WriteString(w, `{"elements":[{"id":"img","type":"image","fileId":"f1"}],"files":{"f1":{"attachId":"att1"}},"appState":{},"baseVersion":"BV"}`)
		case "/v1/bot/docs/d1/attachments/att1":
			_, _ = io.WriteString(w, `{"url":"https://user:secret@example.com/object","mimeType":"image/png"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) {
		server.Config.Handler.ServeHTTP(w, r)
	})
	f := capture.f
	out := filepath.Join(t.TempDir(), "board.excalidraw")
	_, stderr, err := execRoot(t, f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out)
	if err == nil || !strings.Contains(err.Error(), "must not embed credentials") {
		t.Fatalf("err=%v", err)
	}
	if strings.Contains(stderr, "secret") {
		t.Fatalf("stderr leaked signed URL credentials: %s", stderr)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("unsafe export wrote output: %v", statErr)
	}
}

func TestDocsExcalidrawExportNearLimitCanBeImported(t *testing.T) {
	// 18 MiB expands to ~24 MiB as base64, leaving realistic JSON overhead while
	// staying close to the shared 25 MiB import boundary.
	image := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xa5}, 18<<20-8)...)
	var server *httptest.Server
	var imported []byte
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/bot/docs/d1/scene":
			_, _ = io.WriteString(w, `{"elements":[{"id":"img","type":"image","fileId":"f1"}],"files":{"f1":{"attachId":"att1"}},"appState":{},"baseVersion":"BV"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/bot/docs/d1/attachments/att1":
			_ = json.NewEncoder(w).Encode(map[string]string{"url": server.URL + "/blob", "mimeType": "image/png"})
		case r.Method == http.MethodGet && r.URL.Path == "/blob":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(image)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/bot/docs/d2/import/excalidraw":
			imported, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"format":"excalidraw","importedElements":1,"importedFiles":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) { server.Config.Handler.ServeHTTP(w, r) })
	out := filepath.Join(t.TempDir(), "near-limit.excalidraw")
	if _, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 23<<20 || info.Size() > maxDocsImportBytes {
		t.Fatalf("export size=%d, want near but within %d", info.Size(), maxDocsImportBytes)
	}
	if _, _, err := execRoot(t, capture.f, "docs", "import", "d2", "--file", out, "--mode", "replace"); err != nil {
		t.Fatalf("same CLI could not import its export: %v", err)
	}
	if int64(len(imported)) != info.Size() || !bytes.Equal(imported, mustReadTestFile(t, out)) {
		t.Fatalf("import request did not carry the exported file verbatim")
	}
}

func TestDocsExcalidrawExportRejectsFinalFileOverImportLimit(t *testing.T) {
	// 19 MiB is below the raw per-attachment cap but expands beyond the 25 MiB
	// final .excalidraw import limit.
	image := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xa5}, 19<<20-8)...)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/bot/docs/d1/scene":
			_, _ = io.WriteString(w, `{"elements":[{"id":"img","type":"image","fileId":"f1"}],"files":{"f1":{"attachId":"att1"}},"appState":{},"baseVersion":"BV"}`)
		case "/v1/bot/docs/d1/attachments/att1":
			_ = json.NewEncoder(w).Encode(map[string]string{"url": server.URL + "/blob", "mimeType": "image/png"})
		case "/blob":
			_, _ = w.Write(image)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	_, capture := sceneTestFactory(t, func(w http.ResponseWriter, r *http.Request) { server.Config.Handler.ServeHTTP(w, r) })
	dir := t.TempDir()
	out := filepath.Join(dir, "too-large.excalidraw")
	_, _, err := execRoot(t, capture.f, "docs", "scene", "export", "d1", "--image-format", "excalidraw", "-o", out)
	if err == nil || !strings.Contains(err.Error(), "docs import limit") {
		t.Fatalf("err=%v", err)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Fatalf("oversize export left destination behind: %v", statErr)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("oversize export left temporary files: %v", entries)
	}
}

func mustReadTestFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
