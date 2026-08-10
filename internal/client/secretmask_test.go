package client

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// escapedSecrets are the values that defeated the old mask. redactSecrets did a
// literal strings.ReplaceAll over the *already-marshalled* body, and
// json.Marshal rewrites `"` to `\"`, `\` to `\\` and a newline to `\n` first —
// so the literal was no longer present and the substitution found nothing to
// replace. The mask silently no-oped on exactly the passwords a user is most
// likely to choose, and because the transport retries, the line was emitted once
// per attempt.
var escapedSecrets = []struct {
	name  string
	value string
}{
	{"double quote", `p@ss"word`},
	{"backslash", `p@ss\word`},
	{"quote and backslash", `p@ss"w\ord`},
	{"newline", "p@ss\nword"},
	{"tab and control char", "p@ss\tword\x01"},
	{"unicode escape territory", "p@ss\u2028word"},
	{"escape-free control", `plain-hunter2`},
}

// TestSecretValues_JSONEscapingDoesNotDefeatTheMask asserts a password whose
// JSON encoding differs from its literal is still masked in the verbose trace,
// and still reaches the wire intact.
func TestSecretValues_JSONEscapingDoesNotDefeatTheMask(t *testing.T) {
	for _, tc := range escapedSecrets {
		t.Run(tc.name, func(t *testing.T) {
			var gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				gotBody = string(raw)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()

			var trace bytes.Buffer
			c := New(
				&config.Config{APIBaseURL: srv.URL},
				&credential.BotCredential{Token: "bf_t"},
				Options{Verbose: true, ErrOut: &trace},
			)
			if _, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/bot/drive/shares",
				Body:         map[string]any{"file_id": json.Number("1"), "password": tc.value},
				SecretValues: []string{tc.value},
			}); err != nil {
				t.Fatalf("Do: %v", err)
			}

			// The wire keeps the real value; redaction is a logging concern.
			var sent map[string]any
			if err := json.Unmarshal([]byte(gotBody), &sent); err != nil {
				t.Fatalf("parse sent body %q: %v", gotBody, err)
			}
			if sent["password"] != tc.value {
				t.Errorf("wire password: got %q, want %q", sent["password"], tc.value)
			}

			assertTraceMasks(t, trace.String(), tc.value)
		})
	}
}

// TestSecretValues_JSONEscapingDoesNotDefeatTheDryRunMask is the same property
// on the other surface. A dry-run description is meant to be safe to paste into
// a ticket, so it is the one a masked-but-not-really value hurts most.
func TestSecretValues_JSONEscapingDoesNotDefeatTheDryRunMask(t *testing.T) {
	for _, tc := range escapedSecrets {
		t.Run(tc.name, func(t *testing.T) {
			c := New(
				&config.Config{APIBaseURL: "https://octo.test"},
				&credential.BotCredential{Token: "bf_t"},
				Options{DryRun: true, ErrOut: io.Discard},
			)
			body, err := c.Do(context.Background(), &Request{
				Method:       http.MethodPost,
				Path:         "/v1/bot/drive/shares",
				Body:         map[string]any{"password": tc.value},
				SecretValues: []string{tc.value},
			})
			if err != nil {
				t.Fatalf("Do: %v", err)
			}
			assertTraceMasks(t, string(body), tc.value)
		})
	}
}

