package cmd

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
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

// TestShareRealRun_AlsoMasksTheToken is round-14's consistency item. Round 13 masked the
// token in the --dry-run envelope on the stated grounds that the two justifications
// ("mask it, a leaked description hands the share over" and "print it, the caller supplied
// it") cannot both be right. The success envelopes were the surviving instance of the side
// that argument rejected — which left the CLI in the state where its dry-run output was
// safe to paste and its real output was not.
//
// Consequence recorded rather than discovered: a script reading .data.share_url out of
// `share access` now gets the mask. It supplied the link, so it already has the value; the
// output is no longer a place to recover it from.
func TestShareRealRun_AlsoMasksTheToken(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"

	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"file_id":"7","file_name":"a.txt","file_size":"3","permission":"download"}}`))
	}, nil)
	link := env.api.URL + blobSharePathPrefix + token

	if err := env.run("drive", "share", "access", link); err != nil {
		t.Fatalf("share access: %v", err)
	}
	out := env.tf.Out.String()
	if strings.Contains(out, token) {
		t.Errorf("the share token appears in plaintext in the success envelope:\n%s", out)
	}
	if !strings.Contains(out, "share_url") {
		t.Errorf("share_url was dropped rather than masked:\n%s", out)
	}
}

// --- document invite links ---
//
// These live beside the share-link tests rather than in a file of their own
// because they exercise the SAME boundary: `docs invite accept` reuses
// assertSameOrigin and assertShareIDSegment (cmd/docs_inviteurl.go), so a
// hardening applied to one surface must be observably true of both. Splitting
// them would have left two half-lists that drift.

// TestParseInviteURL_Accepts covers the shapes a person can actually paste: the
// whole link the web app shows, absolute or site-relative.
func TestParseInviteURL_Accepts(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "https://octo.example.com"}
	const token = "Ab3cDeFgHiJkLmN0pQrS"
	for _, tc := range []struct{ name, in string }{
		{"absolute link", "https://octo.example.com" + docsInvitePathPrefix + token},
		{"relative link", docsInvitePathPrefix + token},
		{"link with surrounding whitespace", "  https://octo.example.com" + docsInvitePathPrefix + token + "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseInviteURL(cfg, strings.TrimSpace(tc.in))
			if err != nil {
				t.Fatalf("parseInviteURL(%q): %v", tc.in, err)
			}
			if got != token {
				t.Errorf("token: got %q, want %q", got, token)
			}
		})
	}
}

// TestParseInviteURL_Rejects is the SSRF test for the invite surface. An invite
// link arrives from another person, so it is untrusted: the CLI must refuse
// anything that is not an invite path on the configured origin, and must never
// be in a position to fetch an attacker-chosen host. The case list mirrors
// TestParseShareURL_Rejects deliberately — the same rules, asserted on the same
// inputs, so neither surface can quietly enforce less than the other.
func TestParseInviteURL_Rejects(t *testing.T) {
	cfg := &config.Config{APIBaseURL: "https://octo.example.com"}
	for _, tc := range []struct{ name, in string }{
		{"foreign host", "https://evil.example.com/docs/invite/tok-1"},
		{"host that merely shares a suffix", "https://notocto.example.com/docs/invite/tok-1"},
		{"host with an added port", "https://octo.example.com:8443/docs/invite/tok-1"},
		{"embedded credentials", "https://user:pw@octo.example.com/docs/invite/tok-1"},
		{"downgraded scheme", "http://octo.example.com/docs/invite/tok-1"},
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://octo.example.com/docs/invite/tok-1"},
		{"encoded slash smuggling a segment", "https://octo.example.com/docs/invite/tok%2F..%2Fadmin"},
		{"extra path segment", "https://octo.example.com/docs/invite/tok-1/extra"},
		{"empty token", "https://octo.example.com/docs/invite/"},
		{"dot segment as the token", "https://octo.example.com/docs/invite/.."},
		{"the share path, not an invite path", "https://octo.example.com/drive/s/tok-1"},
		{"unrelated path", "https://octo.example.com/v1/bot/docs/invites"},
		{"empty input", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseInviteURL(cfg, tc.in)
			if err == nil {
				t.Fatalf("parseInviteURL(%q) should have failed, got %q", tc.in, got)
			}
			if err.Code != "INVALID_INVITE_URL" {
				t.Errorf("code: got %q, want INVALID_INVITE_URL (%s)", err.Code, err.Message)
			}
			if err.ExitCode() != 2 {
				t.Errorf("exit code: got %d, want 2", err.ExitCode())
			}
		})
	}
}

// TestParseInviteURL_NeverEchoesTheToken is the invite-side counterpart of
// TestParseShareURL_ParseFailureDoesNotEchoTheLink. The whole argument is the
// link and its last segment is the invite token, so every rejection path — not
// just the url.Parse wrapper — has to name the mistake without quoting the value.
func TestParseInviteURL_NeverEchoesTheToken(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"
	cfg := &config.Config{APIBaseURL: "https://octo.example.com"}

	for _, tc := range []struct{ name, raw string }{
		{"control character in the link", "https://octo.example.com/docs/invite/" + token + "\x7f"},
		{"invalid percent escape", "https://octo.example.com/docs/invite/" + token + "%zz"},
		{"malformed IPv6 authority", "https://[::1/docs/invite/" + token},
		{"foreign host", "https://evil.example.com/docs/invite/" + token},
		{"embedded credentials", "https://user:pw@octo.example.com/docs/invite/" + token},
		{"wrong path shape", "https://octo.example.com/nope/" + token},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseInviteURL(cfg, tc.raw)
			if err == nil {
				t.Fatal("a malformed or foreign invite link must be refused")
			}
			visible := err.Message + " " + err.Hint + " " + string(err.Detail)
			if strings.Contains(visible, token) {
				t.Errorf("the invite token reached the error envelope: %s", visible)
			}
			if err.Message == "" {
				t.Error("stripping the input must not strip the reason")
			}
		})
	}
}

// TestInviteTokenFromValue_BareTokenPassesThrough pins the other half of the
// contract: a bare token is NOT a link and must be forwarded untouched, without
// even consulting the configured origin. base64url tokens contain "-", "_" and
// "." and must all survive; the one character that forces link parsing is "/",
// which the token charset excludes.
func TestInviteTokenFromValue_BareTokenPassesThrough(t *testing.T) {
	// No config is set on this factory: reaching webOrigin would fail, which is
	// what proves the bare-token path never gets there.
	f := cmdutil.NewTestFactory()
	for _, token := range []string{
		"Ab3cDeFgHiJkLmN0pQrS",
		"-leading-dash-token",
		"tok_with.dots-and_underscores",
	} {
		t.Run(token, func(t *testing.T) {
			got, err := inviteTokenFromValue(f.Factory, token)
			if err != nil {
				t.Fatalf("a bare token must pass through: %v", err)
			}
			if got != token {
				t.Errorf("token: got %q, want %q unchanged", got, token)
			}
		})
	}
}

// TestDocsInviteAccept_TakesALinkOrAToken drives the real command tree end to
// end: both spellings must produce the same request, and neither must send the
// token anywhere except the configured Octo API. A link on another host must
// fail locally with no request at all — that is the property the whole parser
// exists for.
func TestDocsInviteAccept_TakesALinkOrAToken(t *testing.T) {
	const token = "Ab3cDeFgHiJkLmN0pQrS"

	t.Run("whole invite link", func(t *testing.T) {
		var gotPath string
		tf, srv := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"docId":"d1","documentName":"Plan","role":"writer"}`))
		})
		link := srv.URL + docsInvitePathPrefix + token
		if _, _, err := execRoot(t, tf, "docs", "invite", "accept", link); err != nil {
			t.Fatalf("accept: %v", err)
		}
		if want := "/v1/bot/docs/invites/" + token + "/accept"; gotPath != want {
			t.Errorf("path: got %q, want %q", gotPath, want)
		}
	})

	t.Run("bare token", func(t *testing.T) {
		var gotPath string
		tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"docId":"d1","documentName":"Plan","role":"writer"}`))
		})
		if _, _, err := execRoot(t, tf, "docs", "invite", "accept", token); err != nil {
			t.Fatalf("accept: %v", err)
		}
		if want := "/v1/bot/docs/invites/" + token + "/accept"; gotPath != want {
			t.Errorf("path: got %q, want %q", gotPath, want)
		}
	})

	t.Run("link via the --invite-token flag", func(t *testing.T) {
		var gotPath string
		tf, srv := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"docId":"d1","documentName":"Plan","role":"writer"}`))
		})
		link := srv.URL + docsInvitePathPrefix + token
		if _, _, err := execRoot(t, tf, "docs", "invite", "accept", "--invite-token", link); err != nil {
			t.Fatalf("accept: %v", err)
		}
		if want := "/v1/bot/docs/invites/" + token + "/accept"; gotPath != want {
			t.Errorf("path: got %q, want %q", gotPath, want)
		}
	})

	t.Run("link on another host is refused without a request", func(t *testing.T) {
		var called bool
		tf, _ := importTestRoot(t, func(w http.ResponseWriter, r *http.Request) { called = true })
		_, stderr, err := execRoot(t, tf, "docs", "invite", "accept",
			"https://evil.example.com"+docsInvitePathPrefix+token)
		if err == nil {
			t.Fatal("a link on a foreign origin must be refused")
		}
		if called {
			t.Error("the CLI must not contact anything for a rejected link")
		}
		if !strings.Contains(stderr, "INVALID_INVITE_URL") {
			t.Errorf("stderr should carry INVALID_INVITE_URL:\n%s", stderr)
		}
		if strings.Contains(stderr, token) {
			t.Errorf("the invite token appears in the error envelope:\n%s", stderr)
		}
	})
}

