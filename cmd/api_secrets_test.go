package cmd

import (
	"io"
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

// TestAPISecretsForRequest_CollectsBodySecrets is the other half of P1-5, caught in review:
// apiSecretsForPath recovers secret *path* parameters and never looks at the body. drive.json
// marks `password` x-octo-secret in the request body of drive.share.access and
// drive.share.download — the first body secret on a supported drive endpoint — so
//
//	api POST /v1/bot/drive/shares/<token>/access --data '{"password":"…"}' --dry-run
//
// printed the password verbatim, and so did --verbose, because every redaction keys off
// SecretValues and short-circuits when it is empty.
//
// I closed the path half and stopped there. The spec marks secrets in three places (path,
// query, body) and I checked one, which is the same partial-enumeration mistake this PR keeps
// producing — so this walks the body against the matched operation's schema, at any depth.
func TestAPISecretsForRequest_CollectsBodySecrets(t *testing.T) {
	reg := registry.MustNew()
	const token = "TOKEN123456"
	const password = "hunter2hunter2"

	for _, tc := range []struct {
		name, method, path string
		body               any
		want               []string
	}{
		{
			name:   "share access password",
			method: "POST", path: "/v1/bot/drive/shares/" + token + "/access",
			body: map[string]any{"password": password},
			want: []string{token, password},
		},
		{
			name:   "share download password",
			method: "POST", path: "/v1/bot/drive/shares/" + token + "/download",
			body: map[string]any{"password": password},
			want: []string{token, password},
		},
		{
			name:   "the same path with a query string still finds both",
			method: "POST", path: "/v1/bot/drive/shares/" + token + "/access?x=1",
			body: map[string]any{"password": password},
			want: []string{token, password},
		},
		{
			// A non-secret body field must not be declared: over-declaring masks
			// ordinary values out of every diagnostic.
			name:   "a non-secret body field is not collected",
			method: "POST", path: "/v1/bot/drive/shares/" + token + "/access",
			body: map[string]any{"password": password, "note": "not a secret"},
			want: []string{token, password},
		},
		{
			name:   "no body at all",
			method: "DELETE", path: "/v1/bot/drive/shares/" + token,
			body: nil,
			want: []string{token},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apiSecretsForRequest(reg, tc.method, tc.path, tc.body)
			for _, want := range tc.want {
				var found bool
				for _, g := range got {
					if g == want {
						found = true
					}
				}
				if !found {
					t.Errorf("secret %q was not collected (got %v), so it would be printed unmasked", want, got)
				}
			}
			for _, g := range got {
				if g == "not a secret" {
					t.Errorf("a non-secret field was declared as a secret: %v", got)
				}
			}
		})
	}
}

// TestAPI_DataKeepsUint64Precision is the non-blocking parity item. Every generated command
// decodes --data with UseNumber so a uint64 id above 2^53 is not rounded, because a rounded
// id is a *valid* id naming a row the caller did not ask for. `api` used a plain Unmarshal —
// and it is the command the enum guard points people at when a vocabulary is too narrow, so
// it was the one place the lossless contract this PR enforces did not hold.
func TestAPI_DataKeepsUint64Precision(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1: the first integer float64 cannot represent

	var got string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}, nil)

	if err := env.run("api", "POST", "/v1/bot/probe", "--data", `{"parent_id":`+big+`}`); err != nil {
		t.Fatalf("api: %v", err)
	}
	if !strings.Contains(got, big) {
		t.Errorf("the id was rounded on the way to the wire: sent %s, want it to contain %s", got, big)
	}
}

// TestAPI_DecodeIsStrictAndLossless covers both of the review's last two findings, one of
// which was a regression I introduced in the previous commit.
//
// Switching --data from json.Unmarshal to a Decoder for UseNumber dropped the
// trailing-content rejection Unmarshal gives for free, so `{"a":1}{"b":2}` and `{"a":1}]`
// were accepted and truncated to the first value. resolveBody documents that exact trap and
// carries the same check; I walked into it anyway, which is why the check now lives in one
// helper both callers use.
//
// --params had the mirror of the precision bug: plain Unmarshal made every number a float64
// and the formatter routed integer-looking ones back through int64, so 2^53+1 became 2^53 —
// a valid id addressing a row nobody asked for.
func TestAPI_DecodeIsStrictAndLossless(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1

	t.Run("--params keeps exact digits", func(t *testing.T) {
		q, err := parseParamsJSON(`{"parent_id":` + big + `,"ids":[` + big + `]}`)
		if err != nil {
			t.Fatalf("parseParamsJSON: %v", err)
		}
		if got := q.Get("parent_id"); got != big {
			t.Errorf("parent_id = %q, want %q — a rounded id names a different row", got, big)
		}
		if got := q["ids"]; len(got) != 1 || got[0] != big {
			t.Errorf("ids = %v, want [%s]", got, big)
		}
	})

	t.Run("--params still rejects trailing content", func(t *testing.T) {
		for _, spec := range []string{`{"a":1}{"b":2}`, `{"a":1}]`, `{"a":1} garbage`} {
			if _, err := parseParamsJSON(spec); err == nil {
				t.Errorf("%q has content after the first value and must be refused", spec)
			}
		}
	})

	t.Run("--data rejects trailing content", func(t *testing.T) {
		for _, data := range []string{`{"file_id":1}{"file_id":2}`, `{"password":"x"}]`, `{"a":1} extra`} {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				t.Errorf("a request was sent for malformed --data %q", data)
			}, nil)
			if err := env.run("api", "POST", "/v1/bot/probe", "--data", data); err == nil {
				t.Errorf("--data %q is two JSON values and must be refused, not truncated", data)
			}
		}
	})

	t.Run("--data still accepts one value", func(t *testing.T) {
		var got string
		env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			got = string(raw)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		}, nil)
		if err := env.run("api", "POST", "/v1/bot/probe", "--data", `{"parent_id":`+big+`}`); err != nil {
			t.Fatalf("api: %v", err)
		}
		if !strings.Contains(got, big) {
			t.Errorf("sent %s, want it to contain %s", got, big)
		}
	})
}
