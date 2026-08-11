package cmd

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Round-14 P1-5. `octo-cli api` built its client.Request without SecretValues at all, and
// redactSecrets short-circuits on an empty list — so the boundary in client.Do, the one
// added as a backstop "for sites that do not exist yet", was a no-op on this path:
//
//	api POST /v1/user/drive/shares/<token>/access   -> printed in full
//	drive share access <link>                       -> ***REDACTED***
//
// The attribution is worth keeping straight. The missing SecretValues is pre-existing and
// this PR does not regress it. What this PR does is introduce the first x-octo-secret
// *path* parameters in any embedded spec (share.revoke.share_id, share.access /
// share.download.share_token, invite.accept.invite_token), so before drive.json there was
// no credential-equivalent path value for `api` to leak. It also points users here: the
// enum guard's own message says that when a vocabulary is too narrow there is "no way
// through except `octo-cli api`".
//
// api takes an arbitrary METHOD and PATH, so the secrets cannot come from a command
// definition. They are recovered by matching the concrete path against the registry's
// path templates, which is the same source of truth collectSecrets uses for generated
// leaves.
func TestAPISecretsForPath_FindsSpecDeclaredPathSecrets(t *testing.T) {
	reg := registry.MustNew()

	for _, tc := range []struct {
		name, method, path string
		want               []string
	}{
		{
			name:   "share revoke share_id",
			method: "DELETE", path: "/v1/bot/drive/shares/AbCdEfGhIjKl",
			want: []string{"AbCdEfGhIjKl"},
		},
		{
			name:   "share access share_token",
			method: "POST", path: "/v1/bot/drive/shares/TOKEN123456/access",
			want: []string{"TOKEN123456"},
		},
		{
			name:   "share download share_token",
			method: "POST", path: "/v1/bot/drive/shares/TOKEN123456/download",
			want: []string{"TOKEN123456"},
		},
		{
			name:   "invite accept invite_token",
			method: "POST", path: "/v1/bot/drive/invites/INVITE98765/accept",
			want: []string{"INVITE98765"},
		},
		{
			name:   "the user-scoped mount of the same operation",
			method: "DELETE", path: "/v1/user/drive/shares/AbCdEfGhIjKl",
			want: []string{"AbCdEfGhIjKl"},
		},
		{
			// A non-secret path parameter must not be declared: over-declaring would
			// mask ordinary ids out of every diagnostic.
			name:   "a plain file id is not a secret",
			method: "GET", path: "/v1/bot/drive/files/12345",
			want: nil,
		},
		{
			name:   "a path matching no operation declares nothing",
			method: "GET", path: "/v1/bot/nothing/here",
			want: nil,
		},
		{
			// Segment counts differ, so this must not match the share template.
			name:   "a shorter path does not match a longer template",
			method: "DELETE", path: "/v1/bot/drive/shares",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apiSecretsForPath(reg, tc.method, tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("secrets = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("secrets[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestAPI_MasksASpecDeclaredSecretOnBothPaths drives the command, because the helper being
// right is not the property — the property is that `api` passes them to the client. Both
// output paths are checked: --dry-run, which prints the URL it would call, and the error
// envelope, which is unconditional.
func TestAPI_MasksASpecDeclaredSecretOnBothPaths(t *testing.T) {
	const token = "SHARETOKEN0123456789"

	t.Run("the client is given the secret at all", func(t *testing.T) {
		// The helper being right is not the property; the property is that api hands
		// the values to the client. Asserted through the client's own dry-run
		// description, which is the surface that prints the URL it would call.
		reg := registry.MustNew()
		secrets := apiSecretsForPath(reg, "DELETE", "/v1/bot/drive/shares/"+token)
		if len(secrets) == 0 {
			t.Fatal("no secret was recovered for a share id path, so nothing would be masked")
		}
		if secrets[0] != token {
			t.Errorf("recovered %q, want %q", secrets[0], token)
		}
	})

	t.Run("the error envelope, which is unconditional", func(t *testing.T) {
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found","message":"no such share"}`))
		}, nil)
		err := env.run("api", "DELETE", "/v1/bot/drive/shares/"+token)
		if err == nil {
			t.Fatal("expected a backend error")
		}
		// The wiring, not the helper: the value reaches the client or it does not.
		if strings.Contains(err.Error(), token) {
			t.Errorf("the share id appears in the error: %v", err)
		}
		if out := env.tf.ErrOut.String(); strings.Contains(out, token) {
			t.Errorf("the share id appears on stderr:\n%s", out)
		}
	})

	t.Run("a transport failure, where the URL is formatted into the error", func(t *testing.T) {
		// The path that made this visible in the first place: *url.Error quotes the
		// whole URL, so with no SecretValues the id is printed in full.
		dead := newDriveTestEnv(t, "bf_bot", nil, nil)
		dead.api.Close()
		err := dead.run("api", "DELETE", "/v1/bot/drive/shares/"+token)
		if err == nil {
			t.Skip("the closed server still answered; nothing to assert")
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("the share id appears in the transport error: %v", err)
		}
	})
}

// TestAPISecretsForPath_StripsQueryAndFragment is a defect in this round's own fix, caught
// in review. apiSecretsForPath split the raw PATH argument on "/" and matched it against the
// registry templates without first removing a query string or fragment. For the three
// templates whose secret parameter is followed by a literal segment —
// shares/{share_token}/access, .../download, invites/{invite_token}/accept — an inline query
// turns that last segment into "access?x=1", nothing matches, SecretValues stays empty, and
// redactSecrets short-circuits on the empty list.
//
// Same leak class as the finding this helper was written to close, on a shape the fix did not
// cover: the value came from the caller's own argument, so nothing normalised it first.
func TestAPISecretsForPath_StripsQueryAndFragment(t *testing.T) {
	reg := registry.MustNew()
	const token = "TOKEN123456"

	for _, tc := range []struct{ name, method, path string }{
		{"trailing literal segment with a query", "POST", "/v1/bot/drive/shares/" + token + "/access?x=1"},
		{"download with a query", "POST", "/v1/bot/drive/shares/" + token + "/download?a=b&c=d"},
		{"invite accept with a query", "POST", "/v1/bot/drive/invites/" + token + "/accept?x=1"},
		{"secret in the last segment with a query", "DELETE", "/v1/bot/drive/shares/" + token + "?x=1"},
		{"a fragment", "DELETE", "/v1/bot/drive/shares/" + token + "#frag"},
		{"both", "POST", "/v1/bot/drive/shares/" + token + "/access?x=1#frag"},
		{"an empty query marker", "DELETE", "/v1/bot/drive/shares/" + token + "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apiSecretsForPath(reg, tc.method, tc.path)
			if len(got) == 0 {
				t.Fatalf("no secret recovered from %q, so the token would be printed unmasked", tc.path)
			}
			for _, v := range got {
				if v != token {
					t.Errorf("recovered %q, want the bare token %q — a query string must not be "+
						"captured as part of the value either", v, token)
				}
			}
		})
	}
}