// TestDocsInviteAcceptURL_WrapperIsInstalledOnTheSpecLeaf pins the wiring
// itself, which every test above takes for granted.
//
// registerDocsInviteAcceptURL finds its target by walking docs → invite → accept
// and used to return quietly when any step came up empty. That made link support
// silently optional: rename the operationId, regroup the leaf, or drop the
// x-octo-flag, and the wrapper would stop attaching with nothing failing. The
// user-visible result is not "links are unsupported" but something worse — the
// whole URL goes to the backend as if it were a token, and comes back 410.
//
// So this asserts the three facts that together mean the wrapper is live on the
// command a user actually reaches: the spec declares the operation, the leaf
// exists with the string flag the wrapper rewrites, and the leaf's help carries
// the text only the wrapper appends. Any degradation that detaches the wrapper
// fails here rather than in production.
func TestDocsInviteAcceptURL_WrapperIsInstalledOnTheSpecLeaf(t *testing.T) {
	tf := newTestFactoryWithReg()
	tf.SetConfig(&config.Config{APIBaseURL: "https://octo.example.com", BotToken: "app_test", Format: "json"})

	reg := tf.Factory.Registry()
	if reg == nil {
		t.Fatal("test factory has no registry")
	}
	if _, ok := reg.GetOperation(docsInviteAcceptOp); !ok {
		t.Fatalf("the embedded spec no longer declares %s. If it was renamed, re-point "+
			"cmd/docs_inviteurl.go at the new id: otherwise the invite-link wrapper attaches to nothing.",
			docsInviteAcceptOp)
	}

	root := NewRootCmd(tf.Factory)
	accept := docsInviteAcceptLeaf(root)
	if accept == nil {
		t.Fatalf("%s is declared but `docs invite accept` is not in the command tree", docsInviteAcceptOp)
	}
	flag := accept.Flags().Lookup(docsInviteTokenFlag)
	if flag == nil {
		t.Fatalf("`docs invite accept` has no --%s flag; the wrapper rewrites that slot", docsInviteTokenFlag)
	}
	if got := flag.Value.Type(); got != "string" {
		t.Fatalf("--%s is a %s flag, not string; GetString would fail and the wrapper would refuse every value",
			docsInviteTokenFlag, got)
	}
	if !strings.Contains(accept.Long, docsInviteLinkHelp) {
		t.Errorf("`docs invite accept` help does not carry the invite-link paragraph, so the wrapper "+
			"is not installed on this leaf; a pasted link would be sent to the backend verbatim.\nLong:\n%s",
			accept.Long)
	}
}

