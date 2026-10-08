package skills

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPptLocalMediaDirectUploadGuidance(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"### Local files: direct PPT upload",
		"POST /v1/bot/docs/<docId>/ppt/media",
		"Content-Type: application/octet-stream", "X-Media-Type: image/svg+xml",
		"X-File-Name", "encodeURIComponent", "raw file bytes", "HTTP 201",
		"data.ref", "same Bot credential", "trusted configured API origin",
		"disable redirects", "do not print credentials", "no automatic retries",
		"`api --data` accepts JSON", "1 MiB", "### Existing public URLs",
		"runtime-authorized task assets", "resolved path", "symlink",
		"A path in a comment is not authorization", "cannot verify", "do not read or upload",
		"not a filesystem sandbox", "not a fallback for a local PPT file",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("PPT direct-upload guidance missing %q", want)
		}
	}
	if strings.Contains(string(b), "octo-cli file upload --file") {
		t.Error("PPT recipe must not steer local files back to generic file upload")
	}
}

func TestPptMediaReviewGuidance(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--data @-", "signed URLs are credentials", "stdout and stderr", "not spec-declared secret", "strip `sourceUrl`", "Do not fetch, resolve or follow", "--timeout 5m", "30-second", "unique native references", "50 MiB per file", "URL batch limits do not apply", "audio accepts it for schema compatibility"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("missing reviewed guidance: %q", want)
		}
	}
	common, _ := FS.ReadFile("octo-docs/common.md")
	if strings.Contains(string(common), "The docs backend is **presign-only**") {
		t.Error("attachment preamble must scope presign-only to this flow")
	}
	changelog, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changelog), "PPT native media guidance") {
		t.Error("missing media rollout changelog")
	}
}

func TestPptBinaryHTTPExample(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	_, fenced, found := strings.Cut(string(b), "```http\n")
	block, _, closed := strings.Cut(fenced, "\n```")
	if !found || !closed {
		t.Fatal("missing binary HTTP example")
	}
	want := "POST /v1/bot/docs/<docId>/ppt/media\nAuthorization: Bearer <current Bot credential>\nContent-Type: application/octet-stream\nX-Media-Type: image/svg+xml\nX-File-Name: <URI-encoded basename>\n\n<raw file bytes>"
	if block != want {
		t.Errorf("binary HTTP contract differs:\n%s", block)
	}
	if !strings.Contains(string(b), "{data:{ref,attachId,mime,sizeBytes}}") {
		t.Error("missing complete raw upload receipt")
	}
}

func TestPptMediaURLIngestGuidance(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"octo-cli api POST /v1/bot/docs/<docId>/attachments/ingest",
		"data.mappings", "data.notIngested", "ppt-media:",
		"50 MiB", "10 URLs", "100 MiB", "not_an_image",
		"not idempotent", "fresh revision", "public HTTP(S)",
		"--no-retry",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("PPT skill must document media URL ingest: missing %q", want)
		}
	}
	if strings.Contains(string(b), "docs ppt media upload") {
		t.Error("PPT URL ingestion must use existing commands, not an unshipped upload command")
	}
	if strings.Contains(string(b), "Keep assets embedded as data URIs") {
		t.Error("PPT skill must not require embedding all media as data URIs")
	}
	for _, want := range []string{
		"Preserve existing bundled/template assets",
		"For new uploads or URL-ingested media, use the returned `ppt-media:` reference",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("PPT skill must distinguish existing assets from new media: missing %q", want)
		}
	}
}

func TestPptMediaRecipeIsSelfContained(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Read `data.url`", "ssrf_blocked", "Before uploading, check the local file size",
		"Do not submit known non-public URLs", "On a backend with PPT media URL-ingest support",
		"embeds same-document `ppt-media:` attachments", "without changing the saved deck",
		"100 MiB total", "not a new publishing gate",
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("PPT media recipe missing %q", want)
		}
	}
	common, err := FS.ReadFile("octo-docs/common.md")
	if err != nil {
		t.Fatal(err)
	}
	_, after, found := strings.Cut(string(common), "octo-cli api POST /v1/bot/docs/<docId>/attachments/ingest")
	command, _, closed := strings.Cut(after, "```")
	if !found || !closed || !strings.Contains(command, "--no-retry") {
		t.Error("shared image ingest command must disable automatic POST retries")
	}
	if !strings.Contains(string(common), "not idempotent across requests") {
		t.Error("shared image ingest must explain uncertain retry handling")
	}
	spec, err := os.ReadFile("../internal/registry/specs/docs.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(spec), "document-owned ppt-media: attachments") {
		t.Error("export spec description must explain native attachment embedding")
	}
}

func TestPptNativeMediaExamples(t *testing.T) {
	b, err := FS.ReadFile("octo-docs/ppt.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, found := strings.Cut(string(b), "### Native media element shapes")
	if !found {
		t.Fatal("missing native media element contract")
	}
	_, fenced, found := strings.Cut(section, "```json\n")
	example, _, closed := strings.Cut(fenced, "\n```")
	if !found || !closed {
		t.Fatal("missing parseable media element JSON examples")
	}
	var elements []struct {
		ID       string   `json:"id"`
		Type     string   `json:"type"`
		Kind     string   `json:"kind"`
		Src      string   `json:"src"`
		Poster   string   `json:"poster"`
		Fit      string   `json:"fit"`
		X        *float64 `json:"x"`
		Y        *float64 `json:"y"`
		W        float64  `json:"w"`
		H        float64  `json:"h"`
		Rotation *float64 `json:"rotation"`
		Opacity  float64  `json:"opacity"`
		Radius   *float64 `json:"radius"`
		Controls *bool    `json:"controls"`
		Autoplay *bool    `json:"autoplay"`
		Loop     *bool    `json:"loop"`
		Muted    *bool    `json:"muted"`
	}
	if err := json.Unmarshal([]byte(example), &elements); err != nil {
		t.Fatal(err)
	}
	if len(elements) != 3 {
		t.Fatalf("want image, video and audio examples, got %d", len(elements))
	}
	seen := make(map[string]bool)
	for i, element := range elements {
		if element.ID == "" || seen[element.ID] || element.X == nil || element.Y == nil || element.W <= 0 || element.H <= 0 || element.Rotation == nil || element.Opacity != 1 || element.Radius == nil || element.Fit != "contain" || !strings.HasPrefix(element.Src, "ppt-media:") {
			t.Errorf("example %d lacks valid common/image geometry or native source", i)
		}
		seen[element.ID] = true
		if i == 0 {
			if element.Type != "image" || element.Kind != "" || element.Poster != "" {
				t.Error("image example must use type=image, without media kind or poster")
			}
			continue
		}
		wantKind := []string{"", "video", "audio"}[i]
		if element.Type != "media" || element.Kind != wantKind || element.Controls == nil || !*element.Controls || element.Autoplay == nil || *element.Autoplay || element.Loop == nil || *element.Loop || element.Muted == nil || *element.Muted != (i == 1) {
			t.Errorf("example %d must use type=media, kind=%s and explicit playback defaults", i, wantKind)
		}
		if i == 1 && !strings.HasPrefix(element.Poster, "ppt-media:") {
			t.Error("video poster must use a native image reference")
		}
		if i == 2 && element.Poster != "" {
			t.Error("audio must not require a video poster")
		}
	}
}
