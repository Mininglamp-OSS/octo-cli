package cmd

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
)

// Round-13 P1-b. url.Parse returns a *url.Error whose Error() quotes the entire input,
// and on `share access` / `share download` the entire input is the share link with the
// token in its path. So a malformed link printed the token into the structured error on
// stderr, unconditionally.
//
// The sibling site for presigned URLs already strips that wrapper with urlParseCause
// (cmd/drive.go); this one did not. Everything else in parseShareURL was already written
// with this in mind — the "link path is not a share link" branch deliberately names the
// two accepted shapes instead of echoing the path — which is why only this one line was
// wrong.
func TestParseShareURL_ParseFailureDoesNotEchoTheLink(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"
	cfg := &config.Config{APIBaseURL: "https://octo.example"}

	for _, tc := range []struct{ name, raw string }{
		{"control character in the link", "https://octo.example/drive/s/" + token + "\x7f"},
		{"invalid percent escape", "https://octo.example/drive/s/" + token + "%zz"},
		{"malformed IPv6 authority", "https://[::1/drive/s/" + token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseShareURL(cfg, tc.raw)
			if err == nil {
				t.Fatal("a malformed link must be refused")
			}
			visible := err.Message + " " + err.Hint + " " + string(err.Detail)
			if strings.Contains(visible, token) {
				t.Errorf("the share token reached the error envelope: %s", visible)
			}
			if err.Code != "INVALID_SHARE_URL" {
				t.Errorf("code = %q, want INVALID_SHARE_URL", err.Code)
			}
			// The diagnostic must still say what was wrong with it.
			if err.Message == "" {
				t.Error("stripping the input must not strip the reason")
			}
		})
	}
}

// TestShareDryRun_NeverPrintsTheTokenInPlaintext is round-13's share_url decision.
//
// The dry-run envelope contradicted itself: it masked the token inside "path" — on the
// stated grounds that a leaked dry-run description must not hand the share over — and
// then printed the same token verbatim in "share_url" one line below, on the stated
// grounds that the caller supplied it so nothing new is disclosed. Both cannot be right,
// and the standard for this output is that it is safe to paste into a ticket, so the
// masking side wins.
//
// The assertion is deliberately "nowhere in the envelope", not "the share_url field is
// masked": the value appears in more than one field, and a check on one field would pass
// while another leaked.
func TestShareDryRun_NeverPrintsTheTokenInPlaintext(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"

	for _, tc := range []struct {
		name    string
		argsFor func(link, outPath string) []string
	}{
		{"share access", func(link, _ string) []string {
			return []string{"drive", "share", "access", link, "--dry-run"}
		}},
		{"share download", func(link, outPath string) []string {
			return []string{"drive", "share", "download", link, "--output", outPath, "--dry-run"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var called bool
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
				called = true
			}, nil)
			// The link has to share the configured origin, which is the fake API server.
			link := env.api.URL + blobSharePathPrefix + token
			outPath := filepath.Join(t.TempDir(), "out.bin")

			if err := env.run(tc.argsFor(link, outPath)...); err != nil {
				t.Fatalf("dry run: %v", err)
			}
			if called {
				t.Error("--dry-run must not send a request")
			}
			out := env.tf.Out.String()
			if strings.Contains(out, token) {
				t.Errorf("the share token appears in plaintext in the dry-run envelope:\n%s", out)
			}
			if !strings.Contains(out, "share_url") {
				t.Errorf("share_url was dropped rather than masked:\n%s", out)
			}
		})
	}
}

// TestWebOrigin_RefusesAConfiguredPath is round-13 P2-2. webOrigin returned scheme and
// host only, silently discarding any path, so a deployment served under a path prefix got
// share links built against the wrong origin — links that look right, get handed to a
// recipient, and resolve to nothing. The setting is documented as an origin with no path,
// so saying the value is unusable beats quietly reinterpreting it.
func TestWebOrigin_RefusesAConfiguredPath(t *testing.T) {
	for _, tc := range []struct {
		name, base string
		wantErr    bool
	}{
		{"origin only", "https://octo.example", false},
		{"origin with a trailing slash", "https://octo.example/", false},
		{"origin with a port", "https://octo.example:8443", false},
		{"origin with a path prefix", "https://octo.example/octo", true},
		{"origin with a deep path", "https://octo.example/a/b", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := webOrigin(&config.Config{APIBaseURL: tc.base})
			if tc.wantErr {
				if err == nil {
					t.Fatalf("%q carries a path, which would be dropped and produce a wrong share link, got %v", tc.base, got)
				}
				if err.Code != "MISSING_API_BASE_URL" {
					t.Errorf("code = %q", err.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("%q is a valid origin: %v", tc.base, err)
			}
			if got.Path != "" {
				t.Errorf("origin = %q, want no path", got.String())
			}
		})
	}
}

// TestRedactedShareURL_MasksTheTokenPositionNotTheFirstMatch is a bug in this round's own
// fix, found in review within the hour.
//
// redactedShareURL masked with strings.Replace(canonical, token, mask, 1), which replaces
// the first occurrence *anywhere* in the URL. A token that also appears earlier — in the
// scheme or the host — had that earlier occurrence masked instead, leaving the real token
// in the path. With token "https" the output was
// "***REDACTED***://octo.example/drive/s/https", which masks nothing and looks masked.
//
// Searching for the value was the wrong shape of solution: the token's position is known
// by construction, so the masked link is rebuilt from the parsed parts instead. There is
// nothing left to search and therefore nothing to mismatch.
func TestRedactedShareURL_MasksTheTokenPositionNotTheFirstMatch(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "https://octo.example"}

	// Tokens chosen to collide with earlier parts of the URL: the scheme, the host, and
	// the path prefix itself.
	for _, token := range []string{"https", "octo", "example", "drive", "s", "Ab3cDeFgHiJk"} {
		t.Run("token="+token, func(t *testing.T) {
			parsed, err := parseShareURL(cfg, "https://octo.example"+blobSharePathPrefix+token)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got := redactedShareURL(parsed)

			// The token must not survive in the path position. Checking the last path
			// segment specifically, because that is where it lives — a whole-string
			// "contains" check would be satisfied by the scheme for token "https".
			lastSlash := strings.LastIndex(got, "/")
			if lastSlash < 0 {
				t.Fatalf("masked link has no path: %q", got)
			}
			if segment := got[lastSlash+1:]; segment != shareURLMask {
				t.Errorf("last path segment = %q, want %q — the mask landed somewhere else and the "+
					"token is still in the link: %s", segment, shareURLMask, got)
			}
		})
	}
}