// TestRegisterDocsInviteAcceptURL_PanicsWhenTheLeafIsGone is the other half of
// the invariant: when the spec still declares the operation but the tree does not
// carry the leaf, registration must fail loudly instead of skipping. The panic is
// the same policy registry.MustNew applies to embedded data
// (internal/registry/loader.go:96) — unreachable from user input or the network,
// reachable only by a change in this repo, and hit by every test that builds a
// root command.
func TestRegisterDocsInviteAcceptURL_PanicsWhenTheLeafIsGone(t *testing.T) {
	tf := newTestFactoryWithReg()
	// A root with no service commands at all: the operation is still declared by
	// the embedded spec, so the leaf is missing rather than withdrawn.
	bare := &cobra.Command{Use: "octo-cli"}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("registration silently skipped a missing accept leaf; link support would vanish unnoticed")
		}
		if !strings.Contains(fmt.Sprint(r), docsInviteAcceptOp) {
			t.Errorf("the panic must name the operation that went missing; got %v", r)
		}
	}()
	registerDocsInviteAcceptURL(bare, tf.Factory)
}

// TestNormalizeInviteTokenArg_FailsClosedOnAnUnreadableFlag pins the deny side of
// the value check.
//
// This branch used to `return nil` — treating "I cannot read the value" as "the
// value is fine" — which is fail-open on the one code path whose entire job is to
// decide whether a caller-supplied string may be forwarded. If it ever fires for
// real, the value is an unvalidated string that may be a whole URL pointed at
// someone else's host. The only safe answer is to refuse.
func TestNormalizeInviteTokenArg_FailsClosedOnAnUnreadableFlag(t *testing.T) {
	tf := newTestFactoryWithReg()
	tf.SetConfig(&config.Config{APIBaseURL: "https://octo.example.com", BotToken: "app_test", Format: "json"})

	// A leaf whose invite-token flag is not a string: GetString on it errors,
	// which is the condition the old code let through.
	cmd := &cobra.Command{Use: "accept"}
	cmd.Flags().Int(docsInviteTokenFlag, 0, "wrong type on purpose")
	if err := cmd.Flags().Set(docsInviteTokenFlag, "7"); err != nil {
		t.Fatal(err)
	}

	err := normalizeInviteTokenArg(cmd, tf.Factory, nil)
	if err == nil {
		t.Fatal("an unreadable --invite-token must be refused, not forwarded unchecked")
	}
	if err.Code != "INVALID_INVITE_URL" {
		t.Errorf("code = %q, want INVALID_INVITE_URL", err.Code)
	}
	if err.ExitCode() != 2 {
		t.Errorf("exit code = %d, want 2", err.ExitCode())
	}
}