// TestSecretValues_NestedAndRepeatedSecretsAreMasked covers the structural walk:
// a secret below the root object, inside an array, and appearing more than once.
func TestSecretValues_NestedAndRepeatedSecretsAreMasked(t *testing.T) {
	const secret = `p@ss"w\ord`

	var trace bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(
		&config.Config{APIBaseURL: srv.URL},
		&credential.BotCredential{Token: "bf_t"},
		Options{Verbose: true, ErrOut: &trace},
	)
	if _, err := c.Do(context.Background(), &Request{
		Method: http.MethodPost,
		Path:   "/v1/bot/probe",
		Body: map[string]any{
			"top":    secret,
			"nested": map[string]any{"inner": secret},
			"list":   []any{"safe", secret},
			"strs":   []string{secret},
			"joined": "prefix-" + secret + "-suffix",
			"id":     json.Number("18446744073709551615"),
		},
		SecretValues: []string{secret},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	assertTraceMasks(t, trace.String(), secret)
	// The structural walk must copy a json.Number through unchanged, or a uint64
	// id would stop being reported at full precision.
	if !strings.Contains(trace.String(), "18446744073709551615") {
		t.Errorf("the masked body lost uint64 precision:\n%s", trace.String())
	}
}

// TestTransportError_DoesNotLeakASecretPathParameter pins the error envelope.
//
// *url.Error.Error() embeds the full request URL, so an x-octo-secret *path*
// parameter — drive.invite.accept's invite_token, drive.share.access /
// share.download's share_token — was formatted straight into the ExitError. That
// is not a --verbose surface: it is the structured error on stderr, emitted
// unconditionally, which for a CLI whose callers capture stderr into logs and
// model context is a wider exposure than the trace the masking was built for.
func TestTransportError_DoesNotLeakASecretPathParameter(t *testing.T) {
	const token = "SUPERSECRETINVITETOKEN123"

	// A closed listener address: the request fails at dial time with a *url.Error.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	c := New(
		&config.Config{APIBaseURL: deadURL},
		&credential.BotCredential{Token: "uk_t"},
		Options{NoRetry: true, ErrOut: io.Discard},
	)
	_, err := c.Do(context.Background(), &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/invites/" + token + "/accept",
		SecretValues: []string{token},
	})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	ee := output.AsExitError(err)
	if ee == nil {
		t.Fatalf("expected a structured error, got %v", err)
	}
	if strings.Contains(ee.Message, token) {
		t.Errorf("the error envelope leaked the invite token: %s", ee.Message)
	}
	if !strings.Contains(ee.Message, secretMask) {
		t.Errorf("the error envelope should show the masked value: %s", ee.Message)
	}
	// The rest of the diagnostic must survive — the point is redaction, not
	// swallowing the reason.
	if !strings.Contains(ee.Message, "127.0.0.1") {
		t.Errorf("the error envelope should still name the host: %s", ee.Message)
	}
}

// TestTransportError_RedactionSurvivesRetries checks every attempt's error text,
// not just the last: the retry loop re-formats the transport error each time.
func TestTransportError_RedactionSurvivesRetries(t *testing.T) {
	const token = "SUPERSECRETSHARETOKEN"

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	var trace bytes.Buffer
	c := New(
		&config.Config{APIBaseURL: deadURL},
		&credential.BotCredential{Token: "uk_t"},
		// Verbose so the per-retry "last error" lines are captured too.
		Options{Verbose: true, ErrOut: &trace, Timeout: "2s"},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := c.Do(ctx, &Request{
		Method:       http.MethodPost,
		Path:         "/v1/user/drive/shares/" + token + "/access",
		SecretValues: []string{token},
	})
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if ee := output.AsExitError(err); ee != nil && strings.Contains(ee.Message, token) {
		t.Errorf("the final error leaked the share token: %s", ee.Message)
	}
	if strings.Contains(trace.String(), token) {
		t.Errorf("a retry trace line leaked the share token:\n%s", trace.String())
	}
}

// assertTraceMasks fails when secret appears in text in any encoding, and when
// the mask is missing altogether (which would mean the value was dropped rather
// than masked).
func assertTraceMasks(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Errorf("output leaked the secret verbatim:\n%s", text)
	}
	for _, form := range secretForms(secret) {
		if form == secret {
			continue
		}
		if strings.Contains(text, form) {
			t.Errorf("output leaked the secret as %q:\n%s", form, text)
		}
	}
	if !strings.Contains(text, secretMask) {
		t.Errorf("output should show the masked value:\n%s", text)
	}
}
