package cmd

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// testCredential is a user-API-key credential, so drive routes to /v1/user/*.
func testCredential() *credential.BotCredential {
	return &credential.BotCredential{Token: "uk_person", Source: "test"}
}

// Round-8 P1-2. Rounds 2 and 6 each fixed a site that echoed an x-octo-secret
// value into the unconditional stderr envelope; round 8 found three more. The
// pattern in those fixes was the defect: masking was installed *around the
// transport window*, so any error raised outside it echoed freely.
//
// These tests are written against the boundary rather than the three sites — an
// error that reaches the caller must not contain a value the spec marked secret,
// wherever in the command's lifecycle it was raised. A new failure mode inside
// that boundary should fail here without anyone adding a case for it.

// secretEchoCases are invocations that fail *outside* the transport window, each
// carrying a value the spec marks x-octo-secret.
var secretEchoCases = []struct {
	name    string
	args    []string
	apiBase string // "" keeps the test server
	secret  string
}{
	{
		// buildURL's parse error: OCTO_API_BASE_URL is read without URL
		// validation, so a typo'd port makes url.Parse fail with the whole URL —
		// share id included — in its text. Raised before any request.
		name:    "malformed base URL, share revoke",
		args:    []string{"drive", "share", "revoke", "--share-id", "SUPERSECRETSHAREID123"},
		apiBase: "http://octo.test:notaport",
		secret:  "SUPERSECRETSHAREID123",
	},
	{
		name:    "malformed base URL, invite accept",
		args:    []string{"drive", "invite", "accept", "--invite-token", "SUPERSECRETINVITE456"},
		apiBase: "http://octo.test:notaport",
		secret:  "SUPERSECRETINVITE456",
	},
	{
		name:    "unparseable base URL host",
		args:    []string{"drive", "share", "revoke", "--share-id", "SUPERSECRETSHAREID123"},
		apiBase: "http://[::1",
		secret:  "SUPERSECRETSHAREID123",
	},
	{
		// The flag-parse error, for exactly the case the flag alternative exists
		// to serve: a base64url id beginning with "-" typed bare. Raised by cobra
		// before collectSecrets has run, so there is nothing to mask with — the
		// value must simply not be echoed.
		name:   "leading-dash id typed bare, share revoke",
		args:   []string{"drive", "share", "revoke", "-Ab3SECRETTOKEN"},
		secret: "Ab3SECRETTOKEN",
	},
	{
		name:   "leading-dash id typed bare, invite accept",
		args:   []string{"drive", "invite", "accept", "-XySECRETINVITE"},
		secret: "XySECRETINVITE",
	},
	{
		// Both forms supplied — the belt-and-braces mistake, natural for exactly
		// the ids the spec tells you to pass by flag.
		name:   "id supplied positionally and by flag",
		args:   []string{"drive", "share", "revoke", "SUPERSECRETSHAREID123", "--share-id", "OTHER"},
		secret: "SUPERSECRETSHAREID123",
	},
	{
		// A secret-bearing argv value in the subcommand position: the natural
		// typo when the verb is omitted.
		name:   "secret in the subcommand position",
		args:   []string{"drive", "share", "SUPERSECRETSHAREID123"},
		secret: "SUPERSECRETSHAREID123",
	},
	{
		// A malformed share URL: the whole link is the argument, and the token is
		// inside it, so echoing the path echoes the token.
		name:   "malformed share URL shape",
		args:   []string{"drive", "share", "access", "/drive/s/SUPERSECRETTOKEN/extra"},
		secret: "SUPERSECRETTOKEN",
	},
}

// TestSecretEcho_NoErrorPathEchoesASecret is the boundary assertion.
func TestSecretEcho_NoErrorPathEchoesASecret(t *testing.T) {
	for _, tc := range secretEchoCases {
		t.Run(tc.name, func(t *testing.T) {
			apiBase := tc.apiBase
			var srvURL string
			if apiBase == "" {
				env := newDriveTestEnv(t, "uk_person", func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}, nil)
				srvURL = env.api.URL
				apiBase = srvURL
			}
			root, tf := secretsTestRoot(t, apiBase, client.Options{NoRetry: true})
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("expected this invocation to fail: %v", tc.args)
			}

			// Everything the caller can see: the structured envelope and both streams.
			seen := tf.Out.String() + tf.ErrOut.String()
			if ee := output.AsExitError(err); ee != nil {
				seen += ee.Message + " " + ee.Hint + " " + string(ee.Detail)
			} else {
				seen += err.Error()
			}
			if strings.Contains(seen, tc.secret) {
				t.Errorf("an error path echoed the secret %q.\nvisible output:\n%s", tc.secret, seen)
			}
		})
	}
}

