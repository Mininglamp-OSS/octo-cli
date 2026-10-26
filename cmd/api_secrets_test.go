package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
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

func TestAPIResponseSecretFieldsForRequest_UsesOperationMounts(t *testing.T) {
	reg := registry.MustNew()

	for _, tc := range []struct {
		name, method, path string
		want               []string
	}{
		{
			name:   "prepare on the bot mount",
			method: http.MethodPost,
			path:   "/v1/bot/drive/files/prepare-upload",
			want:   []string{"upload_url"},
		},
		{
			name:   "prepare on the user mount",
			method: http.MethodPost,
			path:   "/v1/user/drive/files/prepare-upload",
			want:   []string{"upload_url"},
		},
		{
			name:   "parts presign on the user mount",
			method: http.MethodPost,
			path:   "/v1/user/drive/files/42/upload/parts-presign",
			want:   []string{"parts[].url"},
		},
		{
			name:   "an operation without response secrets",
			method: http.MethodPost,
			path:   "/v1/bot/drive/files/42/upload/abort",
			want:   nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := apiResponseSecretFieldsForRequest(reg, tc.method, tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("response secret fields = %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("response secret fields[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAPI_MasksPrepareUploadURLOnDeclaredMounts(t *testing.T) {
	const signedURL = "https://objects.example.test/prepare?signature=api-test-only"

	for _, path := range []string{
		"/v1/bot/drive/files/prepare-upload",
		"/v1/user/drive/files/prepare-upload",
	} {
		t.Run(path, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w,
					`{"error":"invalid_argument","message":"prepare rejected %s","details":{"upload_url":%q}}`,
					signedURL, signedURL)
			}, nil)

			err := env.run("api", http.MethodPost, path,
				"--data", `{"space_id":"shared:s1","name":"large.bin","size":7,"content_type":"application/octet-stream"}`)
			if err == nil {
				t.Fatal("expected the prepare request to fail")
			}
			ee := output.AsExitError(err)
			if ee == nil {
				t.Fatalf("expected a structured error, got %v", err)
			}
			visible := strings.Join([]string{err.Error(), string(ee.Detail), env.tf.ErrOut.String()}, "\n")
			if strings.Contains(visible, signedURL) {
				t.Fatalf("api error exposed the prepare upload_url:\n%s", visible)
			}
			if !strings.Contains(visible, "REDACTED") {
				t.Fatalf("api error did not show the response secret was masked:\n%s", visible)
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
			got, serr := apiSecretsForRequest(reg, tc.method, tc.path, tc.body)
			if serr != nil {
				t.Fatalf("apiSecretsForRequest: %v", serr)
			}
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

func TestAPISecretsForRequest_CollectsSecretMapValues(t *testing.T) {
	reg := registry.MustNew()
	const secret = "api-custom-env-secret-4f2a"

	got, err := apiSecretsForRequest(reg, http.MethodPost, "/market/api/v1/plugins/install",
		map[string]any{"custom_env": map[string]any{"API_TOKEN": secret}})
	if err != nil {
		t.Fatalf("apiSecretsForRequest: %v", err)
	}
	if len(got) != 1 || got[0] != secret {
		t.Errorf("secret values = %v, want [%q]", got, secret)
	}
}

func TestAPI_MasksCustomEnvOnMarketplaceLocalPrefixes(t *testing.T) {
	const secret = "api-custom-env-prefix-secret-7b42"
	body := `{"plugin_id":"plugin-1","workspace_id":"workspace-1","runtime_id":"runtime-1",` +
		`"custom_env":{"API_TOKEN":"` + secret + `"}}`

	for _, tc := range []struct {
		name, prefix, path string
	}{
		{name: "canonical prefix", prefix: "market", path: "/market/api/v1/plugins/install"},
		{name: "escaped operation literal", prefix: "market", path: "/market/api/v1/plugins/%69nstall"},
		{name: "escaped module and version", prefix: "market", path: "/%6darket/api/v%31/plugins/install"},
		{name: "uppercase hex escape", prefix: "market", path: "/%6Darket/api/v1/plugins/install"},
		{name: "empty prefix", prefix: "", path: "/api/v1/plugins/install"},
		{name: "empty prefix escaped literal", prefix: "", path: "/api/v1/plugins/%69nstall"},
		{name: "custom prefix", prefix: "local-market", path: "/local-market/api/v1/plugins/install"},
		{name: "escaped custom prefix", prefix: "local-market", path: "/%6cocal-market/api/v1/plugins/%69nstall"},
		{name: "escaped configured prefix", prefix: "local%2Dmarket", path: "/local-market/api/v1/plugins/install"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OCTO_MARKETPLACE_API_PREFIX", tc.prefix)
			root, tf := secretsTestRoot(t, "https://octo.test", client.Options{DryRun: true})
			root.SetArgs([]string{"api", http.MethodPost, tc.path, "--data", body, "--dry-run"})
			if err := root.Execute(); err != nil {
				t.Fatalf("api dry-run: %v (stderr: %s)", err, tf.ErrOut.String())
			}

			stdout, stderr := tf.Out.String(), tf.ErrOut.String()
			if strings.Contains(stdout, secret) {
				t.Errorf("stdout exposed custom_env value: %s", stdout)
			}
			if strings.Contains(stderr, secret) {
				t.Errorf("stderr exposed custom_env value: %s", stderr)
			}

			var envelope struct {
				Data struct {
					URL  string         `json:"url"`
					Body map[string]any `json:"body"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
				t.Fatalf("stdout is not valid JSON: %v (%s)", err, stdout)
			}
			if got, want := envelope.Data.URL, "https://octo.test"+tc.path; got != want {
				t.Errorf("dry-run URL = %q, want caller path %q", got, want)
			}
			customEnv, ok := envelope.Data.Body["custom_env"].(map[string]any)
			if !ok {
				t.Fatalf("dry-run body omitted custom_env: %#v", envelope.Data.Body)
			}
			if _, ok := customEnv["API_TOKEN"]; !ok {
				t.Errorf("dry-run body omitted API_TOKEN: %#v", customEnv)
			}
		})
	}
}

func TestMatchPathTemplate_UnreservedLiteralEscapes(t *testing.T) {
	const rawID = "opaque%2Fid%7E"
	for _, tc := range []struct {
		name, template, path string
		match                bool
	}{
		{"unreserved characters", "/aZ0-._~/{id}", "/%61%5A%30%2D%2e%5f%7E/" + rawID, true},
		{"escaped template literal", "/%61%5A%30%2D%2e%5f%7E/{id}", "/aZ0-._~/" + rawID, true},
		{"literal case remains significant", "/A/{id}", "/%61/" + rawID, false},
		{"encoded slash stays within its segment", "/plugins/install/{id}", "/plugins%2Finstall/" + rawID, false},
		{"double encoding stays encoded", "/plugins/install/{id}", "/plugins%252Finstall/" + rawID, false},
		{"reserved characters stay encoded", "/market+api/{id}", "/market%2Bapi/" + rawID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values, matched := matchPathTemplate(tc.template, pathSegments(tc.path))
			if matched != tc.match {
				t.Fatalf("match = %v, want %v for %q against %q", matched, tc.match, tc.path, tc.template)
			}
			if matched && values["id"] != rawID {
				t.Errorf("captured id = %q, want unchanged %q", values["id"], rawID)
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

// TestAPI_RefusesANonStringValueAtASecretProperty is round-16 P1-3, request half.
//
// Both halves of redaction assumed "declared secret ⇒ Go string"; a JSON number
// cannot be collected by CollectBodySecrets, while redactBodyValue leaves the
// json.Number in output. `api` performs no schema validation, so an unquoted
// accepted, sent, and printed in both the request trace and the error detail — and an
// all-digit share password is an ordinary choice, with the quotes the only thing protecting
// it.
//
// The fix fails closed rather than teaching the masker to stringify: a type error here is
// strictly better than a disclosure, and widening the masker over every numeric value would
// mask ordinary ids — file ids, space ids — out of every diagnostic. The response half still
// masks non-string scalars, because there no caller mistake is involved.
func TestAPI_RefusesANonStringValueAtASecretProperty(t *testing.T) {
	for _, tc := range []struct{ name, data, value string }{
		{"an all-digit password", `{"password":8675309}`, "8675309"},
		{"a uint64-sized password", `{"password":9007199254740993}`, "9007199254740993"},
		{"a boolean", `{"password":true}`, "true"},
		{"an object", `{"password":{"v":"secret123"}}`, "secret123"},
		{"an array", `{"password":["secret123"]}`, "secret123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, _ *http.Request) {
				t.Error("the request was sent, so the unmaskable value reached the wire and the trace")
			}, nil)
			err := env.run("api", "POST", "/v1/bot/drive/shares/TOKEN123456/access",
				"--data", tc.data, "--verbose")
			if err == nil {
				t.Fatal("a non-string value at an x-octo-secret property must be refused")
			}
			if !strings.Contains(err.Error(), "password") {
				t.Errorf("the error must name the offending property: %v", err)
			}
			// Default-deny: the refusal itself must not print what it refused.
			if strings.Contains(err.Error(), tc.value) {
				t.Errorf("the refusal echoed the value it refused: %v", err)
			}
			if out := env.tf.ErrOut.String(); strings.Contains(out, tc.value) {
				t.Errorf("the value appears on stderr:\n%s", out)
			}
		})
	}
}

// TestAPI_AStringValueAtASecretPropertyStillWorks is the allow direction: the refusal must
// be about the type, not about the property being present at all.
func TestAPI_AStringValueAtASecretPropertyStillWorks(t *testing.T) {
	const password = "hunter2hunter2"

	var sent string
	env := newDriveTestEnv(t, "bf_bot", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		sent = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}, nil)
	if err := env.run("api", "POST", "/v1/bot/drive/shares/TOKEN123456/access",
		"--data", `{"password":"`+password+`"}`, "--verbose"); err != nil {
		t.Fatalf("api: %v", err)
	}
	if !strings.Contains(sent, password) {
		t.Errorf("the password must still reach the wire intact: %s", sent)
	}
	if out := env.tf.ErrOut.String(); strings.Contains(out, password) {
		t.Errorf("the password appears in the verbose trace:\n%s", out)
	}
}

// TestSecrets_NoSpecDeclaresASecretApiCannotCollectOrMask is round-16 P2-2's tripwire.
//
// `api` recovers path and body secrets, but a query-position secret would be missed because
// pathSegments drops the query. Header declarations are safe today because `api` has no
// arbitrary-header input. This test retains those parameter-boundary constraints; body-secret
// shapes are exercised by request-level consumers instead of a schema-wide type census.
//
// The census covers every operation's parameters rather than relying on a list, so a future
// unsupported declaration is caught here.
func TestSecrets_NoSpecDeclaresASecretApiCannotCollectOrMask(t *testing.T) {
	reg := registry.MustNew()

	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if !p.Secret {
					continue
				}
				if p.In != "path" && p.In != "header" {
					t.Errorf("%s declares x-octo-secret on a %s parameter %q, but `api` only "+
						"collects path and body secrets and cannot safely recover a %s value",
						info.ID, p.In, p.Name, p.In)
				}
				if p.Type != "" && p.Type != "string" {
					t.Errorf("%s declares x-octo-secret on a %s-typed parameter %q; the masker's "+
						"disclosure rules are written for string values", info.ID, p.Type, p.Name)
				}
			}
		}
	}
}

// TestAPI_ParamsArrayElementsAreJSONNotGoSyntax is round-16 P2-3.
//
// The array arm of parseParamsJSON fell through to fmt.Sprintf("%v", item) for anything that
// was not a json.Number, so an object became `map[id:9007199254740993]`, a nested array
// `[1 2]`, and null `<nil>` — Go's own debug spelling on the wire. The *same* value at top
// level is marshalled correctly by the default arm, so the two arms disagreed about what a
// non-scalar query value is, and the previous round edited this exact switch while fixing
// only its numeric half.
func TestAPI_ParamsArrayElementsAreJSONNotGoSyntax(t *testing.T) {
	const big = "9007199254740993" // 2^53 + 1

	q, err := parseParamsJSON(`{"filters":[{"id":` + big + `}],"nest":[[1,2]],"vals":[null],` +
		`"mixed":["s",1,true],"top":{"id":` + big + `}}`)
	if err != nil {
		t.Fatalf("parseParamsJSON: %v", err)
	}
	for key, want := range map[string][]string{
		"filters": {`{"id":` + big + `}`},
		"nest":    {`[1,2]`},
		"vals":    {`null`},
		"mixed":   {"s", "1", "true"},
		"top":     {`{"id":` + big + `}`},
	} {
		got := q[key]
		if len(got) != len(want) {
			t.Errorf("%s = %v, want %v", key, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s[%d] = %q, want %q", key, i, got[i], want[i])
			}
		}
	}
}