// TestSecretEcho_DiagnosticsSurviveRedaction guards the other direction: the
// point is to stop echoing the value, not to stop saying what went wrong. A
// caller who mistypes an id must still be told which mistake they made.
func TestSecretEcho_DiagnosticsSurviveRedaction(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		expect string // a substring that must survive
	}{
		{"leading dash names the flag form", []string{"drive", "share", "revoke", "-Ab3SECRETTOKEN"}, "--share-id"},
		{"both forms says which mistake", []string{"drive", "share", "revoke", "X", "--share-id", "Y"}, "positionally"},
		// A path that matches neither accepted shape reaches the shape branch,
		// which must still name both shapes.
		{"unrecognised link shape names both shapes", []string{"drive", "share", "access", "/wrong/place"}, "/drive/s/"},
		// A path with the right prefix but extra segments reaches
		// assertShareIDSegment, which names the mistake without echoing the id.
		{"extra segments says which id was malformed", []string{"drive", "share", "access", "/drive/s/TOK/extra"}, "share token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDriveTestEnv(t, "uk_person", func(w http.ResponseWriter, r *http.Request) {}, nil)
			root, tf := secretsTestRoot(t, env.api.URL, client.Options{NoRetry: true})
			root.SetArgs(tc.args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("expected failure for %v", tc.args)
			}
			seen := tf.Out.String() + tf.ErrOut.String()
			if ee := output.AsExitError(err); ee != nil {
				seen += ee.Message + " " + ee.Hint
			}
			if !strings.Contains(seen, tc.expect) {
				t.Errorf("the diagnostic lost %q, so redaction went too far:\n%s", tc.expect, seen)
			}
		})
	}
}

// TestSecretEcho_ClientRedactsEveryErrorItReturns pins the boundary at the client
// rather than at the sites inside it: whatever error Do produces, a declared
// secret must not be in its text. Driven directly so a failure mode with no CLI
// surface is still covered.
func TestSecretEcho_ClientRedactsEveryErrorItReturns(t *testing.T) {
	const secret = "SUPERSECRETSHAREID123"
	cases := []struct {
		name    string
		apiBase string
		req     client.Request
	}{
		{
			name:    "buildURL parse failure",
			apiBase: "http://octo.test:notaport",
			req: client.Request{Method: http.MethodDelete,
				Path: "/v1/user/drive/shares/" + secret, SecretValues: []string{secret}},
		},
		{
			name:    "unparseable authority",
			apiBase: "http://[::1",
			req: client.Request{Method: http.MethodDelete,
				Path: "/v1/user/drive/shares/" + secret, SecretValues: []string{secret}},
		},
		{
			name:    "no base URL configured",
			apiBase: "",
			req: client.Request{Method: http.MethodDelete,
				Path: "/v1/user/drive/shares/" + secret, SecretValues: []string{secret}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := client.New(&config.Config{APIBaseURL: tc.apiBase},
				testCredential(), client.Options{NoRetry: true})
			_, err := c.Do(context.Background(), &tc.req)
			if err == nil {
				t.Fatal("expected an error")
			}
			text := err.Error()
			if ee := output.AsExitError(err); ee != nil {
				text += " " + ee.Message + " " + ee.Hint + " " + string(ee.Detail)
			}
			if strings.Contains(text, secret) {
				t.Errorf("Do returned an error carrying the secret: %s", text)
			}
		})
	}
}

// Round-9 P2-3. rejectUnknownSubcommand echoed the token when it was all
// lower-case, which is a third path in the class the round-8 fix addressed. Rather
// than tighten the shape test again — the previous attempt is what left this open —
// the token is not echoed at all and the available subcommands are listed instead,
// which is more useful for a typo anyway.
func TestSecretEcho_UnknownSubcommandNeverEchoesTheToken(t *testing.T) {
	for _, token := range []string{
		"ozvbmdlkqnwtyeh",    // all lower-case: the shape rule used to allow this
		"ozvbmd-lkqnwtyeh",   // lower-case with a dash
		"SUPERSECRETSHAREID", // upper-case: previously suppressed
		"abc123def456",       // digits
	} {
		t.Run(token, func(t *testing.T) {
			env := newDriveTestEnv(t, "uk_person", func(w http.ResponseWriter, r *http.Request) {}, nil)
			root, tf := secretsTestRoot(t, env.api.URL, client.Options{NoRetry: true})
			root.SetArgs([]string{"drive", "share", token})
			err := root.Execute()
			if err == nil {
				t.Fatal("an unknown subcommand must fail")
			}
			seen := tf.Out.String() + tf.ErrOut.String() + err.Error()
			if ee := output.AsExitError(err); ee != nil {
				seen += ee.Message + " " + ee.Hint
			}
			if strings.Contains(seen, token) {
				t.Errorf("the unknown-subcommand error echoed %q:\n%s", token, seen)
			}
			// It must still be actionable: name at least one real subcommand.
			if !strings.Contains(seen, "revoke") {
				t.Errorf("the error should list the available subcommands:\n%s", seen)
			}
		})
	}
}
